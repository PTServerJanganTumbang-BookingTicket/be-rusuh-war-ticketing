# Migrations

Plain SQL, applied in filename order against the shared Aurora PostgreSQL
cluster described in ADR-005 §4. Each migration has an `.up.sql` and a
`.down.sql`.

## How these get applied

By hand, with a SQL client. The CD pipeline deliberately does not run them —
a POC decision recorded in `DEPLOY.md` — so the schema is whatever someone
last executed against the database.

Aurora has a public endpoint in the POC, so DBeaver connects to it directly;
`DEPLOY.md` §"Connecting with DBeaver" has the host, credentials and SSL
settings. Execute each file **as a script**, not statement by statement: every
one of them opens and closes its own transaction, so running them a statement
at a time breaks that guarantee.

Order matters: 0001 creates `accounts`, and every later file has a foreign key
to it. Running any of them first fails with `relation "accounts" does not
exist`.

| | Creates | |
|---|---|---|
| `0001_user_domain` | `accounts`, `consent_records`, `saved_niks`, `magic_tokens` | |
| `0002_tickets_read_model` | `tickets`, `events` | FK → `accounts` |
| `0003_outbox_and_refresh` | `outbox`, `refresh_tokens` | FK → `accounts` |
| `0004_engagement_read_model` | `bookings`, `queue_entries` | FK → `accounts` |
| `0005_outbox_drop_tracking` | adds `outbox.dropped_at`, `outbox.drop_reason` | needs `outbox` from `0003` |
| `0006_bookings_grant_lookup` | `bookings.grant_token` (unique), `UNIQUE (event_id, queue_token)` | needs `bookings` from `0004` |
| `0007_bookings_admission_indexes` | partial indexes for the admission count and the GRANTED sweep | `0006` |
| `0008_ticket_categories` | `ticket_categories` | FK → `events` |
| `0009_booking_domain` | `booking_items`, `nik_claims`, `tickets.booking_item_id`, SELECTING deadline index | `0008` |
| `0010_drop_terms_and_holder_email` | drops the terms columns and `booking_items.holder_email` | `0009` |
| `0011_payment_events` | `payment_events` (`gateway_event_id` unique), payment-deadline index for the HELD / AWAITING_PAYMENT sweep | `0010` |
| `0012_drop_consent_policy_version` | drops `consent_records.policy_version`, which was `NOT NULL` and no longer written — until this runs, every registration fails the constraint | `0001` |
| `0013_bookings_paid_at` | adds `bookings.paid_at`, the gateway settlement time §5.4 reports. Until this runs, `paid_at` is substituted with the payment deadline | `0004` |
| `0014_payment_method_switches` | append-only operator kill switch for a payment method or one bank under it. Subtracts from the code catalogue, never defines it | — |

Every statement uses `IF NOT EXISTS`, so re-running a file is harmless. That
is the only thing standing in for a migration runner, so keep it true: a
migration written without `IF NOT EXISTS` becomes a file nobody can safely
apply twice, and with no ledger there is no way to know whether it already ran.

Two consequences of owning this by hand:

- Nothing records what is applied. There is no `schema_migrations` table unless
  someone creates one, so "does this environment have 0005?" is answered by
  looking at the database.
- A service deploy cannot tell. The pipeline reports success whether or not the
  schema matches the code, so a missing table surfaces as a runtime 500 rather
  than a failed release.

`infra/migrator/` still holds a CodeBuild project that applies these in order
from inside the VPC, with an advisory lock and a `schema_migrations` ledger. It
is not wired into the pipeline; `DEPLOY.md` has the command to run it on demand
and where to re-insert the job. `golang-migrate` also matches this naming
convention as-is, if a runner is preferred later:

```bash
migrate -path ./migrations -database "$DATABASE_URL" up
```

## Ownership

| Tables | Owned by |
|---|---|
| `accounts`, `consent_records`, `saved_niks`, `magic_tokens`, `refresh_tokens` | `services/user` |
| `tickets`, `events` | `services/booking` — `services/user` reads only |
| `bookings` | `services/booking` — `services/user` reads only (BR-09) |
| `queue_entries` | `services/queue` — `services/user` reads only (BR-09) |
| `outbox` | written by every producer, drained by `services/outbox-publisher` |
| `ticket_categories` (0008), `booking_items`, `nik_claims` (0009) | `services/booking` |

`bookings` and `queue_entries` are transcribed from ADR-005 §4 because
`services/booking` and `services/queue` do not exist in this repository yet.
When they land, their migrations become authoritative — migration 0004 should
shrink to nothing rather than be allowed to drift from them.

`services/nik-verify` reads `accounts.nik_hmac` and `saved_niks.nik_hmac` to
answer `source: registered | saved` without calling Dukcapil. It writes nothing.

`services/outbox-publisher` reads and marks `outbox` rows and owns no table.

## Things the schema enforces that code must not be trusted to

- `saved_niks.verified_at NOT NULL` — no unverified NIK can be stored, because
  checkout treats a stored NIK as proof and would skip Dukcapil (ADR-005 §8).
- `consent_records` UPDATE/DELETE trigger — the consent record is evidence under
  UU PDP No. 27/2022 and must survive repair scripts.
- `accounts_nik_hmac_key` — BR-11, one NIK to one account globally.
- `accounts_nik_all_or_nothing` — a crashed registration cannot leave an account
  that reads as NIK-verified without the hash behind it.

## Retention

`outbox` and `refresh_tokens` both accumulate and neither records anything once
drained or lapsed. `migrations/0003` carries the two sweep statements; no job
runner is committed to run them yet. The NIK retention floor in ADR-005 §8 does
not apply to either — neither holds a `nik_hmac`.

Note that `outbox` payloads carry plaintext magic-link tokens, because the email
cannot be rendered without them. That makes the table short-lived operational
state with a real blast radius, not an audit log: anyone who can read it during
the window can log in as the addressee.

## What is NOT in the schema

`BR-12` (max 5 saved NIKs per account) is a row count, which no constraint can
express. It is enforced in `AddSavedNIK` inside a transaction holding
`SELECT … FROM accounts WHERE id = $1 FOR UPDATE`, which serialises concurrent
adds for one account. Removing that lock reintroduces the race.
