# payment-api

The only component that talks to Midtrans. It owns the five payment endpoints
and the webhook that turns a settlement into tickets.

| Endpoint | Credential | What it does |
|---|---|---|
| `GET /payments/{booking_id}/methods` | `X-Grant-Token` | The method catalogue, plus how much of the window is left. |
| `POST /payments/{booking_id}` | `X-Grant-Token` | First charge. Returns VA number or QR. |
| `POST /payments/{booking_id}/retry` | `X-Grant-Token` | Switch method inside the same window. |
| `GET /payments/{booking_id}/status` | `X-Grant-Token` | What the client polls. |
| `POST /payments/webhook` | the body's `signature_key` | Midtrans notification. Always answers 200. |

## Why a grant and not the account token

The four credentials of ADR-005 §17 are not interchangeable. `Authorization:
Bearer` identifies an **account**; it says nothing about whether that account
is holding seats. The grant says exactly that and nothing else, which is why it
is the credential here: a logged-in customer with no grant must not be able to
pay for a booking, and a guest with a grant must be able to.

`pkg/grantauth` resolves `X-Grant-Token` against the `grant:{token}` hash and
hands the handler `{event_id, booking_id}` plus **one** of `account_id` or
`device_hash` (ADR-006 §8.13a). Two consequences the code depends on:

- A **guest** grant additionally requires `X-Device-Id`, and
  `HMAC(DEVICE_PEPPER, X-Device-Id)` must equal the stored `device_hash`
  (ADR-006 §8.9) → otherwise `401 DEVICE_MISMATCH`. An **account** grant is
  valid from any device, because the account token is the stronger claim.
- The token is resolved **before** the device is checked, so an unknown grant
  is always `GRANT_TOKEN_INVALID`. Checking the device first would let a
  caller distinguish "no such grant" from "that grant exists, wrong device".

A broken Redis is a 503, never a 401. Treating an infrastructure failure as an
authentication failure would tell every paying customer their session expired.

## What it does not touch

Nothing in this service reads or writes a queue counter. `slots:open:{ev}` is
gone and `advance_now_serving.lua` has exactly one caller, `admission-ctl`
(ADR-006 §3.2, §4.1). The webhook's old inline `DECR slots:open` +
`EVALSHA advance_now_serving` + `PUBLISH` block was removed **with no
replacement**: admission is driven by the admission controller's own clock, and
a second writer racing it was the bug, not the feature.

The implementation plan §10.5 still describes the old block. Where the two
disagree, ADR-006 wins.

## The webhook

Midtrans **Core API**, not Snap. The differences that matter:

- The signature is `signature_key` **in the body**, not an
  `X-Callback-Signature` header:
  `SHA512(order_id + status_code + gross_amount + ServerKey)`, plain
  concatenation, no separators. Verified with a constant-time compare before
  anything else in the payload is read.
- `order_id` is `{booking_id}:{payment_attempts}`, so a notification names the
  attempt it belongs to, not just the booking. A late notification for a
  superseded attempt is recorded and otherwise ignored — except a late
  **settlement**, which is a real payment that must not be silently dropped.
- `gross_amount` is a decimal string and is fed to the signature **as-is**.
  Re-formatting it (`"50000"` for `"50000.00"`) produces a valid-looking
  signature that never matches.

Every notification is recorded in `payment_events` under
`gateway_event_id = order_id + ':' + transaction_status + ':' + status_code`,
which is UNIQUE. A duplicate rolls the transaction back and still answers 200 —
Midtrans retries anything else, and a retry storm against a settled booking is
worse than a swallowed duplicate.

**It always answers 200.** A 500 makes Midtrans retry, and the failures worth
retrying here are the ones where our own database is down — exactly when a
retry storm is least affordable. Failures are logged and alarmed, not returned.

### Late settlement

A settlement that lands after the booking expired and the seats were resold is
the one case the service cannot fix by itself. It emits a
`payment.failed_notice` with `FailureDisputed`, which routes the customer to
support with the payment reference attached. There is no automatic refund path;
ADR-006 did not grant one.

## Methods, and why only two

Only `bank_transfer` (BCA, BNI, BRI, CIMB) and `gopay` are opened. The reason
is `custom_expiry`: it is sent with every charge so the gateway's deadline and
BR-05's ten-minute window end on the same second, and its `order_time` is the
booking's `held_at`, not now — a customer who spends four minutes choosing a
method gets six, not ten.

Card and over-the-counter channels do not honour `custom_expiry`. A VA that
stays payable for 24 hours after the seats are resold is the failure mode this
excludes. The same choice removes the reversal cases: `deny`/`refund` arrive on
Permata, Mandiri Bill and Indomaret, none of which are opened.

## Running locally

```bash
cp example.env .env
make run
```

`MIDTRANS_SERVER_KEY` is empty in `example.env` and the service **refuses to
start** without it. Put a sandbox server key there; `MIDTRANS_BASE_URL` already
points at sandbox.

To exercise the webhook, compute the signature the same way the service does:

```bash
ORDER=<booking_uuid>:1; STATUS=200; AMOUNT=150000.00; KEY=<server key>
printf '%s%s%s%s' "$ORDER" "$STATUS" "$AMOUNT" "$KEY" | sha512sum
```

## Configuration

| Variable | Required | Notes |
|---|---|---|
| `DEVICE_PEPPER` | yes, no default | Keys the guest device binding. **Not** the NIK pepper and **not** `JWT_SECRET`. Must match whatever issues grants. Rotatable, but only *between* sales: rotating mid-sale returns `401 DEVICE_MISMATCH` to every guest holding a grant. |
| `MIDTRANS_SERVER_KEY` | yes, no default | Signs charges **and** verifies every notification. Missing, the service would reject every valid callback and confirm no booking — while reporting healthy. |
| `MIDTRANS_BASE_URL` | no | Sandbox. A deployment that forgets this should charge nobody rather than everybody. |
| `DATABASE_URL` | yes | Shared Aurora. Front it with RDS Proxy. |
| `REDIS_ADDR` | yes | Grants, breaker state, credential cleanup. |
| `PAYMENT_TTL_SECONDS` | no | 600. BR-05's window, and the `custom_expiry` sent to Midtrans. |
| `MIN_RETRY_REMAINING_SECONDS` | no | 60. Below this remaining, a method switch is refused rather than handed a VA that dies first. Must be below `PAYMENT_TTL_SECONDS`; startup fails otherwise. |
| `MAX_PAYMENT_ATTEMPTS` | no | 5. Each attempt mints a new VA or QR at Midtrans. |
| `SETTLED_TOMBSTONE_SECONDS` | no | 3600. How long `settled:{booking_id}` answers queue re-entrancy after the queue credential is deleted (contract §3.3). |
| `MIDTRANS_TIMEOUT_MS` | no | 10000. Not optional in effect: a hung gateway otherwise holds the invocation until the platform kills it and the breaker never sees the failure. |
| `PAYMENT_BREAKER_FAILURE_THRESHOLD`, `PAYMENT_BREAKER_OPEN_SECONDS` | no | 5 / 30. The breaker gates **new** charges only. A replay of an existing charge's instructions is served while it is open, because that answer needs no gateway call. |
| `PAYMENT_ICON_BASE` | no | Cosmetic. A wrong value shows broken images; it fails no payment. |
| `DATABASE_MAX_CONNS` | no | 4. Keep small: every warm Lambda container holds its own pool. |

## Database

Writes `bookings`, `booking_items`, `payment_events`, `tickets` and the outbox.
Migration `0006_booking_domain` adds everything this service needs that was not
already there — `payment_events`, `booking_items`, `nik_claims`,
`bookings.grant_token`, `tickets.booking_item_id`. Migrations live in
`/migrations` at the repo root and are applied by the pipeline, not by this
service.

Ticket issuance is idempotent through
`ON CONFLICT (booking_item_id) DO NOTHING RETURNING id`: a webhook delivered
twice before the duplicate guard commits still issues one ticket per seat.
