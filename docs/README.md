# Backend Implementation Plans

Per-domain implementation plans for `be-rusuh-war-ticketing`. Folders mirror the
**Apidog collection** (`documentations/api/rusuh-booking-apidog.json`) so a reader can go
from an Apidog request straight to the plan that implements it.

## Sources of truth

These docs plan the build. They do **not** redefine behaviour. When they disagree with a
source below, the source wins and the plan gets fixed.

| What | Where (`documentations` repo) |
|---|---|
| Requirements (FR / NFR / BR) | `requirement/requirements.md` v1.1 |
| Architecture of record | `adr/ADR-005-final-architecture-decision.md`, amended by `adr/ADR-006-queue-admission-amendments.md` (queue) |
| API contract | `api/rusuh-booking-apidog.json` (**source of truth**, Apidog project export) + `api/api-contracts.md` v2.7.0. OpenAPI YAML archived (`api/archive/`), not maintained |
| Detailed team implementation plan | `requirement/implementation-plan.md` v2.1 (queue sections amended by ADR-006, see its banner) |

## Layout

| Folder | Apidog folder | Service(s) | Status |
|---|---|---|---|
| [`01-authentication/`](01-authentication/README.md) | Authentication | `services/user` | **documented** — branch `feature/user-notif-nikverify-outboxpublisher` |
| [`02-accounts/`](02-accounts/README.md) | Accounts | `services/user` | **documented** — branch |
| `02-accounts/tickets/` | Accounts › Tickets (`GET /users/me/tickets`, `GET /tickets/{ticket_id}/eticket`) | `services/user` | not started |
| `03-events/` | Events | `services/events` | not started |
| [`04-queue/`](04-queue/README.md) | Queue | `services/queue`, `services/sse-server`, `services/admission-ctl` | **planning** |
| [`05-booking/`](05-booking/README.md) | Booking | `services/booking` (+ SELECTING sweep in `services/admission-ctl`) | **in progress**: GET endpoints built (B1–B3); `POST /bookings` (B4) next |
| [`06-nik/`](06-nik/README.md) | NIK | `services/nik-verify` | **documented** — branch |
| `07-payment/` | Payment | `services/payment-api` | not started |
| `08-admin/` | Admin | `services/payment-api` | not started |

Folders are created when their planning starts. Copy [`_template.md`](_template.md) for each endpoint.

Cross-cutting references: [`redis-keys.md`](redis-keys.md) (every Redis key and channel) ·
[`DEVELOPMENT-FLOW.md`](DEVELOPMENT-FLOW.md) (how we build: spec → review → red → green → verify).

## Conventions

- **One file per Apidog endpoint**, numbered in Apidog order: `01-join-queue.md`, and so on.
  A nested Apidog folder becomes a subfolder with its own numbering (Accounts › Tickets →
  `02-accounts/tickets/01-get-my-tickets.md`).
  Internal components with no endpoint (workers, loops) come after the endpoints.
- Every endpoint file has **at least one Mermaid sequence diagram** of the happy path and the
  main failure branches.
- Cite sources by section (`ADR-005 §6`, `impl-plan §7.2`). Don't copy long passages, link to them.
- Mark unresolved items as **`OPEN:`**, so `grep -rn "OPEN:" docs/` lists every pending decision.
- Task checklists at the bottom of each file are the unit of work. One PR per checkbox
  group is the target.
