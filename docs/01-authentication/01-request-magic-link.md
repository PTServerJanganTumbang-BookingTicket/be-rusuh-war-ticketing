# POST /auth/magic-link — Request Magic Link

| | |
|---|---|
| **Service** | `services/user` (Lambda) |
| **Auth** | None |
| **Contract** | Apidog `POST /auth/magic-link` · api-contracts §1.1 |
| **Requirements** | FR-31 |
| **Status** | done |

## 1. Purpose

Issues a one-time login link to the supplied email address, **only if an account
exists for it**. An unknown address is answered `404 EMAIL_NOT_REGISTERED` and
nothing is written.

> **This endpoint is an email-enumeration oracle, deliberately.** Until v2.25 the
> response, timing and status were identical whether or not the account existed,
> precisely so that a caller could not discover which addresses are registered —
> a privacy leak in its own right, since an address here implies a ticket holder.
> That was reversed knowingly. Anyone can now walk a list of addresses and read
> the status code to learn which have accounts.
>
> The **rate limiter is the only thing throttling it**, which makes
> `MagicLinkLimit` and `MagicLinkWindow` security parameters rather than
> politeness ones. It runs *before* the account lookup for exactly that reason,
> and a test pins the ordering.
>
> What the change bought: previously an unregistered address received a real
> email carrying a working-looking link, which then failed at redemption with
> "no account exists for this address". That made the endpoint a way to send
> mail to people who never signed up.

A token row and its outbox event commit together in a single transaction. A token
with no queued email is a link nobody receives; an email with no token is a link
that cannot be redeemed.

The same handler is shared with `POST /auth/magic-link/resend`; the only difference
is the response message (impl: `handleMagicLink` in `services/user/internal/user/infrastructure/http/auth.go`).

## 2. Flow

```mermaid
sequenceDiagram
    autonumber
    actor C as Client
    participant H as user-service
    participant R as Redis
    participant DB as Aurora
    participant OBP as outbox-publisher

    C->>H: POST /auth/magic-link {email}
    H->>H: validate email format
    alt invalid format
        H-->>C: 400 VALIDATION_ERROR
    end

    H->>R: rate limit rl:auth:magic-link:{email} (5/hour)
    alt over limit
        H-->>C: 429 TOO_MANY_REQUESTS (retry_after)
    end

    H->>H: generate 256-bit random plain token + SHA-256 hash

    opt account exists for this email
        H->>DB: FindAccountByEmail → account_id
    end

    H->>DB: BEGIN — INSERT magic_tokens (hash, purpose=LOGIN)<br/>INSERT outbox (auth.magic_link payload with plain token)
    H->>DB: COMMIT
    H-->>C: 200 "Login link sent to your email"

    OBP->>DB: poll outbox WHERE published_at IS NULL
    OBP->>OBP: SQS → notification-service → SES
    OBP->>DB: UPDATE outbox SET published_at = now()
```

## 3. Steps

1. Require `Content-Type: application/json` body; decode and validate `email` field.
2. Normalise email: `strings.ToLower(strings.TrimSpace(email))`.
3. Rate-limit on `auth:magic-link:{email}` — 5 attempts per hour. Fail **open**
   on Redis error (limiter unavailable ≠ login blocked).
4. Generate 32-byte random buffer → base64url plain token, SHA-256 hash. Only the
   hash persists.
5. Attempt `FindAccountByEmail`: if found, attach `account_id` to the token row;
   if not found, leave it empty. Either way the transaction continues.
6. Open one transaction:
   - `INSERT magic_tokens (id, email, token_hash, purpose=LOGIN, expires_at=now+15min)`
   - `INSERT outbox (id, aggregate_id=email, type=auth.magic_link, payload={account_id,email,token})`
7. Commit. Return 200 with a fixed message.

## 4. Data touched

| Store | Key / table | Op | Notes |
|---|---|---|---|
| Redis | `auth:magic-link:{email}` | token bucket | 5 / hour, fail-open |
| Aurora | `accounts` | SELECT | Optional — to attach account_id |
| Aurora | `magic_tokens` | INSERT | `token_hash` only; plain token is in the outbox payload |
| Aurora | `outbox` | INSERT | `auth.magic_link` event; same TX as magic_tokens |

## 5. Responses and errors

| HTTP | Code | When |
|---|---|---|
| 200 | — | Link issued; an account exists for the address |
| 404 | `EMAIL_NOT_REGISTERED` | No account for this address. Nothing written, no email sent |
| 400 | `VALIDATION_ERROR` | Missing or malformed `email` |
| 429 | `TOO_MANY_REQUESTS` | 5 requests in the last hour for this address |

## 6. Edge cases and decisions

- **Enumeration is now possible, and accepted** (v2.25). `FindAccountByEmail` is a
  gate, not a best-effort attachment. See §1 for the trade and what limits it.
- **Order matters**: the rate limiter runs before the account lookup, so a limited
  caller is refused with 429 before learning whether the address exists. Reversing
  those two lines would let a throttled attacker keep probing.
- **Checked before the token is minted**: an unknown address costs no crypto, no
  `magic_tokens` row and no outbox event. The previous behaviour spent all three
  on addresses that could never redeem.
- **Fail-open rate limiter**: a Redis outage cannot take down login. The downside is
  a brief rate-limit bypass; acceptable given the 15-minute token TTL.
- **Token hash only in DB**: a database read is not enough to authenticate as anyone
  with a live link.
- **Atomic outbox**: if the transaction fails, no orphaned email is queued and no
  token exists to redeem.

## 7. Tests

- [x] unit: valid email → token stored + event queued
- [x] unit: missing email → 400
- [x] unit: invalid email format → 400
- [x] unit: 5th request within window → 200 (allowed), 6th → 429
- [x] unit: limiter error → fail-open, request proceeds
- [x] unit: unknown email → 404 `EMAIL_NOT_REGISTERED`
- [x] unit: unknown email writes nothing — no token row, no outbox event
- [x] unit: rate limit is evaluated before the account lookup (429, not 404)
- [x] unit: the stored token carries `account_id`, now that it is always known

## 8. Tasks

- [x] HTTP handler `RequestMagicLink` + validation
- [x] usecase `RequestMagicLink`: rate limit, token generation, single-TX write
- [x] `SaveMagicTokenWithEvent` adapter (postgres)
- [x] outbox event type `auth.magic_link` in `pkg/events`
- [x] rate limiter adapter (Redis token bucket)
