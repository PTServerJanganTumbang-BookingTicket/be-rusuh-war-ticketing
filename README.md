# be-rusuh-war-ticketing

Backend monorepo for the **Rusuh War Ticketing** platform — a high-contention event
ticket-booking system. Multiple independently-deployable Go services share a common
library, organised as a **multi-module Go workspace**.

## Architecture

- **Multi-module** — each service and the shared library is its own Go module (`go.mod`),
  so every module pins its **own isolated dependency set**.
- **Go workspace (`go.work`)** — ties the modules together for local development: builds
  and refactors across modules are atomic, with no module-proxy publishing.
- **Domain-based Hexagonal Architecture (Ports & Adapters)** inside each service
  (`internal/{domain}/{port,domain,application,infrastructure}`).

```
be-rusuh-war-ticketing/
├── go.work                     # workspace: ties pkg + all services together (local dev)
├── pkg/                        # SHARED LIBRARY — its own module
│   ├── go.mod                  #   module .../be-rusuh-war-ticketing/pkg
│   ├── apinizer/               #   HTTP response/header helpers
│   ├── apperror/               #   application error types
│   ├── dotenv/                 #   env loader
│   └── idgen/                  #   ULID id generation
├── migrations/                 # shared Aurora schema (plain SQL, up/down)
└── services/
    ├── booking/                # BOOKING SERVICE — its own module
        ├── go.mod              #   module .../be-rusuh-war-ticketing/services/booking
        ├── cmd/
        │   ├── server/         #   local HTTP server entrypoint
        │   └── lambda/         #   AWS Lambda (provided.al2023) entrypoint
        ├── internal/
        │   ├── booking/        #   hexagonal domain tree
        │   │   ├── port/{inbound,outbound}/
        │   │   ├── domain/{entity,valueobject,dto,mapper}/
        │   │   ├── application/usecase/
        │   │   └── infrastructure/{http,memory}/
        │   ├── config/
        │   └── delivery/       #   HTTP wiring + lambda proxy
        ├── _mock/              #   go.uber.org/mock generated mocks
    │   ├── Dockerfile  template.yaml  sonar-project.properties
    │   └── Makefile
    ├── user/                   # USER SERVICE — accounts, consent, saved NIKs, tickets
    ├── nik-verify/             # NIK VERIFY — the only service that calls Dukcapil
    ├── outbox-publisher/       # OUTBOX PUBLISHER — drains the outbox onto SQS
    └── notification/           # NOTIFICATION — SQS to SES; owns the queue and DLQ
```

### Module map

| Module | Path | Depends on |
|---|---|---|
| Shared lib | `.../be-rusuh-war-ticketing/pkg` | — (godotenv, ulid, testify only) |
| Booking | `.../be-rusuh-war-ticketing/services/booking` | `pkg` + AWS Lambda / validator |
| User | `.../be-rusuh-war-ticketing/services/user` | `pkg` + AWS Lambda / validator / pgx / go-redis |
| NIK verify | `.../be-rusuh-war-ticketing/services/nik-verify` | `pkg` + AWS Lambda / validator / pgx / go-redis |
| Outbox publisher | `.../be-rusuh-war-ticketing/services/outbox-publisher` | `pkg` + AWS Lambda / SQS / pgx / go-redis |
| Notification | `.../be-rusuh-war-ticketing/services/notification` | `pkg` + AWS Lambda / SES / go-redis |

### Service boundaries worth knowing

- **`nik-verify` is the only service that calls Dukcapil.** It owns the rate
  limiter, the circuit breaker whose state pauses admission (BR-13a) and the
  24-hour `nik:valid:{hmac}` cache. Callers use `POST /nik/validate`.
- **It is also the only holder of the NIK pepper.** `/nik/validate` returns
  `nik_hmac`, so other services store what they are handed and never hold the
  secret. ADR-005 §8 accepts that any process holding the pepper enables
  offline enumeration if compromised, and that the leak is unremediable because
  the pepper can never be rotated — so the holder count stays at one.
- **Access tokens are HS256 JWTs** issued by `user` and verifiable by any
  service sharing `JWT_SECRET`. That secret is separate from the pepper and,
  unlike it, is rotatable.
- **Email goes through the transactional outbox, never directly.** A producer
  writes an `outbox` row in the same transaction as the state change that
  warrants the email; `outbox-publisher` forwards it to SQS; `notification`
  renders and sends it. No service in the request path touches SES, which is
  what makes NFR-10 true rather than aspirational — a provider outage cannot
  fail a registration.

### The email path

```
producer (user, booking, payment-api)
  └─ INSERT INTO outbox   ← same transaction as the state change
       └─ outbox-publisher (every 1s / scheduled)
            ├─ Redis lease: processed:{outbox_id}   ← stops concurrent duplication
            └─ SQS rusuh-notification
                 └─ notification (Lambda, batch of 10)
                      ├─ Redis dedupe: email:sent:{outbox_id}
                      ├─ SES
                      └─ DLQ after 5 attempts        ← NFR-10
```

`notification` owns the queue and the DLQ; `outbox-publisher` takes their URL
and ARN as stack parameters, which is why its deploy job is gated on that one.
Event types and payload shapes live in `pkg/events`, so a renamed type is a
compile error rather than a silently undelivered email.

The booking module resolves `pkg` via a `replace` directive (`=> ../../pkg`), so it builds
both inside the workspace **and** standalone (`GOWORK=off`) without a module proxy.

## Prerequisites

- Go 1.26.x (the CI floats to the latest 1.26 patch via `check-latest: true`)
- Docker (optional, for container builds)
- AWS SAM CLI (for Lambda deploy)
- `go.uber.org/mock` `mockgen` (for regenerating mocks)

## Working in the workspace

From the repo root, the workspace makes every module visible at once:

```bash
# build / test everything (workspace mode)
go build ./...
go test ./...

# work on a single module
cd services/booking && go test ./...
cd pkg             && go test ./...
```

### Adding a new service

1. `mkdir -p services/<name>` and scaffold its hexagonal tree.
2. Give it `services/<name>/go.mod` (module `.../be-rusuh-war-ticketing/services/<name>`),
   with `replace .../pkg => ../../pkg` if it uses the shared library.
3. Add `./services/<name>` to the `use (...)` block in `go.work`.
4. Add the module to the CI matrix and give it a deploy job.

## Dependency isolation

Each module owns its dependency set. To prove a module is **self-contained** (not secretly
relying on the workspace to supply a dependency), build it with the workspace disabled:

```bash
cd services/booking && GOWORK=off go build ./...
cd pkg             && GOWORK=off go build ./...
```

CI runs exactly this check per module, so isolation is enforced, not assumed.

## Testing & coverage

```bash
cd services/booking && go test -coverprofile=coverage.out ./...
go tool cover -func=coverage.out | grep total
```

Coverage gate: **80%** per module, enforced in CI.

## Deploy (booking → AWS Lambda via SAM)

```bash
cd services/booking
make build-lambda          # provided.al2023 bootstrap (arm64)
sam build && sam deploy --stack-name rusuh-booking --resolve-s3 \
  --capabilities CAPABILITY_IAM --no-confirm-changeset --region <region>
```

CI deploys automatically on `push` to `main` when `services/booking/**` or `pkg/**` changes.

## CI/CD

A single root workflow (`.github/workflows/ci.yml`) runs per-module, triggered only for the
modules whose files changed. Every service needs three files or its jobs fail:
`sonar-project.properties` (scan), `template.yaml` (SAM deploy) and a
`build-<Name>Function` Makefile target that `template.yaml` names via
`BuildMethod: makefile`. A service with a server binary also needs a
`Dockerfile`, and `notification` has none on purpose — it is SQS-driven, so it
has no `cmd/server` and is excluded from the hadolint trigger.

1. **detect-changes** — path filter → which modules to run.
2. **lint / test / govulncheck** — matrix over changed modules; each builds with
   `GOWORK=off` (isolation), tests `./...`, and enforces the 80% coverage gate.
3. **hadolint** — Dockerfile lint.
4. **sonarqube** — gated on lint+test+govulncheck.
5. **deploy-booking / deploy-user / deploy-nik-verify / deploy-notification /
   deploy-outbox-publisher** — gated on sonarqube; each runs only when its own
   service or the shared lib changed. `deploy-outbox-publisher` additionally
   waits on `deploy-notification`, because it consumes that stack's queue
   outputs.

A change to `pkg/**` fans out to every dependent service (re-test + redeploy), because
services vendor `pkg` at build time.
