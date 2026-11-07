# POST /events/{event_id}/queue/join — Join Queue

| | |
|---|---|
| **Service** | `services/queue` (Lambda + API Gateway, provisioned concurrency from T-30 min) |
| **Auth** | none required · optional `Authorization: Bearer` (enables BR-07) · `X-Device-Id` required for guests |
| **Contract** | Apidog `POST /events/{event_id}/queue/join` · api-contracts §3.1 |
| **Requirements** | FR-04, FR-05, FR-10a, FR-10d..f, BR-07, BR-09, BR-16, NFR-05, NFR-13 |
| **Detail** | impl-plan §7.2 (stale order, see §3) · ADR-005 §5, §6 · **ADR-006 §4** (S2) |
| **Specs** | `services/queue/features/join_*.feature`, `captcha_turnstile.feature` (90 scenarios) |
| **Status** | **implemented** (build step 4, branch `feature/step-4-join-queue`). FR-05 e2e with the real admission-ctl waits for step 3 |

## 1. Purpose

Gives the caller a `ticket_no` (their FCFS place) and a `queue_token`, **in the body and in an HttpOnly
cookie**. **Join never grants**: admission-ctl is the only writer (S2). If the queue is empty, join waits up
to `JOIN_GRANT_WAIT_MS` (1000) for admission-ctl's next tick (every 500 ms) to grant it, so FR-05 still
answers `granted: true` in the same response. The hot path is **Redis only**. Aurora is read only: during
the empty-queue wait, and once on a rejoin after the customer's turn came.

Called when the customer presses the event button: "Masuk Antrian" (`queue_state: OPEN`) **and** "Beli
Tiket" (`DIRECT`, empty queue). Both need a grant, and only join leads to one. Coming back (refresh, bank
app) is `GET /queue/me`, not join.

## 2. Flow

```mermaid
sequenceDiagram
    autonumber
    actor C as Client
    participant Q as queue-service
    participant R as Redis
    participant CAP as Turnstile
    participant DB as Aurora (writer)

    C->>Q: POST /join {captcha_token} · X-Device-Id · Bearer? · cookie / X-Queue-Token?
    Note over Q: 1 shape: JSON ≤ 8 KB, event id usable as {ev} → else 400 VALIDATION_ERROR / 404 NOT_FOUND
    Note over Q: 2 identity: bad Bearer → 401 TOKEN_EXPIRED / TOKEN_INVALID<br/>guest without device id → 400 DEVICE_ID_REQUIRED

    Q->>R: 3 rate limit rl:join:{ip}, then rl:joinacct:{id}
    alt over limit
        Q-->>C: 429 TOO_MANY_REQUESTS {retry_after} + Retry-After
    else limiter down
        Note over Q: fail open + RateLimitFailOpen
    end

    Note over Q: 4a no captcha_token → 400 CAPTCHA_REQUIRED
    Q->>CAP: 4b siteverify (1 s)
    alt explicit invalid
        Q-->>C: 403 CAPTCHA_FAILED (no ticket_no consumed)
    else timeout / 5xx / bad body / our secret wrong
        Note over Q: fail open + CaptchaFailOpenSeconds
    end

    Q->>R: 5 snapshot: state, now_serving, issued, name, sale_starts_at, sold_out_at (1 pipeline)
    alt SCHEDULED
        Q-->>C: 409 SALE_NOT_OPEN {queue_state NOT_OPEN, sale_starts_at}
    else CLOSED
        Q-->>C: 410 SALE_CLOSED {queue_state CLOSED}
    else missing / unknown state
        Q-->>C: 503 QUEUE_NOT_READY
    end

    Q->>R: 6 rejoin: HGETALL token:{presented qt} (BR-09), else GET acct:{ev}:{id} (BR-07)
    alt token of this event, not the caller's
        Q-->>C: 401 DEVICE_MISMATCH (guest, other device) / UNAUTHORIZED (other account), clear cookie
    else existing place
        Q-->>C: 200 rejoined true, state, same queue_token + cookie
    end

    alt 7 SOLD_OUT
        Q-->>C: 409 SOLD_OUT {queue_state SOLD_OUT, sold_out_at}
    else 8 issued − now_serving ≥ QUEUE_CAPACITY
        Q-->>C: 503 QUEUE_FULL {queue_state FULL, queue_capacity}
    end

    Q->>R: 9 SET acct NX (logged in) · INCR issued · HSET token · ZADD by_ticket

    opt 10 queue empty: ticket_no == now_serving + 1 (FR-05)
        loop every 100 ms, up to JOIN_GRANT_WAIT_MS (1000)
            Q->>DB: SELECT status, grant_token, booking_expires_at WHERE event_id, queue_token
        end
        alt row found
            Q-->>C: 200 granted true, grant_token, queue_token, booking_expires_at
        else no row in time / Aurora error / client hung up
            Note over Q: fall through to the queued answer.<br/>The turn arrives via the broadcast + /me
        end
    end

    Q-->>C: 200 ticket_no, now_serving, position, queue_token, sse_url, event + Set-Cookie
```

## 3. Steps

Approved order (2026-11-06; `services/queue/features/join_gates.feature`). Every rejection happens before
`INCR`, so a rejected call never burns a ticket number. Rate limit comes before CAPTCHA, so rejected
attempts never cost an external call (ADR-005 §6). impl-plan §7.2 still shows capacity before BR-07; this
order wins.

1. **Request shape**: JSON body ≤ 8 KB with a string `captcha_token` (else 400 `VALIDATION_ERROR`); event id
   without `{`, `}` or whitespace (else 404 `NOT_FOUND`)
2. **Identity**: a Bearer must verify (expired → 401 `TOKEN_EXPIRED`, anything else → 401 `TOKEN_INVALID`,
   never a silent guest). Device id: required for guests, optional when logged in, any non-empty value
   (no format check since contract v2.9)
3. **Rate limit**: IP (20 / 5 min), then account (3 / min). Limiter outage fails open (`RateLimitFailOpen`)
4. **CAPTCHA**: missing → 400 `CAPTCHA_REQUIRED`; explicit invalid → 403 `CAPTCHA_FAILED`; provider trouble
   fails open (`CaptchaFailOpenSeconds`)
5. **Sale state**: `SCHEDULED` → 409 `SALE_NOT_OPEN`, `CLOSED` → 410 `SALE_CLOSED`, missing or unknown → 503
   `QUEUE_NOT_READY`
6. **Rejoin, before sold-out and capacity**, so a customer who already has a place is never refused:
   - BR-09: the presented `queue_token` (`X-Queue-Token`, else cookie) of this event, **owned by the caller**:
     a guest token needs a guest on the same device (`device_hash`), a logged-in token the same account. Not the
     caller's → 401 `DEVICE_MISMATCH` / `UNAUTHORIZED` and the cookie is cleared. A logged-in caller never takes
     over a guest place: it gets its own (BR-07 cannot be bypassed)
   - BR-07: `acct:{ev}:{account_id}` → token. A pointer whose token does not appear within 300 ms is stale and
     is replaced on issue (compare-and-set), not deleted
   - Still waiting (`ticket_no > now_serving`): a place missing from `by_ticket` (a join that died between
     `HSET` and `ZADD`) is put back with `ZADD NX` under `WATCH queue:{ev}`, only while still ahead of
     `now_serving` (`JoinRepaired`). A served ticket is never re-added
   - Turn has come: `state` is the booking row's status; no row yet = pending grant (`QUEUED`). A failed
     lookup is 503, never a guess
7. **Sold out**: new joiners get 409 `SOLD_OUT`; rejoiners kept their place in step 6
8. **Capacity**: `issued − now_serving ≥ QUEUE_CAPACITY` → 503 `QUEUE_FULL` (fail fast, no buffering)
9. **Issue**: `SET acct NX` (logged in; a lost race waits up to 2 s for the winner's token and returns that
   place) → `INCR issued` → `HSET token` (+ TTL) → `ZADD by_ticket`. Token before `by_ticket`, so
   admission-ctl never sees a member without its hash. A failure releases `acct` and answers 503
10. **FR-05**: if `ticket_no == now_serving + 1`, poll the writer for the GRANTED row, ≤ 1 s, read only
11. **Respond**: `queue_token` in the body and `Set-Cookie: queue_token=…; Domain=my-rusuh.my.id; Path=/events/{event_id};
    Max-Age={SALE_WINDOW_SECONDS}; HttpOnly; Secure; SameSite=Strict`. **Mobile** keeps the body token in
    secure storage and sends `X-Queue-Token`. **Web** ignores it and relies on the cookie (the stream's
    `EventSource` cannot send headers; never put it in `localStorage`)

## 4. Data touched

Full catalog: [`../redis-keys.md`](../redis-keys.md).

| Store | Key / table | Op | Notes |
|---|---|---|---|
| Redis | `rl:join:{ip}`, `rl:joinacct:{id}` | token bucket (`pkg/ratelimit` Lua) | 20 / 5 min, 3 / min |
| Redis | `queue:{ev}:state`, `queue:{ev}` (`now_serving`, `name`, `sale_starts_at`), `queue:{ev}:issued`, `queue:{ev}:sold_out_at` | one pipelined read | `name` and `sale_starts_at` are seeded by sale-ops |
| Redis | `token:{queue_token}` | HGETALL (rejoin), HSET + EXPIRE | `qt_` + ULID. Fields `ticket_no`, `event_id`, `joined_at`, `account_id` (logged in), `device_hash` (`HMAC(DEVICE_PEPPER, device id)`, whenever a device id is sent). TTL `SALE_WINDOW_SECONDS` |
| Redis | `acct:{ev}:{account_id}` | GET, SET NX EX, WATCH/MULTI replace | logged-in only |
| Redis | `queue:{ev}:by_ticket` | ZADD on issue; ZADD NX under WATCH on repair | score = `ticket_no` |
| Aurora | `bookings` (migration `0005`: `grant_token`, `UNIQUE (event_id, queue_token)`) | SELECT on the writer | empty-queue wait (≤ 11 reads) and one read on a rejoin after the turn |

Join never runs the admission Lua, never publishes and never writes to Aurora.

## 5. Responses and errors

| HTTP | Code | When |
|---|---|---|
| 200 | — | **queued**: `ticket_no`, `now_serving`, `position`, `queue_token`, `sse_url`, `event {id, name}` · **rejoined** (BR-09 or BR-07): same place fields + `rejoined: true`, `state` (§3.3 values; not `QUEUED` → route by state) · **granted** (FR-05): `granted`, `grant_token`, `queue_token`, `booking_expires_at`. No wait estimate ([README §8](README.md#8-wait-time-estimate-fr-09)) |
| 400 | `VALIDATION_ERROR` | bad body or device id format |
| 400 | `DEVICE_ID_REQUIRED` · `CAPTCHA_REQUIRED` | guest without `X-Device-Id` · no `captcha_token` |
| 401 | `TOKEN_EXPIRED` · `TOKEN_INVALID` | bad Bearer (also `Bearer ` with nothing after it) |
| 401 | `DEVICE_MISMATCH` | guest `queue_token` presented from another device (or with no `device_hash`); clears the cookie |
| 401 | `UNAUTHORIZED` | logged-in `queue_token` presented by another account or by a guest; clears the cookie |
| 403 | `CAPTCHA_FAILED` | explicit CAPTCHA rejection |
| 404 | `NOT_FOUND` | event id that cannot be a Redis hash tag |
| 409 | `SALE_NOT_OPEN` | state `SCHEDULED`; data `{queue_state: NOT_OPEN, sale_starts_at}` |
| 409 | `SOLD_OUT` | state `SOLD_OUT`, new joiners only; data `{queue_state: SOLD_OUT, sold_out_at}` (new code, 2026-11-06) |
| 410 | `SALE_CLOSED` | state `CLOSED`; data `{queue_state: CLOSED}` |
| 429 | `TOO_MANY_REQUESTS` | rate limit; data `{retry_after}` (seconds) and a `Retry-After` header |
| 503 | `QUEUE_FULL` | data `{queue_state: FULL, queue_capacity}` |
| 503 | `QUEUE_NOT_READY` | event never seeded or unknown state (new code) |
| 503 | `INTERNAL_ERROR` | Redis down or a failed write; fail closed, retry later (new code: any 5xx without its own code) |

`sse_url` is `SSE_BASE_URL` + `/events/{event_id}/queue/stream` (absolute in AWS: the stream is on
`events.my-rusuh.my.id`, join on `api.my-rusuh.my.id`).

## 6. Edge cases and decisions

- **DECIDED (S2): join never grants.** ADR-005's bypass ran `advance_now_serving.lua` and the INSERT inside
  the Lambda, a second writer. Now the Lambda only reads. If it dies while waiting, nothing is lost: the ctl
  grants anyway and the client finds out from the broadcast + `/me`.
- **"Queue empty" check:** `ticket_no == now_serving + 1` right after the `INCR`, using the snapshot's
  `now_serving`. Two simultaneous first joiners: only the first waits; the second learns its turn from the
  broadcast + `/me`.
- **Wait budget:** ≤ 1 s, only on an empty queue. Typical 250–500 ms. Lambda timeout 10 s (≥ 3 × the wait).
- **DECIDED (2026-11-07): a presented token rejoins only for its owner.** Guest token: `X-Device-Id` must hash to
  `device_hash` (no rebind; a guest who clears site data starts again). Logged-in token: the Bearer account must be its
  `account_id`, from any device. Otherwise 401 and the cookie is cleared, so the next join is fresh. A logged-in caller
  with a guest token gets its own place. Unknown or other-event tokens are ignored (`join_token_binding.feature`).
- **DECIDED (2026-11-06): token in body and cookie**, cookie `Domain=my-rusuh.my.id` (`COOKIE_DOMAIN`).
- **DECIDED (2026-11-06): `SOLD_OUT` refuses new joiners** (409), like a waiting room's "reject" mode; people
  already in line keep their place and wait for returned seats. Joining works again when the state goes back
  to `OPEN`.
- **Capacity overshoot accepted:** the gate reads then INCRs (not atomic), so up to (parallel joins − 1) extra
  numbers can pass `QUEUE_CAPACITY` under a race. The cap is a safety net; the home page shows `FULL` first.
- **Crash between `INCR` and `HSET`:** a harmless gap (admission-ctl only grants `by_ticket` members).
  **Between `HSET` and `ZADD`:** repaired by the next rejoin (step 6).
- A **guest** whose join response failed will retry and get a new token. The orphaned first token can still
  be granted and then LAPSES unclaimed, holding one slot for the booking TTL. Accepted.
- **DECIDED (2026-11-05): Cloudflare Turnstile**, invisible, behind the `Captcha` port. `siteverify` with a
  1 s deadline. Timeout, 5xx, 429, a non-JSON body, `internal-error`, and `missing-/invalid-input-secret`
  (our misconfiguration, plus `CaptchaMisconfigured`) all fail open. Only an explicit rejection of the token
  is 403. Tests and k6 use the mock adapter (`CAPTCHA_PROVIDER=mock`, refused in production).
- **Still open (ADR §17 #8, T-7 days):** confirm Turnstile can absorb about 300k `siteverify` calls in about
  60 s for our plan. The client must refresh a token close to expiring.
- **DEFERRED: qpersist** (ADR-006 §9); the POC relies on Redis Multi-AZ.

## 7. Tests

All in `services/queue/features/` (godog, strict) plus Go unit tests under `internal/`.

- [x] unit: gate order (shape → identity → rate limit → CAPTCHA → state → rejoin → sold out → capacity), each branch returns the right code and later gates are not called
- [x] unit: guest without device id → 400. Logged-in without device id → OK
- [x] integration: rejoin by account (another device) and by token (cookie / header) → same place, `rejoined: true`
- [x] integration: token ownership: guest token from another device → 401 `DEVICE_MISMATCH`; logged-in token of another account or a guest → 401 `UNAUTHORIZED`; cookie cleared (`join_token_binding.feature`)
- [x] integration: capacity reached → `QUEUE_FULL`, `issued` not incremented; a rejoiner is not refused
- [x] integration: `SOLD_OUT` → 409 for new joiners, rejoiners keep their place
- [ ] integration: empty queue + ctl running → `granted: true` within 1 s (stand-in controller passes; real ctl `@e2e` after step 3)
- [x] integration: empty queue + ctl stopped → queued answer after ≤ 1 s, ≤ 11 reads, SELECT only
- [x] integration: non-empty queue → no wait, no SQL, no PUBLISH, no Lua but the rate limiter
- [x] concurrency: 10k parallel joins → `ticket_no` 1..10k each once, `by_ticket` size = 10k; 10 parallel joins of one account → one place
- [x] contract: the 8 documented responses validate against the Apidog schemas

## 8. Tasks

- [x] `services/queue` scaffold (hexagonal, `cmd/lambda` + `cmd/server`)
- [x] outbound ports: `QueueStore`, `BookingReader`, `Captcha`, `RateLimiter`, `TokenVerifier`, `Metrics`
- [x] redis adapter: snapshot, engagement, acct claim / release, line repair
- [x] Turnstile adapter (`siteverify`, timeout, fail-open) + mock adapter
- [x] config: secrets (`TURNSTILE_SECRET_KEY`, `DEVICE_PEPPER`, `JWT_SECRET`, `DATABASE_URL`) from Secrets Manager, never in the repo
- [x] usecase `Join` + HTTP handler + cookie
- [x] FR-05 bounded wait for the grant row
- [x] `template.yaml` route + provisioned concurrency parameter
- [x] contract updates in `documentations` (api-contracts v2.8.0 + Apidog: codes, cookie, `sse_url`, all join responses). Edited on branch `docs/queue-adr-006`, **not committed yet**
- [ ] CI: Redis + Postgres services so `@integration` runs in CI (today CI runs unit level only)
