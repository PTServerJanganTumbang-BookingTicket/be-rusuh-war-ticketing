# 06 — NIK (Dukcapil Verification)

Verifies an Indonesian national identity number (NIK) against the Dukcapil
registry. This is an internal service — its only consumer at POC scope is
`services/user`. Every upstream service that needs a verified NIK calls through
this service; the HMAC pepper lives here and nowhere else.

**Service**: `services/nik-verify` (Lambda + API Gateway)

**Sources**: ADR-005 §8 · api-contracts §4.2 · requirements FR-15a ·
BR-13, BR-13a · NFR-12.

## Files

| # | File | Apidog request |
|---|---|---|
| 01 | [01-validate-nik.md](01-validate-nik.md) | `POST /nik/validate` |

## 1. Component overview

```mermaid
flowchart LR
    subgraph Callers
        US[user-service<br/>register + saved-NIK]
    end

    subgraph nik-verify service
        H[HTTP handler]
        UC[NikUsecase]
    end

    subgraph Guards
        EV[(Evidence DB<br/>accounts + saved_niks)]
        RC[(Redis cache<br/>nik:valid:{hmac})]
        BK[(Redis breaker<br/>nik:breaker:state)]
        RL[(Redis rate limiter<br/>nik:rps)]
    end

    DUK[Dukcapil API]

    US --> H --> UC
    UC -->|1. evidence| EV
    UC -->|2. cache| RC
    UC -->|3. breaker state| BK
    UC -->|4. rate limit| RL
    UC -->|5. call| DUK
    UC -->|record outcome| BK
    UC -->|cache positive| RC
```

## 2. Verification pipeline

The pipeline order is the design (usecase `Validate`):

| Step | Guard | Fail behaviour |
|---|---|---|
| 1 | Format check (16 digits after normalise) | 400 — caller's error, no paid call |
| 2 | Evidence store (accounts + saved_niks) | Fail closed on DB error |
| 3 | 24-hour Redis cache | Fail open on Redis error (miss costs one extra Dukcapil call) |
| 4 | Circuit breaker (`CLOSED / HALF_OPEN / OPEN`) | Stop at OPEN (503) |
| 5 | Rate limiter (contracted RPS ceiling) | 503 when throttled |
| 6 | Dukcapil API | 503 on error; record outcome to breaker |

Steps 2 and 3 run **before** 4 deliberately: a NIK with stored proof is answerable
while Dukcapil is down, and refusing it would throw away the exact benefit of
storing proof at the moment it matters most.

## 3. Key design decisions

- **Pepper lives here only**: the HMAC key is a KMS-managed secret loaded at
  startup. Every caller receives the hash and masked form in the response; they
  persist those and never hold the secret. Rotating the pepper is impossible
  (ADR-005 §8) — all stored hashes would become orphans.
- **Always 200 for valid/invalid NIK**: the call succeeded; the answer was yes or
  no. Non-2xx is reserved for "no answer obtainable", which is what callers must
  fail closed on.
- **Circuit breaker state shared via Redis**: `admission-ctl` reads the same key
  (`nik:breaker:state`) to pause admissions when Dukcapil is unusable (BR-13a).
- **Only positive answers cached**: caching a rejection would strand a customer
  who mistyped once behind a 24-hour window.

## 4. Open decisions

| # | Decision |
|---|---|
| 1 | **OPEN:** Dukcapil stub vs live call in staging. Current impl ships a stub (`services/nik-verify/internal/nik/infrastructure/dukcapil/stub.go`) for testing. Confirm when live credentials will be available. |
| 2 | **OPEN:** `DUKCAPIL_RATE_LIMIT_RPS` config value — the contracted RPS ceiling. Needs to be set before load testing. |
