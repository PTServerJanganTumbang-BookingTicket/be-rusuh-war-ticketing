# nik-verify

The single gateway to Dukcapil. Every NIK verification in the system —
registration, adding a saved NIK, checkout — goes through `POST /nik/validate`.

## Why it is its own service

ADR-005 §4 puts it beside `queue-service`, `booking-service` and `payment-api`
as the only component with a line drawn to the Dukcapil API. Three things it
owns cannot work correctly if callers dial Dukcapil themselves:

| It owns | What breaks if a second caller bypasses it |
|---|---|
| The rate limiter | `DUKCAPIL_RATE_LIMIT_RPS` is the contracted ceiling on the whole sale. Two independent limiters enforce nothing. |
| The circuit breaker | `dukcapil:breaker:state` is read by `admission-ctl` to pause admission (BR-13a). A private breaker blocks calls here while the queue carries on granting slots nobody can use. |
| The `nik:valid:{hmac}` cache | Bypassing it means paying Dukcapil per call for answers already held (NFR-12). |

## Why it returns `nik_hmac`

This is the only service that holds the NIK pepper. Callers store the hash they
are handed and never see the secret.

ADR-005 §8 accepts that any process holding the pepper enables offline
enumeration of every stored NIK if compromised, and that the leak can never be
remediated because the pepper cannot be rotated. Keeping it in one service
instead of three is the cheapest available reduction in that blast radius.

## Verification order

The sequence in `usecase.Validate` is the design:

1. **Normalise and check format** — a malformed NIK must never cost a paid call.
2. **Stored evidence** (`accounts.nik_verified_at`, `saved_niks.verified_at`) —
   free, and the entire reason NIK Terdaftar and Daftar NIK Teman exist.
3. **The 24-hour cache** — a Dukcapil answer still inside its window.
4. **Circuit breaker** — if Dukcapil is unusable, stop.
5. **Rate limiter** — the contracted RPS is a hard ceiling.
6. **Dukcapil.**

Steps 2 and 3 precede step 4 deliberately: a NIK with stored proof stays
answerable during an outage, and refusing it would discard the benefit of
storing proof exactly when it matters most.

## Fail closed

A NIK that cannot be verified blocks the operation. There is no fallback path
(ADR-005 §8, BR-13). The distinction the code draws everywhere:

- Dukcapil **answers "invalid"** → HTTP 200, `valid: false`. The dependency is
  healthy, so the breaker records a **success**. Counting rejections as
  failures would let a run of mistyped NIKs pause the whole sale.
- Dukcapil **gives no answer** → HTTP 503 `DUKCAPIL_UNAVAILABLE`, breaker
  records a failure.

## Running locally

```bash
cp example.env .env     # DUKCAPIL_MODE=stub, no network calls
make run
```

The stub answers from the NIK's last four digits so every branch is reachable
without a real endpoint: `0000` is rejected, `9999` simulates an outage,
anything else verifies. `APP_ENV=production` with `DUKCAPIL_MODE=stub` is
refused at startup.

## Configuration

| Variable | Required | Notes |
|---|---|---|
| `NIK_PEPPER` | yes, no default | KMS-managed secret. **Never rotated.** Must be the same value in every service that hashes a NIK. Back it up — losing it breaks BR-02 permanently. |
| `DATABASE_URL` | yes | Shared Aurora, read-only use here. Front it with RDS Proxy. |
| `REDIS_ADDR` | yes | Cache, limiter and breaker state. |
| `DUKCAPIL_MODE` | no | `live` (default) or `stub`. `stub` is refused in production. |
| `DUKCAPIL_BASE_URL`, `DUKCAPIL_API_KEY` | in live mode | |
| `DUKCAPIL_TIMEOUT_MS` | no | 3000. Not optional in effect: without it a hung endpoint holds the invocation until the platform kills it and the breaker never sees a failure. |
| `DUKCAPIL_RATE_LIMIT_RPS`, `DUKCAPIL_BURST_CAPACITY` | no | 40 / 80. Confirm the real contracted figure at T-7 per the ADR-005 runbook. |
| `DUKCAPIL_BREAKER_FAILURE_THRESHOLD`, `DUKCAPIL_BREAKER_OPEN_SECONDS` | no | 5 / 30. |
| `DATABASE_MAX_CONNS` | no | 4. Keep small: every warm Lambda container holds its own pool. |

## Database

Reads only, from `accounts` and `saved_niks`. It has no write path to them by
design — a write path here could create verification evidence without a
Dukcapil answer, which is the hole `CHECK (verified_at IS NOT NULL)` closes.
Migrations live in `/migrations` at the repo root.
