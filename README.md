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

The first increment uses an in-memory store. It is suitable for contract and
orchestration tests, not deployment. PostgreSQL transactional persistence and
the shared webhook outbox are the next implementation boundary.

## Boundaries

- `internal/core`: domain model and ports.
- `internal/app`: orchestration; no HTTP or provider code.
- `internal/adapters/httpclient`: routing and connector clients.
- `internal/adapters/memory`: temporary development persistence.
- `internal/transport/httpapi`: public V2 transport.

Provider credentials and provider payloads never enter this service.
