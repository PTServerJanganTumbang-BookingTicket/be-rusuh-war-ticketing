-- 0001_user_domain.up.sql
--
-- Identity and NIK-evidence tables, per ADR-005 §4 and §8.
--
-- Design notes that the DDL enforces rather than leaves to application code:
--
--   * No column anywhere stores a raw NIK. nik_hmac is HMAC-SHA256 of the
--     normalised NIK under the KMS-managed pepper, computed by nik-verify.
--     nik_masked holds only the digits that responses are allowed to show.
--   * saved_niks.verified_at is NOT NULL. An unverified saved NIK would be used
--     at checkout to SKIP Dukcapil, which moves the fail-closed hole one step
--     back instead of closing it (ADR-005 §8). There is deliberately no PENDING
--     state, and no write path — migration, backfill or repair script — can
--     create a row without proof of verification.
--   * consent_records is append-only. Revocation is an operational action that
--     writes a new row elsewhere, never an UPDATE or DELETE here (UU PDP
--     No. 27/2022, NFR-11). Enforced by trigger below, not just convention.

BEGIN;

CREATE TABLE IF NOT EXISTS accounts (
    id                 TEXT        PRIMARY KEY,
    email              TEXT        NOT NULL,
    email_verified_at  TIMESTAMPTZ,
    -- NULL until registration completes with a Dukcapil-verified NIK.
    -- BR-11: an account without a verified NIK cannot purchase.
    nik_hmac           TEXT,
    nik_masked         TEXT,
    nik_verified_at    TIMESTAMPTZ,
    dukcapil_ref       TEXT,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- The three NIK columns travel together or not at all. A half-written NIK
    -- is the shape a crashed registration leaves behind, and it would read as
    -- "verified" to a query that only checks nik_verified_at.
    CONSTRAINT accounts_nik_all_or_nothing CHECK (
        (nik_hmac IS NULL AND nik_masked IS NULL AND nik_verified_at IS NULL)
        OR
        (nik_hmac IS NOT NULL AND nik_masked IS NOT NULL AND nik_verified_at IS NOT NULL)
    )
);

-- BR-11: one NIK, one account, globally. A partial index so the many accounts
-- still mid-registration (nik_hmac NULL) do not collide with each other.
CREATE UNIQUE INDEX IF NOT EXISTS accounts_nik_hmac_key
    ON accounts (nik_hmac) WHERE nik_hmac IS NOT NULL;

-- Registration and magic-link login both resolve an account by address, and a
-- second account on the same address orphans the first.
CREATE UNIQUE INDEX IF NOT EXISTS accounts_email_key
    ON accounts (lower(email));

CREATE TABLE IF NOT EXISTS consent_records (
    id             TEXT        PRIMARY KEY,
    account_id     TEXT        NOT NULL REFERENCES accounts (id) ON DELETE RESTRICT,
    policy_version TEXT        NOT NULL,
    consented_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS consent_records_account_id_idx
    ON consent_records (account_id);

-- Append-only in the strong sense: the database refuses, so a future repair
-- script cannot quietly rewrite the evidence that consent was given.
CREATE OR REPLACE FUNCTION consent_records_append_only() RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'consent_records is append-only (UU PDP No. 27/2022): % rejected', TG_OP;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS consent_records_no_mutation ON consent_records;
CREATE TRIGGER consent_records_no_mutation
    BEFORE UPDATE OR DELETE ON consent_records
    FOR EACH ROW EXECUTE FUNCTION consent_records_append_only();

CREATE TABLE IF NOT EXISTS saved_niks (
    id           TEXT        PRIMARY KEY,
    account_id   TEXT        NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    nik_hmac     TEXT        NOT NULL,
    nik_masked   TEXT        NOT NULL,
    label        TEXT        NOT NULL,
    -- See header: this is the invariant, not a convenience column.
    verified_at  TIMESTAMPTZ NOT NULL,
    dukcapil_ref TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT saved_niks_verified CHECK (verified_at IS NOT NULL),
    CONSTRAINT saved_niks_account_nik_key UNIQUE (account_id, nik_hmac)
);

CREATE INDEX IF NOT EXISTS saved_niks_account_id_idx
    ON saved_niks (account_id);

-- BR-12 caps the list at 5 per account. That is a count, not a row-level
-- predicate, so it is enforced in the use case inside a transaction that takes
-- SELECT … FOR UPDATE on the owning account row (ADR-005 §8). Recorded here so
-- the next person does not go looking for a constraint that cannot exist.

CREATE TABLE IF NOT EXISTS magic_tokens (
    id         TEXT        PRIMARY KEY,
    account_id TEXT        REFERENCES accounts (id) ON DELETE CASCADE,
    email      TEXT        NOT NULL,
    -- SHA-256 of the value mailed to the user. Storing the token itself would
    -- make a database read enough to log in as anyone with a live link.
    token_hash TEXT        NOT NULL UNIQUE,
    purpose    TEXT        NOT NULL DEFAULT 'LOGIN',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL,
    used_at    TIMESTAMPTZ,

    CONSTRAINT magic_tokens_purpose CHECK (purpose IN ('LOGIN', 'VERIFY_EMAIL'))
);

CREATE INDEX IF NOT EXISTS magic_tokens_expires_at_idx
    ON magic_tokens (expires_at) WHERE used_at IS NULL;

COMMIT;
