# rusuh-booking

Ticket booking service for the Rusuh Booking Ticket assessment.

Built on a **domain-based hexagonal architecture (ports & adapters)**, following the
`boilerplate-golang` structure, exposing a REST API over Go's standard `net/http`.

## Architecture

```
cmd/server/main.go                  HTTP server entry point (config load, logger, server, graceful shutdown)
cmd/lambda/main.go                  AWS Lambda entry point (native handler, Option B)
internal/
├── config/                         env-based configuration
├── delivery/                       composition root — wires adapters -> usecase -> controller
├── shared/entity/                  cross-domain shared types (validation errors)
└── booking/
    ├── port/
    │   ├── inbound/                IBookingUsecase (driving port)
    │   └── outbound/               IBookingRepository (driven port)
    ├── domain/
    │   ├── entity/                 Booking business entity
    │   ├── dto/                    request/response DTOs
    │   ├── valueobject/            immutable value types
    │   └── mapper/                 entity <-> DTO transformation
    ├── application/usecase/        business logic (implements inbound port)
    └── infrastructure/
        ├── http/                   handlers + router (implements the handler interface)
        └── memory/                 in-memory repository (implements outbound port)
pkg/
├── apinizer/                       health check + JSON response helpers
├── apperror/                       transport-agnostic application errors
└── dotenv/                         .env loader
_mock/                              generated gomock mocks (go.uber.org/mock)
```

Dependencies flow inward: `infrastructure -> application -> domain`. The usecase
constructor returns the inbound port interface, so the composition root depends only on
abstractions.

## Prerequisites

- Go 1.26.0+
- `mockgen` from `go.uber.org/mock` (for mock generation)
- `golangci-lint` (for linting)
- Docker (for containerized builds and SAM local)
- AWS SAM CLI (only for running/deploying the Lambda target)

## Running locally

The service has two entrypoints that share the same wiring (`delivery.New(cfg)`):
`cmd/server/main.go` (plain HTTP server, for local/EC2) and `cmd/lambda/main.go` (native
AWS Lambda handler, Option B). You can run and test both locally.

### 1. As a plain HTTP server

```bash
cp example.env .env   # optional; all values have defaults
make run              # go run ./cmd/server  -> listens on :8080

# or with Docker
make docker-build
docker run --rm -p 8080:8080 rusuh-booking:latest
```

### 2. Tests (no Docker, no AWS)

```bash
go mod tidy             # first time: pulls aws-lambda-go, aws-lambda-go-api-proxy, oklog/ulid
make test               # go test -v ./...
make unit-test-coverage # coverage profile + func summary

# just the delivery (integration + lambda-proxy) tests
go test -v ./internal/delivery/...
```

Two integration tests exercise the fully-wired stack without a live server:
`internal/delivery/integration_test.go` drives it over a real `httptest` server, and
`internal/delivery/lambda_proxy_test.go` feeds synthetic API Gateway v2 events through
the Lambda proxy adapter against the same mux.

### 3. As a Lambda (closest to production) — needs Docker + AWS SAM CLI

```bash
make build-lambda                                   # -> bin/bootstrap (arm64, provided.al2023)

# one-shot invoke with the sample event
sam local invoke BookingFunction -e events/create-booking.json

# or run a local API Gateway on :3000
sam local start-api
curl -X POST localhost:3000/bookings \
  -H 'Content-Type: application/json' \
  -d @events/create-booking.json
```

> The in-memory repository resets on each SAM container restart, so a `GET` after a
> `POST` across restarts will not find the booking. This is expected for the demo;
> swap in a persistent repository adapter (e.g. DynamoDB) behind `IBookingRepository`
> for durable state.

## API Endpoints

| Method | Path                        | Description                        |
|--------|-----------------------------|------------------------------------|
| GET    | `/sys/ping`                 | Health check                       |
| POST   | `/bookings`                 | Create a booking (`PENDING_PAYMENT`) |
| GET    | `/bookings/{booking_id}`    | Get a booking by id                |

All responses use the `{meta:{code,message}, data}` envelope (api-contracts.md §7).

### Create booking

```bash
curl -X POST localhost:8080/bookings \
  -H 'Content-Type: application/json' \
  -d '{
    "event_id": "evt_rusuh_2026",
    "tickets": [
      {"category_id": "cat_vip", "nik": "3171000000000001", "holder_email": "a@example.com"}
    ],
    "contact_email": "contact@example.com",
    "terms_version": "v1"
  }'
```

## Testing

```bash
make test               # go test -v ./...
make unit-test-coverage # coverage profile + func summary
make mock               # regenerate gomock mocks after changing port interfaces
```

## Linting

```bash
make golint
```

## CI/CD

GitHub Actions (`.github/workflows/cicd.yml`), adapted from the ParkirPintar assessment
pipeline. Runs on push / PR to `main` and `cicd`:

```
golangci-lint ─┐
test ──────────┼─► sonarqube ─► deploy-lambda (SAM, push only)
govulncheck ───┘
hadolint  (independent)
```

- **`golangci-lint`** — static analysis / style.
- **`test`** — runs tests and enforces the **80% coverage gate**.
- **`govulncheck`** — dependency vulnerability scan against the Go vuln DB (fails on a reachable vuln).
- **`hadolint`** — Dockerfile linting.
- **`sonarqube`** — quality scan with the coverage report (`needs` the three gates above).
- **`deploy-lambda`** — `sam build` → `sam deploy` (native Lambda behind API Gateway HTTP API), on `push` only, AWS auth via OIDC.

### Required repository secrets and variables

Set these under **Settings → Secrets and variables → Actions**.

| Kind     | Name                   | Used by      | Purpose                                             |
|----------|------------------------|--------------|-----------------------------------------------------|
| Secret   | `SONAR_TOKEN`          | `sonarqube`  | Auth token for the SonarQube server.                |
| Secret   | `AWS_DEPLOY_ROLE_ARN`  | `deploy-lambda` | ARN of the IAM role GitHub assumes via OIDC to deploy. |
| Variable | `SONAR_HOST_URL`       | `sonarqube`  | SonarQube server URL.                               |
| Variable | `AWS_REGION`           | `deploy-lambda` | AWS region to deploy the stack into (e.g. `ap-southeast-1`). |

> **AWS OIDC setup (one-time):** add the GitHub OIDC identity provider
> (`token.actions.githubusercontent.com`, audience `sts.amazonaws.com`) to the AWS
> account, then create the IAM role referenced by `AWS_DEPLOY_ROLE_ARN` with a trust
> policy scoped to this repo/branch and permissions for CloudFormation, Lambda, API
> Gateway, IAM (PassRole) and S3 (SAM artifact bucket).

## Environment Variables

| Variable     | Description                          | Default |
|--------------|--------------------------------------|---------|
| `APP_PORT`   | Application port                     | `8080`  |
| `APP_ENV`    | Environment (local/preprod/prod)     | `local` |
| `LOG_LEVEL`  | Log level (debug/info/warn/error)    | `info`  |
| `LOG_FORMAT` | Log format (json/text)               | `json`  |
