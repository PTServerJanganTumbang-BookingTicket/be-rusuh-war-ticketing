# user

Identity: accounts, magic-link login, consent, the Daftar NIK Teman list, and
the customer's own tickets.

Login is **optional** (FR-29). Everything here exists to make an account worth
having — chiefly to cut Dukcapil calls (NFR-12) — not to gate the purchase.

## Endpoints

| Method | Path | Requirement |
|---|---|---|
| POST | `/auth/magic-link` | FR-31 |
| POST | `/auth/magic-link/resend` | FR-31 |
| GET | `/auth/verify` | FR-31 |
| GET | `/auth/verify-email` | FR-31 |
| POST | `/auth/refresh` | contract §1.4 |
| POST | `/auth/register` | FR-29, FR-30, FR-31a..d |
| GET&nbsp;/&nbsp;POST&nbsp;/&nbsp;DELETE | `/accounts/me/saved-niks[/{id}]` | FR-32a..d |
| GET | `/users/me/tickets` | contract §6.1 |
| GET | `/tickets/{ticket_id}/eticket` | FR-27, contract §6.2 |

## Three things to know before changing this service

**It never calls Dukcapil.** All verification goes through `nik-verify`
(`POST /nik/validate`), which owns the rate limiter, the circuit breaker whose
state pauses admission (BR-13a) and the 24-hour cache. It also returns
`nik_hmac`, so the NIK pepper never enters this process — there is no
`NIK_HMAC_KEY` here, by design. See ADR-005 §8 for why the pepper's blast
radius matters: it can never be rotated, so a leak can never be remediated.

**NIKs are stored as a hash plus a mask, never as digits.** `nik_hmac` is the
only lookup key; `nik_masked` exists because a hash cannot be unmasked and the
contract requires a masked form in responses (FR-32c). Both are written from
the values `nik-verify` hands back. No endpoint returns the full sixteen
digits, and the DTOs have no field that could.

**Account creation runs only through `POST /auth/register`.** Redeeming a magic
link does not create one. An account must carry a Dukcapil-verified NIK and a
consent record (FR-31a, FR-31c, BR-11), and both are written in a single
transaction — an account with a verified NIK and no consent record is a UU PDP
violation, and that is exactly what a crash between two separate writes leaves.

## Running locally

```bash
cp example.env .env
# requires Postgres and Redis; apply /migrations first
make run
```

`nik-verify` must be running too (`DUKCAPIL_MODE=stub` is enough).

Nothing sends mail locally unless `outbox-publisher` and `notification` are
also running against a real queue and SES. To pick up a login link without
them, read the plaintext token out of the queued `outbox` row:

```sql
SELECT payload->>'token' FROM outbox
 WHERE type = 'auth.magic_link' ORDER BY created_at DESC LIMIT 1;
```

## Configuration

| Variable | Required | Notes |
|---|---|---|
| `DATABASE_URL` | yes | Shared Aurora. Front it with RDS Proxy. |
| `NIK_VERIFY_BASE_URL` | yes | Startup fails without it: this service cannot verify a NIK on its own. |
| `JWT_SECRET` | yes, no default | Signs access tokens; shared with every service that verifies them. NOT the NIK pepper, and unlike the pepper it is rotatable. |
| `REDIS_ADDR` | yes | Magic-link rate limiting. |
| `SALE_WINDOW_SECONDS` | no | 21600 fallback. This service never queues anyone — it needs the value because the session TTL is derived from it. The queue owns it and the real number is open (ADR-005 Bagian 17 #10). |
| `ACCESS_TOKEN_TTL_SECONDS` | no | Defaults to `SALE_WINDOW_SECONDS` + 1140 (22740s). Leave unset; set it only to deliberately shorten the revocation window. Startup refuses anything below `SALE_WINDOW_SECONDS` + 990. |
| `SECURE_COOKIES` | no | On everywhere but local; cannot be disabled outside local. |
| `COOKIE_DOMAIN` | no | Empty means host-only. Also scopes the Origin check. |
| `DATABASE_MAX_CONNS` | no | 4. Keep small: every warm Lambda container holds its own pool. |

## Email goes through the outbox, not a mailer

This service has no mailer and never talks to SES. It writes an `outbox` row in
the **same transaction** as the state change that warrants the email, and
`outbox-publisher` → SQS → `notification` does the rest (ADR-005 §4, §9).

That is what makes NFR-10 true rather than aspirational: an email provider
outage cannot fail a registration, because nothing in the request path touches
one. It also removes the opposite failure — an account that exists with no
verification email queued, which is what a crash between two separate writes
would leave.

`RegisterAccount` writes four things at once for the same reason: the account,
its consent record, its verification token and the `account.verify` event.
Every partial outcome is a defect nothing downstream can detect, and one of
them (a verified NIK with no consent record) is a UU PDP violation.

## Sessions

Access tokens are HS256 JWTs lasting **`SALE_WINDOW_SECONDS` + 1140 seconds**
— 22740s, 6h19m, at the 21600s fallback (contract §1.3 `expires_in: 22740`).

That is deliberately **not** `GRANT_TOKEN_TTL_SECONDS`. The grant's 1080s bounds
`granted_at` → payment deadline. The session starts at **login**, which happens
before the queue, so it has to cover the wait as well: sale window + 930s
journey + 60s BR-22 pause grace + 150s margin.

Contract v2.3 set this to 1080 because 1080 was derived from the journey —
correct for the grant, a coincidence for the session. At `ratePerMin` 318 and
`QUEUE_CAPACITY` 300000 the last position waits about 15.7 hours, so an
18-minute session died in the waiting room for most of the queue. What broke was
not the waiting room — the stream uses the cookie — but
`GET /accounts/me/saved-niks` on the Detail Ticket page: the saved-NIK list 401s
exactly when the customer opens it, forcing NIKs to be typed by hand, forcing a
Dukcapil call, and Dukcapil is fail-closed (BR-13).

The session now outlives the whole journey, so no refresh ever lands
mid-payment. A refresh that fails there would throw the customer out, which is
the "jangan PHP" failure NFR-02 exists to prevent.

It doubles as the revocation window: the token is verified without consulting
any store, so nothing can take it back early. That is why the long-lived half of
the session is the refresh token — opaque, stored as SHA-256, revocable, and
rotating on every use: redeeming one issues a replacement and spends the
original.

Replaying a spent token is the signature of theft, so the whole rotation chain
is revoked and the customer signs in again (OAuth 2.0 BCP §4.13). Each login
opens its own family, so revoking one chain does not sign the customer out of
their other devices.

`POST /auth/refresh` is documented in contract §1.4 (added in v2.2). It is
**not** part of the booking journey — the access token outlasts it — so it is
never called mid-payment. It exists to carry a session across days, which is
what keeps a returning customer from having to request a new magic link.

## redirect_context (BR-09)

On login, the account's one live engagement is resolved — a non-terminal
booking, else a queue position — and returned as `redirect_context` so the
customer lands back where they were.

"One" is BR-07: a logged-in customer holds at most a single queue position or
booking per event. A booking outranks a queue entry, because once a slot is
granted the booking is where they actually are.

The lookup reads `bookings` and `queue_entries`, which `booking-service` and
`queue-service` own and which do not exist in this repository yet — migration
0004 transcribes the minimum shape from ADR-005 §4. **When those services land,
their migrations become authoritative.** The lookup is best-effort: a failure
omits the field rather than failing the login, because landing on the home page
is degraded while a failed login is broken.

## Database

Owns `accounts`, `consent_records`, `saved_niks`, `magic_tokens`,
`refresh_tokens`. Writes `outbox` rows, which `outbox-publisher` drains. Reads
`tickets` and `events` (`booking-service`) and `bookings` and `queue_entries`
(`booking-service`, `queue-service`). Migrations are in
`/migrations` at the repo root; `migrations/README.md` lists the invariants the
schema enforces that application code must not be trusted to.
