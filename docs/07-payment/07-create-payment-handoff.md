# POST /payments/{booking_id}/handoff — Create Payment Handoff Code

| | |
|---|---|
| **Service** | `services/payment-api` (Lambda) |
| **Auth** | `X-Grant-Token` (+ `X-Device-Id` for a guest, + `Authorization` for an account grant). `X-Payment-Session` is refused |
| **Contract** | Apidog `POST /payments/{booking_id}/handoff` · api-contracts §5.6 (v2.26) |
| **Sources** | ADR-007 · ADR-006 §8.9, §8.13, §8.17 |
| **Requirements** | FR-21 · NFR-02 · NFR-11 |
| **Status** | done |

## 1. Purpose

The mobile app opens the web payment page in a WebView. The app holds the credentials (grant, device id, account
session), the web page holds none, and none of them may travel in a URL (ADR-007). This endpoint gives the app a
**one-time code** for that one trip: 256 random bits, valid 60 seconds, used once by
[08-exchange-payment-handoff.md](08-exchange-payment-handoff.md).

It runs behind the normal grant guard, so the device binding (guest) and the account session check (logged-in) happen
here, once. The web page never repeats them.

## 2. Flow

```mermaid
sequenceDiagram
    autonumber
    actor App as Mobile app
    participant G as grant guard<br/>pkg/grantauth
    participant H as payment-api
    participant R as Redis
    participant DB as Aurora

    App->>G: POST /payments/RB-00912/handoff<br/>X-Grant-Token (+ X-Device-Id / Authorization)
    alt X-Payment-Session present
        G-->>App: 400 VALIDATION_ERROR
    else grant / device / session check fails
        G-->>App: 400 / 401 (same as §5.1–§5.4)
    end
    G->>H: grant in context
    H->>R: rate limit rl:handoff:{grant} (10 / min)
    alt over the limit
        H-->>App: 429 TOO_MANY_REQUESTS {retry_after}
    end
    H->>DB: SELECT … FROM bookings WHERE id=$1 AND grant_token=$2
    alt no row
        H-->>App: 404 NOT_FOUND
    else SELECTING / PAID / EXPIRED
        H-->>App: 409 BOOKING_STATUS_INVALID {status}
    else HELD / AWAITING_PAYMENT past payment_expires_at
        H-->>App: 409 PAYMENT_WINDOW_CLOSED {payment_expires_at}
    end
    H->>R: SET paymenthandoff:{code} = grant_token NX EX 60
    H-->>App: 201 {booking_id, handoff_code, payment_url, expires_at}
```

## 3. Steps

1. **Guard** (`grantauth.Middleware`, unchanged apart from one rule): a request carrying `X-Payment-Session` is refused
   with `400 VALIDATION_ERROR` before the grant is read. The session cannot mint new codes; only the app can.
2. **Rate limit**: `pkg/ratelimit` bucket `rl:handoff:<grant_token>`, capacity 10, refill 10 per minute. A Redis error
   on the limiter **fails open** (same as join): the limiter protects Redis from a client bug, it is not a security
   control (the code itself is the control).
3. **Booking** (`IBookingRepository.GetOwned`, the same read as §5.4): no row → `404 NOT_FOUND`.
4. **Status**, in this order:

   | `bookings.status` | Answer |
   |---|---|
   | `GRANTED`, `SELECTING` | `409 BOOKING_STATUS_INVALID`, `data.status = "SELECTING"` |
   | `CONFIRMED` | `409 BOOKING_STATUS_INVALID`, `data.status = "PAID"` |
   | `EXPIRED`, `LAPSED`, `ABANDONED` | `409 BOOKING_STATUS_INVALID`, `data.status = "EXPIRED"` |
   | `HELD`, `AWAITING_PAYMENT` with `payment_expires_at <= now` | `409 PAYMENT_WINDOW_CLOSED`, `data.payment_expires_at` |
   | `HELD`, `AWAITING_PAYMENT` (in time), `PENDING_CANCELLATION` | continue |

   `data.status` uses the four values of §4.4, so the app routes the same way it does on `GET /bookings/{id}`.
5. **Code**: `hc_` + 32 bytes from `crypto/rand`, base64url without padding (46 characters). `SET
   paymenthandoff:<code> <grant_token> NX EX 60`. `NX` failing (a collision on 256 random bits) is treated as a Redis
   error: 503.
6. **Answer** `201 {booking_id, handoff_code, payment_url, expires_at}`, `expires_at = now + 60 s`.
7. **`payment_url`** = `APP_BASE_URL` + `PAYMENT_WEB_PATH` with `{booking_id}` replaced by `url.PathEscape(booking_id)`,
   + `#code=` + `handoff_code`. With the defaults: `{APP_BASE_URL}/m/orders/{booking_id}/pay#code={handoff_code}`.
   The app opens it as is after checking the prefix `https://rush-my-ticket.my.id/m/orders/`, so a change of web
   route needs no app release (ADR-007 rule 3).

The value is the **grant token only**. The booking id is in the grant hash, so storing it twice would only create a
second copy to disagree with.

## 4. Data touched

| Store | Key / table | Op | Notes |
|---|---|---|---|
| Redis | `grant:<grant_token>` | HGETALL | Guard only |
| Redis | `rl:handoff:<grant_token>` | EVALSHA (`pkg/ratelimit`) | Token bucket, 10 / min |
| Aurora | `bookings` | SELECT | Bound on `id` **and** `grant_token` |
| Redis | `paymenthandoff:<code>` | SET NX EX 60 | Plain string: the grant token. Read once by 08 (`GET` + `DEL` in one `MULTI`) |

## 5. Configuration

| Variable | Default | Rule |
|---|---|---|
| `APP_BASE_URL` | `http://localhost:3000` | The public web frontend origin, the **same variable** notification uses for emailed links (GitHub repo variable `APP_BASE_URL`, template parameter `AppBaseUrl`, no template default). Required; `https://` outside local; one trailing `/` is dropped |
| `PAYMENT_WEB_PATH` | `/m/orders/{booking_id}/pay` | The web payment route. Starts with `/`, contains `{booking_id}` exactly once, no `#` or `?`. Template parameter `PaymentWebPath` (same default), GitHub repo variable `PAYMENT_WEB_PATH` optional |

The contract and ADR-007 call this setting `PAYMENT_WEB_URL`; it is `APP_BASE_URL` because the payment page lives on
the same web frontend as the email links. For the app's prefix check to pass, the deployed value must be
`https://rush-my-ticket.my.id`.

The bootstrap script `infra/bootstrap/configure-github.sh` still sets `APP_BASE_URL="https://ticket.sjt.com"`; it is
not changed here. Check the repo variable on GitHub before a deploy.

## 6. Responses and errors

| HTTP | Code | When |
|---|---|---|
| 201 | — | Code created |
| 400 | `VALIDATION_ERROR` | `X-Payment-Session` sent here |
| 400 | `DEVICE_ID_REQUIRED` | Guest without `X-Device-Id` (guard) |
| 401 | `GRANT_TOKEN_MISSING` / `GRANT_TOKEN_INVALID` / `DEVICE_MISMATCH` | Guard |
| 401 | `UNAUTHORIZED` / `TOKEN_EXPIRED` / `TOKEN_INVALID` | Account grant session check (guard) |
| 404 | `NOT_FOUND` | Not this grant's booking |
| 409 | `BOOKING_STATUS_INVALID` | §3 step 4, with `data.status` |
| 409 | `PAYMENT_WINDOW_CLOSED` | §3 step 4, with `data.payment_expires_at` |
| 429 | `TOO_MANY_REQUESTS` | Over 10 / min, with `data.retry_after` and `Retry-After` |
| 503 | `INTERNAL_ERROR` | Redis (code write) or Aurora unreachable |

Errors are written with `apinizer.WriteError`, **not** payment-api's own `WriteError`: that one passes `nil` as `data`,
which would drop `data.status`, `data.payment_expires_at` and `data.retry_after`.

## 7. Edge cases and decisions

- **A new code does not cancel an older one.** Each is single use and dies in 60 s; tracking "the latest code per
  grant" would need a second key for no gain.
- **The code is stored as the key in clear**, like `grant:<token>`. Hashing it would protect only against someone
  reading Redis, who can read the grant itself anyway.
- **`PENDING_CANCELLATION` is accepted** although its deadline has passed: a payment may still land in the 30 s grace,
  and the page shows "memverifikasi pembayaran…" from §5.4 (contract §4.4). Refusing it would show an expired screen to
  someone who may have paid.
- **The status check runs before the deadline check.** An `EXPIRED` booking whose grant still exists answers
  `BOOKING_STATUS_INVALID {status: EXPIRED}` (the app already routes on it); `PAYMENT_WINDOW_CLOSED` covers only a
  `HELD` / `AWAITING_PAYMENT` booking past its deadline before the sweep runs.

## 8. Tests

`services/payment-api`: `application/usecase/handoff_test.go`, `infrastructure/http/handoff_test.go`,
`infrastructure/redis/handoff_test.go` (miniredis and, with `REDIS_ADDR`, the real Redis), `delivery/handoff_e2e_test.go`
(real Postgres via `pkg/pgtest`).

- [x] unit (usecase): each row of the §3 status table gives its answer, `PENDING_CANCELLATION` included
- [x] unit (usecase): `HELD` at, one second after and one second before `payment_expires_at`
- [x] unit (usecase): the stored value is the grant token, the TTL is 60 s, the code has the `hc_` prefix and 256 bits
- [x] unit (usecase): two calls give two different codes, both stored
- [x] unit (usecase): over the limit → 429 with `retry_after` rounded up; a broken limiter lets the request through;
      Redis failing on the write → 503
- [x] unit (handler): 201 body shape; 409 and 429 keep their `data` and `Retry-After` (regression for the `nil` data
      writer); without the guard → 500, not 401
- [x] integration (Redis): the 11th call in a minute is refused; a limiter error is reported to the use case
- [x] e2e (Postgres): a guest code needs the grant's device; a session cannot mint another code (400)
- [x] unit (usecase): `payment_url` is `{base}/m/orders/{booking_id}/pay#code={handoff_code}`, with the same code as
      `handoff_code`; the booking id is path-escaped
- [x] unit (config): `APP_BASE_URL` empty → refused; `http://` outside local → refused; trailing `/` dropped;
      `PAYMENT_WEB_PATH` without `/`, without `{booking_id}`, with it twice, or with `#` / `?` → refused
- [x] unit (handler) + e2e: the 201 body carries `payment_url`

## 9. Tasks

- [x] `pkg/rediskeys`: `PaymentHandoff(code)`, `HandoffRateLimit(grantToken)` + rows in `docs/redis-keys.md`
- [x] port `IHandoffStore` / `IHandoffLimiter` (outbound) + Redis adapter `infrastructure/redis/handoff.go`
- [x] usecase `CreateHandoff` (`application/usecase/handoff.go`, its own `IHandoffUsecase` port) + handler + route
- [x] templates: route in `services/payment-api/template.yaml` **and** `template-cfn.yaml`, and in
      `infra/api-gateway/template.yaml`
- [x] config `App.AppBase` (`APP_BASE_URL`), `App.PaymentWebPath` (`PAYMENT_WEB_PATH`) + validation; `example.env`
- [x] `dto.HandoffResponse.PaymentURL`; `NewHandoff` takes base + path; built in `CreateHandoff`
- [x] templates: `AppBaseUrl` (no default) and `PaymentWebPath` (default) parameters + env in `template.yaml` **and**
      `template-cfn.yaml`
- [x] `cicd.yml`: pass `AppBaseUrl=${{ vars.APP_BASE_URL }}` and `PaymentWebPath=${{ vars.PAYMENT_WEB_PATH || '/m/orders/{booking_id}/pay' }}` to payment-api; preflight fails when `APP_BASE_URL` is
      empty and payment-api is deployed (as it does for notification)
