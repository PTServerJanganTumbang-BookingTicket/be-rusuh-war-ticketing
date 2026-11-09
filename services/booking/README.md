# rusuh-booking

Ticket booking service for the Rusuh Booking Ticket assessment.

Built on a **domain-based hexagonal architecture (ports & adapters)**, following the
`boilerplate-golang` structure, exposing a REST API over Go's standard `net/http`.

## Architecture

Hexagonal (ports and adapters). The build plan is `docs/05-booking/` (steps B1–B4).

```
cmd/server/main.go     HTTP server entry point
cmd/lambda/main.go     AWS Lambda entry point (connections reused across invocations)
internal/
├── config/            env-based configuration (Aurora writer, Redis, DEVICE_PEPPER, purchase rules)
├── delivery/          composition root: opens the connections, mounts the routes, Cache-Control: no-store
└── booking/
    ├── domain/                    entities, contract codes, metric names
    ├── port/outbound/             Bookings, Catalog, Stock, Metrics
    ├── application/usecase/       TicketPage (GET /events/{event_id}/tickets)
    └── infrastructure/            postgres (writer), redis (stock MGET), http handlers, metrics (log lines)
```

Shared code: `pkg/grantauth` (grant check on every booking route), `pkg/bookingstate` (status and its API
meaning), `pkg/apinizer` (contract envelope), `pkg/rediskeys`.

## Prerequisites

- Go 1.26.0+
- `golangci-lint` (for linting)
- Docker (for containerized builds and SAM local)
- AWS SAM CLI (only for running/deploying the Lambda target)

## Running locally

The service has two entrypoints that share the same wiring (`delivery.New(ctx, cfg)`, which needs Redis and Postgres: `make deps-up` at the repo root):
`cmd/server/main.go` (plain HTTP server, for local/EC2) and `cmd/lambda/main.go` (native
AWS Lambda handler, Option B). You can run and test both locally.

### 1. As a plain HTTP server

```bash
cp example.env .env   # DATABASE_URL is required; the file points at the compose stack
make run              # go run ./cmd/server  -> listens on :8080

# or with Docker
make docker-build
docker run --rm -p 8080:8080 rusuh-booking:latest
```

### 2. Tests

```bash
make test-module MODULE=services/booking   # from the repo root: unit + integration (real Redis / Postgres)
```

Integration tests skip without `REDIS_ADDR` / `DATABASE_URL`; `make verify` sets both.

### 3. As a Lambda (closest to production) — needs Docker + AWS SAM CLI

```bash
make build-lambda                                   # -> bin/bootstrap (arm64, provided.al2023)

# run a local API Gateway on :3000 (needs Redis + Postgres reachable from the container)
sam local start-api
curl localhost:3000/sys/ping
```

## API Endpoints

| Method | Path | State |
|--------|------|-------|
| GET | `/sys/ping` | health check, no grant |
| GET | `/events/{event_id}/tickets` | ticket selection page (claims GRANTED → SELECTING), behind `pkg/grantauth` |
| GET | `/bookings/{booking_id}` | build step B3 |
| POST | `/bookings` | build step B4 (unmounted until then) |

Responses use the contract envelope `{meta:{code,message,server_time[,error_code]}, data}` (api-contracts §7).

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
| `DATABASE_URL` | Aurora writer DSN (required outside local) | — |
| `DATABASE_MAX_CONNS` | Pool size per Lambda | `2` |
| `REDIS_ADDR` | Redis address | `localhost:16379` |
| `REDIS_PASSWORD` | Redis password | — |
| `REDIS_CLUSTER` | Cluster mode (ElastiCache) | `true` |
| `DEVICE_PEPPER` | HMAC key for guest device hashes; same value as queue (required outside local) | — |
| `MAX_TICKETS_PER_TRANSACTION` | BR-01 per-order limit | `3` |
