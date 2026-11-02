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
└── services/
    └── booking/                # BOOKING SERVICE — its own module
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
        ├── Dockerfile  template.yaml  sonar-project.properties
        └── Makefile
```

### Module map

| Module | Path | Depends on |
|---|---|---|
| Shared lib | `.../be-rusuh-war-ticketing/pkg` | — (godotenv, ulid, testify only) |
| Booking | `.../be-rusuh-war-ticketing/services/booking` | `pkg` + AWS Lambda / validator |

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
modules whose files changed:

1. **detect-changes** — path filter → which modules to run.
2. **lint / test / govulncheck** — matrix over changed modules; each builds with
   `GOWORK=off` (isolation), tests `./...`, and enforces the 80% coverage gate.
3. **hadolint** — Dockerfile lint.
4. **sonarqube** — gated on lint+test+govulncheck.
5. **deploy-booking** — gated on sonarqube; only when booking or the shared lib changed.

A change to `pkg/**` fans out to every dependent service (re-test + redeploy), because
services vendor `pkg` at build time.
