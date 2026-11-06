# POST /events/{event_id}/queue/join — Join Queue

| | |
|---|---|
| **Service** | `services/queue` (Lambda + API Gateway, provisioned concurrency from T-30 min) |
| **Auth** | none required · optional `Authorization: Bearer` (enables BR-07) · `X-Device-Id` required for guests |
| **Contract** | Apidog `POST /events/{event_id}/queue/join` · api-contracts §3.1 |
| **Requirements** | FR-04, FR-05, FR-10a, FR-10d..f, BR-07, BR-16, NFR-05, NFR-13 |
| **Detail** | impl-plan §7.2 · ADR-005 §5, §6 · **ADR-006 §4** (S2) |
| **Status** | implemented (build step 4, branch `feature/step-4-join-queue`); FR-05 e2e waits for admission-ctl (step 3) |

## 1. Purpose

Gives the caller a `ticket_no` (their FCFS place) and a `queue_token` cookie. **Join never
grants**: admission-ctl is the only writer (S2). If the queue is empty, join waits up to
`JOIN_GRANT_WAIT_MS` (1000) for admission-ctl's next regular tick (every 500 ms) to grant it, so
FR-05 still answers `granted: true` in the same response. The hot path is **Redis only**. Aurora is read only, and
only on the empty-queue path.

## 2. Flow

```mermaid
sequenceDiagram
    autonumber
    actor C as Client
    participant Q as queue-service
    participant R as Redis
    participant CAP as CAPTCHA
    participant DB as Aurora

    C->>Q: POST /join (captcha_token, X-Device-Id, Bearer?)
    Q->>Q: guest without X-Device-Id?
    opt missing device id
        Q-->>C: 400 DEVICE_ID_REQUIRED
    end

    Q->>R: rate limit rl:join:{ip} (+ per account if logged in)
    alt over limit
        Q-->>C: 429 TOO_MANY_REQUESTS
    end

    Q->>CAP: verify(captcha_token)
    alt invalid
        Q-->>C: 403 CAPTCHA_FAILED (no ticket_no consumed)
    else provider down
        Note over Q: fail-open + CaptchaFailOpenSeconds
    end

    Q->>R: GET queue:{ev}:state
    alt SCHEDULED
        Q-->>C: 409 SALE_NOT_OPEN (sale_starts_at)
    else CLOSED
        Q-->>C: 410 CLOSED
    else key missing
        Q-->>C: 503 (event not seeded)
    end

    Q->>R: issued - now_serving >= capacity?
    alt full
        Q-->>C: 503 QUEUE_FULL
    end

    opt logged in (BR-07)
        Q->>R: SET acct:{ev}:{account} uuid NX
        alt already engaged
            Q->>R: HGETALL token:{existing}
            Q-->>C: 200 rejoined true, existing state + re-issued cookie
        end
    end

    Q->>R: INCR queue:{ev}:issued → ticket_no
    Q->>R: HSET token:{uuid} · ZADD by_ticket

    opt queue empty: ticket_no == now_serving + 1 (FR-05)
        Note over Q,DB: admission-ctl's next tick (≤ 500 ms) grants it
        loop every 100 ms, up to JOIN_GRANT_WAIT_MS (1000)
            Q->>DB: SELECT grant_token, booking_expires_at WHERE event_id, queue_token (writer)
        end
        alt row found
            Q-->>C: 200 granted true, grant_token, booking_expires_at
        else not yet (slots full, ctl restarting)
            Note over Q: fall through to the normal queued response.<br/>The client gets its turn via the broadcast + /me
        end
    end

    Q-->>C: 200 ticket_no, now_serving, position, sse_url + Set-Cookie queue_token
```

## 3. Steps

Order matters: **rate limit before CAPTCHA**, so we don't pay for an external call on rejected
attempts (ADR-005 §6). The full pseudo-code is in impl-plan §7.2, steps 1–12.

Approved order (2026-11-06; specs in `services/queue/features/join_gates.feature`):

1. Request shape (JSON, ≤ 8 KB, event id usable as a `{ev}` tag), then identity: optional
   `accountID` from Bearer (bad token → 401, never a silent guest), `deviceID` (guests only, 16–128 `[A-Za-z0-9_-]`)
2. Rate limit (IP, then account). Limiter outage fails open (`RateLimitFailOpen`)
3. CAPTCHA required, then verify, fail-open on provider trouble
4. Sale state gate: `SCHEDULED` → 409, `CLOSED` → 410 `SALE_CLOSED`, missing/unknown → 503 `QUEUE_NOT_READY`
5. Rejoin **before** capacity: BR-09 (cookie / `X-Queue-Token`), then BR-07 (`acct:{ev}:{id}`).
   A QUEUED place missing from `by_ticket` is repaired (`ZADD NX`, `JoinRepaired`)
6. Sold out: new joiners get 409 `SOLD_OUT`; rejoiners keep their place
7. Capacity gate (fail fast, no buffering)
8. `SET acct NX` (logged in), `INCR issued`, `HSET token`, `ZADD by_ticket`
9. FR-05: if the queue is empty, wait ≤ 1 s for the grant row (read only)
10. Set cookie (`Max-Age` = `SALE_WINDOW_SECONDS`) and respond with `queue_token` in the body too.
   **Mobile** has no cookie store: it keeps `queue_token` in secure storage and sends `X-Queue-Token`.
   **Web** ignores the body field and relies on the HttpOnly cookie (never put it in `localStorage`)

## 4. Data touched

| Store | Key / table | Op | Notes |
|---|---|---|---|
| Redis | `rl:join:{ip}`, `rl:joinacct:{id}` | token bucket | 20 / 5 min, 3 / min |
| Redis | `queue:{ev}:state`, `queue:{ev}:issued`, `queue:{ev}` | GET / INCR / HGET | |
| Redis | `acct:{ev}:{account_id}` | SET NX EX | logged-in only |
| Redis | `token:{uuid}` | HSET EX | `device_hash` for guests only |
| Redis | `queue:{ev}:by_ticket` | ZADD | score = ticket_no |
| Aurora | `bookings` | SELECT (writer) | empty-queue wait only, ≤ 10 reads |

## 5. Responses and errors

| HTTP | Code | When |
|---|---|---|
| 200 | — | queued, or `granted: true` (empty queue, ctl granted within 1 s), or `rejoined: true` (BR-07). Queued responses carry `ticket_no`, `now_serving`, `position`, **`queue_token`** and `sse_url`; no wait estimate (frontend computes it, [README §8](README.md#8-wait-time-estimate-fr-09)) |
| 400 | `DEVICE_ID_REQUIRED`, `CAPTCHA_REQUIRED` | |
| 403 | `CAPTCHA_FAILED` | |
| 409 | `SALE_NOT_OPEN` | state SCHEDULED |
| 409 | `SOLD_OUT` | state SOLD_OUT, new joiners only (new code, 2026-11-06) |
| 410 | `SALE_CLOSED` | state CLOSED |
| 401 | `TOKEN_EXPIRED`, `TOKEN_INVALID` | bad Bearer |
| 429 | `TOO_MANY_REQUESTS` | rate limit |
| 503 | `QUEUE_FULL` · `QUEUE_NOT_READY` (not seeded, new code) · `INTERNAL_ERROR` (Redis down) | |

## 6. Edge cases and decisions

- **DECIDED (S2): join never grants.** ADR-005's bypass ran `advance_now_serving.lua` and the
  INSERT inside the Lambda, which made a second writer. With admission counts read from Aurora (S1), two writers
  could over-admit, and the Lambda needed its own crash path. Now the Lambda only reads. If it dies while waiting, nothing is lost: the ctl grants anyway and the client finds out
  from the broadcast + `/me`.
- **"Queue empty" check:** `ticket_no == now_serving + 1` right after the `INCR`. A race with
  another join is harmless: both wait, and the ctl grants whoever fits.
- **Wait budget:** 1 s at most (covers at least one full 500 ms tick plus the INSERT), and only on an
  empty queue (low traffic by definition). Typical wait about 250–500 ms. The Lambda timeout must be well above 1 s.
- **DEFERRED: qpersist.** ADR-005 also `RPUSH`ed each join to `qpersist:{ev}` for durable positions
  in `queue_entries`. Deferred to production (ADR-006 §9); the POC relies on Redis Multi-AZ.
- A **guest** whose join response failed will retry and get a new token. The orphaned first
  token can still be granted and then LAPSES unclaimed, holding one slot for the booking TTL. Accepted.
- **DECIDED (2026-11-05): Cloudflare Turnstile**, invisible mode, behind the `Captcha` port.
  The client gets the token before the sale opens (README §2). The server calls Turnstile
  `siteverify` with a short timeout. A timeout or 5xx means fail-open plus the `CaptchaFailOpenSeconds`
  alarm. An explicit "invalid" means `403 CAPTCHA_FAILED`. Tests and k6 use the mock adapter, or
  Turnstile's published test keys, so load tests never call the real service.
- **Still open (ADR §17 #8, T-7 days):** confirm Turnstile can absorb about 300k `siteverify`
  calls in about 60 s for our account and plan. Tokens are single-use and short-lived, so a token
  fetched too early before the sale can expire. The client must refresh it if it's close to expiring.
- `acct` exists but `token:{uuid}` is gone: `DEL acct` and continue as a new join (impl-plan §7.2 step 6).
- `INCR issued` happens after every gate, so rejected calls never burn a ticket number.

## 7. Tests

- [x] unit: gate order (rate limit → CAPTCHA → state → capacity → BR-07), each branch returns the right code
- [x] unit: guest without device id → 400. Logged-in without device id → OK
- [x] integration: 2 joins with the same account → one token, second returns `rejoined: true`
- [x] integration: capacity reached → `QUEUE_FULL`, `issued` not incremented
- [ ] integration: empty queue + ctl running (stand-in controller passes; real ctl e2e after step 3) → `granted: true` within 1 s, row written by the ctl (join wrote nothing to Aurora)
- [x] integration: empty queue + ctl stopped → normal queued response after ≤ 1 s, no row
- [x] integration: non-empty queue → no wait, no Aurora call
- [x] concurrency: 10k parallel joins → `ticket_no` unique and contiguous, `by_ticket` size = 10k

## 8. Tasks

- [x] `services/queue` scaffold (copy booking hexagonal layout, `cmd/lambda` + `cmd/server`)
- [x] outbound ports: `QueueStore`, `GrantStore`, `BookingReader`, `Captcha`
- [x] redis adapter: engagement, queue_state, acct
- [x] Turnstile adapter (`siteverify`, timeout, fail-open on error) + mock adapter
- [x] config: `TURNSTILE_SECRET_KEY` from Secrets Manager / SSM, never in the repo
- [x] usecase `Join` + HTTP handler + cookie
- [x] FR-05 bounded wait for the grant row (tested with a stand-in controller; e2e after 05 admission-ctl lands)
- [x] `template.yaml` route + provisioned concurrency
