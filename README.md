# Dinapay V2

Provider-neutral Payments V2 API and orchestrator. V1 traffic remains in the
existing `dinapay` service.

Contracts are owned by
[`Germatic/dinapay-contracts`](https://github.com/Germatic/dinapay-contracts)
and this initial scaffold targets commit `450139e`.

## Run locally

```sh
export API_KEYS='demo-key=account1:merchant1'
export ROUTER_URL='http://localhost:8091'
export CONNECTORS='connector-binancepay-v2=http://localhost:8092,connector-transferdirecto-v2=http://localhost:8093'
# Optional: configure only after the Dinaria checkout is publicly reachable.
export CHECKOUT_BASE_URL=''
go run ./cmd/dinapay-v2
```

Set `DB_URL` to use PostgreSQL. Without it, the service falls back to an
in-memory store suitable only for local contract tests. The PostgreSQL adapter
reserves idempotency before external calls and atomically commits the payment
and V2 rows in the existing shared webhook outbox.

The migration creates only `dinapay_v2_*` business tables. It adds
`webhooks.api_version` with default `1`; existing registrations therefore keep
their V1 behavior. V2 deliveries target registrations explicitly marked `2`.

## Boundaries

- `internal/core`: domain model and ports.
- `internal/app`: orchestration; no HTTP or provider code.
- `internal/adapters/httpclient`: routing and connector clients.
- `internal/adapters/memory`: temporary development persistence.
- `internal/adapters/postgres`: durable V2 state and shared webhook outbox.
- `internal/adapters/dinacore`: adapter for the current ledger API.
- `internal/transport/httpapi`: public V2 transport.

Provider credentials and provider payloads never enter this service.

## Financial reconciliation

Payout reconciliation is asynchronous and disabled by default. Set
`RECONCILIATION_MODE=observe` only after the deployed Dinacore version exposes
the authenticated ledger-evidence endpoint. Optional controls are
`RECONCILIATION_BATCH_SIZE` (default `50`) and `RECONCILIATION_INTERVAL`
(default `10s`).

Terminal payout transitions enqueue constant-time work in the same database
transaction. The worker then compares the expected debit and compensation with
Dinacore's immutable ledger and records idempotent findings; it never changes a
balance. A payout resource version protects newer transitions from being
removed by an older concurrent reconciliation attempt. The metrics
`dinapay_reconciliation_findings_open`,
`dinapay_reconciliation_findings_critical`,
`dinapay_reconciliation_findings_oldest_seconds`, and
`dinapay_reconciliation_queue_pending` expose operational health without
high-cardinality labels.

## Data-policy observation

When `CONTROL_PLANE_URL` and `CONTROL_PLANE_RUNTIME_TOKEN` are configured,
Dinapay V2 refreshes the active data-policy snapshot outside the transaction
path. `DATA_POLICY_REFRESH_INTERVAL` defaults to `30s`. A failed refresh keeps
the last known good snapshot; no payment or payout performs a synchronous
Control Plane call.

The initial rollout is observation-only. After routing, Dinapay combines
global, account and merchant policies with the selected connector's immutable
technical requirements. Missing required fields increment
`dinapay_data_policy_missing_fields_total` and produce a structured internal
log, but do not reject the transaction. Merchant IDs are intentionally absent
from metric labels to keep cardinality bounded.

## Provider events

Connectors publish the normalized contract to
`POST /internal/v1/provider-events` using `SERVICE_TOKEN`. The consumer stores
an inbox record before applying a transition, rejects provider/connection/order
mismatches, ignores state regressions, and creates the public webhook in the
same database transaction.

A first transition to `confirmed` inserts an idempotent `cashin` row in the
existing `dinacore_balance_outbox`. It does not change the payment to `paid`;
that state remains reserved for a later settlement/reconciliation decision.

## Consolidated payment reads

V2 is the complete read surface during the gradual migration:

- `GET /v2/payments/{transactionId}` reads a native V2 payment first and falls
  back to an authorized legacy V1 payment.
- `GET /v2/payments?limit=50&cursor=...` returns V1 and V2 payments in one
  globally ordered page. The opaque cursor uses `creationDate` and
  `transactionId`, so equal timestamps do not skip or repeat rows.
- Legacy rows are mapped to the V2 public contract. Missing legacy data is
  omitted rather than fabricated.
- `actionUrl` is normalized to the Dinaria checkout only when
  `CHECKOUT_BASE_URL` is configured. Otherwise it uses the provider's
  recommended redirect URL. Provider-native alternatives remain under
  `paymentData`.

Payment origin remains internal. It will route future operations to the
correct implementation and is not exposed in the public response.

Operational dashboards use `GET /internal/v1/dashboard/payments`, authenticated
with the dedicated `DASHBOARD_READ_TOKEN`. It provides the same consolidated
V1+V2 page with optional `accountId` and `merchantId` filters, without borrowing
a customer API key or granting the dashboard direct database access.

## Refund migration

The public V2 refund endpoints are available for payments whose internal
origin is V1:

- `POST /v2/payments/{transactionId}/refunds`
- `GET /v2/payments/{transactionId}/refunds`
- `GET /v2/refunds/{refundId}`

V2 authenticates and authorizes the payment first, then delegates through a
loopback legacy adapter while preserving the caller's idempotency key. The
adapter converts the legacy resource to the V2 contract. Credentials are not
persisted or logged. Native V2 refunds remain explicitly unsupported until the
connector refund operation and transactional ledger compensation are wired.
