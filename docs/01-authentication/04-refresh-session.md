# POST /auth/refresh — Refresh Session

| | |
|---|---|
| **Service** | `services/user` (Lambda) |
| **Auth** | Same-Origin required |
| **Contract** | Apidog `POST /auth/refresh` · api-contracts §1.4 |
| **Requirements** | FR-31, BR-09, NFR-02, NFR-07 |
| **Status** | done |

## 1. Purpose

Exchanges a valid refresh token for a fresh access/refresh pair. Tokens rotate:
the one presented is spent, and a replacement comes back in one transaction.
Presenting a token that was already spent is the signature of a stolen credential,
so the entire rotation family is revoked and the customer must log in again
(OAuth 2.0 BCP §4.13).

The access token lasts 22740 s — covering the queue wait and the full booking
journey — so this endpoint is not called mid-payment or in the waiting room. It
exists for sessions that span days.

The refresh token travels in the request body, not in `Authorization`: it is the
subject of this request, not a bearer credential, and keeping it out of
`Authorization` prevents it being logged by proxies that record that header.

## 2. Flow

```mermaid
sequenceDiagram
    autonumber
    actor C as Client
    participant H as user-service
    participant DB as Aurora

    C->>H: POST /auth/refresh {refresh_token}
    H->>H: Same-Origin check
    alt missing Origin/Referer
        H-->>C: 403 FORBIDDEN
    end

    H->>H: validate body — refresh_token required
    alt missing
        H-->>C: 400 VALIDATION_ERROR
    end

    H->>H: generate replacement token (256-bit random + SHA-256)

    H->>DB: RotateRefreshToken(SHA256(presented), replacement)
    note over DB: One transaction:<br/>1. SELECT … FOR UPDATE matching token_hash<br/>2. If used_at IS NOT NULL → revoke family, return ErrReplayed<br/>3. If not found or expired → return 404<br/>4. SET used_at=now(), replaced_by=replacement.id<br/>5. INSERT replacement (inherits account_id, family_id)

    alt ErrRefreshTokenReplayed
        H->>H: log metric RefreshTokenReplayed
        H-->>C: 401 TOKEN_INVALID "session ended; please sign in again"
    else not found / expired
        H->>H: clear access_token cookie
        H-->>C: 401 TOKEN_EXPIRED
    end

    H->>DB: FindAccountByID(rotated.AccountID)
    alt account not found
        H-->>C: 401 TOKEN_INVALID
    end

    H->>H: Issue new JWT (re-reads nik_verified from account row)
    H->>H: Set access_token cookie (HttpOnly, Secure, SameSite=Strict)
    H-->>C: 200 {access_token, refresh_token, expires_in}
```

## 3. Steps

1. Same-Origin check (CSRF protection; internal endpoint).
2. Decode and validate body — `refresh_token` field required.
3. Generate new 256-bit replacement token and its SHA-256 hash.
4. `RotateRefreshToken(SHA256(presented), replacement)` — one transaction:
   - If already used (replay): revoke entire family (`revoked_at=now()` on all
     rows sharing `family_id`), return `ErrRefreshTokenReplayed`.
   - If not found / expired: return 404.
   - Otherwise: mark presented token used, insert replacement inheriting
     `account_id` and `family_id`.
5. On replay: log metric `RefreshTokenReplayed`, clear `access_token` cookie, return 401 `TOKEN_INVALID`.
6. On expired/unknown: clear cookie, return 401 `TOKEN_EXPIRED`.
7. `FindAccountByID(rotated.AccountID)` — re-read from DB to pick up latest
   `nik_verified` state. A NIK verified or revoked since the last refresh takes
   effect here.
8. Issue new JWT with fresh claims.
9. Set new `access_token` cookie.
10. Return 200 with new pair.

## 4. Data touched

| Store | Key / table | Op | Notes |
|---|---|---|---|
| Aurora | `refresh_tokens` | UPDATE used_at + INSERT replacement | Single transaction via `RotateRefreshToken` |
| Aurora | `refresh_tokens` | UPDATE revoked_at (all in family) | Replay path only |
| Aurora | `accounts` | SELECT | Re-read to get current `nik_verified` |

## 5. Responses and errors

| HTTP | Code | When |
|---|---|---|
| 200 | — | New pair issued; `access_token` cookie updated |
| 400 | `VALIDATION_ERROR` | `refresh_token` field missing or empty |
| 401 | `TOKEN_INVALID` | Token replayed (family revoked) · account not found |
| 401 | `TOKEN_EXPIRED` | Token not found · expired · already revoked |
| 403 | — | Same-Origin check fails |

## 6. Edge cases and decisions

- **Replay and expired share 401, differ in code**: `TOKEN_INVALID` vs
  `TOKEN_EXPIRED`. The response message is intentionally vague — a replayed
  token should not tell the attacker that detection fired.
- **Family revocation on replay**: revoking only the replayed link would let an
  attacker who already holds a newer link continue. Revoking the whole family
  is the minimum safe response.
- **Per-login family**: a replay revocation only hits that login's chain, so a
  customer is not logged out of other devices.
- **`nik_verified` re-read**: the claim in the new JWT reflects the current
  database state, not the old token's claim. NIK status changes take effect
  on the next refresh.
- **Refresh token TTL**: 30 days (`entity.RefreshTokenTTL`). The access token
  (22740 s) covers a full sale; the refresh token is for sessions across days.

## 7. Tests

- [x] unit: valid token → 200, new pair returned, old token marked used
- [x] unit: missing body field → 400
- [x] unit: replayed token → 401 TOKEN_INVALID, family revoked
- [x] unit: expired token → 401 TOKEN_EXPIRED
- [x] unit: token for deleted account → 401 TOKEN_INVALID
- [x] unit: nik_verified reads from account, not from old claims
- [x] integration: concurrent refresh with same token → one 200, one 401 (replay)

## 8. Tasks

- [x] `RefreshSession` HTTP handler — body decode, cookie management
- [x] usecase `RefreshSession`: rotation, replay detection, account re-read
- [x] `RotateRefreshToken` adapter — single-transaction rotate or revoke
- [x] `ErrRefreshTokenReplayed` sentinel error in outbound port
