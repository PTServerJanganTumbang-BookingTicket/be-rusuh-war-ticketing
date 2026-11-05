-- 0003_outbox_and_refresh.up.sql
--
-- The transactional outbox (ADR-005 §4, §9) and refresh-token rotation.

BEGIN;

-- The shared outbox. ADR-005 §4 gives the shape:
--   outbox(id, aggregate_id, type, payload jsonb, published_at NULL)
--
-- Every producer writes here IN THE SAME TRANSACTION as the state change the
-- event describes. That is the whole point: an account that exists with no
-- verification email queued, or an email for an account that rolled back, are
-- both impossible if the two writes commit together. Publishing is a separate,
-- retryable step (outbox-publisher), so a broker outage cannot fail a
-- registration (NFR-10).
--
-- Payloads carry plaintext link tokens, because the email cannot be built
-- without them. That makes this table short-lived operational state, not an
-- audit log — see the retention note at the bottom.
CREATE TABLE IF NOT EXISTS outbox (
    id           TEXT        PRIMARY KEY,
    aggregate_id TEXT        NOT NULL,
    type         TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);

-- The publisher's only query: unpublished rows, oldest first. A partial index
-- keeps it the size of the backlog rather than the size of all history, so
-- OutboxLag stays cheap to measure once millions of rows have drained.
CREATE INDEX IF NOT EXISTS outbox_unpublished_idx
    ON outbox (created_at, id) WHERE published_at IS NULL;

-- Refresh tokens.
--
-- Access tokens live SALE_WINDOW_SECONDS + 1140 (contract §1.3: 22740s at the
-- 21600s fallback), so the session outlives the queue wait AND the 15.5-minute
-- journey. Deliberately not GRANT_TOKEN_TTL_SECONDS, which bounds a different
-- interval. These rows are what carry a session beyond that — without them a
-- customer who comes back the next day has to request a new magic link.
--
-- Design, following OAuth 2.0 BCP §4.13:
--   * Opaque and random, stored as SHA-256. A database read must not be enough
--     to impersonate anyone, same rule as magic_tokens.
--   * One-time use. Redeeming one issues a replacement (rotation).
--   * family_id groups a rotation chain. Replaying a spent token is the
--     signature of a stolen token, and the response is to revoke the entire
--     family — not just the replayed link, because the thief may already hold
--     a newer one.
CREATE TABLE IF NOT EXISTS refresh_tokens (
    id          TEXT        PRIMARY KEY,
    account_id  TEXT        NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    family_id   TEXT        NOT NULL,
    token_hash  TEXT        NOT NULL UNIQUE,
    issued_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    revoked_at  TIMESTAMPTZ,
    -- The token this one was rotated into. Lets an operator walk a chain when
    -- investigating a revocation.
    replaced_by TEXT
);

CREATE INDEX IF NOT EXISTS refresh_tokens_family_idx
    ON refresh_tokens (family_id) WHERE revoked_at IS NULL;

CREATE INDEX IF NOT EXISTS refresh_tokens_account_idx
    ON refresh_tokens (account_id);

-- Sweeping expired rows. Both tables accumulate and neither is a record of
-- anything once drained or lapsed:
--
--   DELETE FROM outbox WHERE published_at < now() - INTERVAL '7 days';
--   DELETE FROM refresh_tokens WHERE expires_at < now() - INTERVAL '30 days';
--
-- Not scheduled here: no job runner is committed yet. The retention floor in
-- ADR-005 §8 concerns NIK columns and does not apply — neither table holds a
-- nik_hmac.

COMMIT;
