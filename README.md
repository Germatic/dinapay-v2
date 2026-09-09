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
export CHECKOUT_BASE_URL='https://checkout.demo.dinaria.com'
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
