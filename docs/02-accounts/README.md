# 02 — Accounts

Manages a logged-in customer's saved NIK list ("Daftar NIK Teman"). Saved NIKs are
pre-verified friend NIKs that can be used at checkout without an extra Dukcapil call,
reducing paid API usage (NFR-12) and removing Dukcapil as a hard gate during the
booking TTL.

**Service**: `services/user` (Lambda + API Gateway)

**Sources**: ADR-005 §8 · api-contracts §1.6 · requirements FR-15a, FR-32a..d ·
BR-12, BR-13 · NFR-11, NFR-12, NFR-14.

## Files

| # | File | Apidog request |
|---|---|---|
| 01 | [01-get-saved-niks.md](01-get-saved-niks.md) | `GET /accounts/me/saved-niks` |
| 02 | [02-add-saved-nik.md](02-add-saved-nik.md) | `POST /accounts/me/saved-niks` |
| 03 | [03-delete-saved-nik.md](03-delete-saved-nik.md) | `DELETE /accounts/me/saved-niks/{id}` |

## 1. Design constraints

- **No raw NIK ever stored or returned**: only `nik_hmac` (HMAC-SHA256 keyed by
  nik-verify's pepper) and `nik_masked` (first 4 + last 2 digits). The full 16
  digits are neither persisted nor echoed (FR-32c).
- **Verified-before-insert invariant**: `saved_niks.verified_at IS NOT NULL` is
  enforced by a `CHECK` constraint in the DB migration (`0001_user_domain.up.sql`).
  An unverified entry would bypass Dukcapil at checkout, moving the fail-closed
  hole rather than closing it.
- **5-entry cap per account** (BR-12): enforced under a `SELECT … FOR UPDATE` on
  the owning account row, so two concurrent POSTs cannot both see four entries
  and both insert.
- **Consent prerequisite**: accounts predating the consent requirement exist.
  `AddSavedNIK` checks `HasConsent` before calling Dukcapil, because holding
  someone else's NIK without a consent record is a UU PDP breach (NFR-11).

## 2. Key table

```sql
-- migration 0001_user_domain.up.sql
CREATE TABLE saved_niks (
    id           TEXT PRIMARY KEY,   -- snk_{ULID}
    account_id   TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    nik_hmac     TEXT NOT NULL,
    nik_masked   TEXT NOT NULL,
    label        TEXT NOT NULL,
    verified_at  TIMESTAMPTZ NOT NULL,  -- CHECK (verified_at IS NOT NULL)
    dukcapil_ref TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT saved_niks_account_nik_key UNIQUE (account_id, nik_hmac)
);
```

## 3. Open decisions

| # | Decision |
|---|---|
| 1 | **OPEN:** No periodic re-validation of stored NIKs. A NIK verified today and stored is used permanently without re-checking Dukcapil. Safe for POC because the check-in gate is out of scope. |
| 2 | **OPEN:** Consent check reads `consent_records` on every AddSavedNIK. Consider caching on the account entity once consent columns are added to the accounts read path. |
