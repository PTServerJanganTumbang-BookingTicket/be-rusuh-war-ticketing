-- 0002_tickets_read_model.up.sql
--
-- The issued-ticket table. booking-service OWNS the writes; user-service only
-- reads it, to serve GET /users/me/tickets and GET /tickets/{id}/eticket.
-- It lives in this migration set because ADR-005 §4 puts every domain in one
-- Aurora cluster; move it to a booking-owned migration if that ever splits.
--
-- OWNERSHIP, which §9 leaves open for accounts (it is written for queue tokens):
-- a ticket is reachable by the account that BOUGHT it and by the account whose
-- own registered NIK holds it. It is deliberately NOT reachable through
-- saved_niks — otherwise a stranger's purchase for a NIK you happen to have
-- saved would appear in your list and its e-ticket QR would be readable by you.
-- FR-32d still holds: deleting a saved NIK touches nothing here.

BEGIN;

CREATE TABLE IF NOT EXISTS tickets (
    id                   TEXT        PRIMARY KEY,
    booking_id           TEXT        NOT NULL,
    event_id             TEXT        NOT NULL,
    -- NULL for guest checkout, which FR-29 keeps supported.
    purchaser_account_id TEXT        REFERENCES accounts (id) ON DELETE SET NULL,
    -- The holder. Matched against the attendee's KTP at the gate by hashing
    -- what they present; never reversed (ADR-005 §8).
    nik_hmac             TEXT        NOT NULL,
    nik_masked           TEXT        NOT NULL,
    holder_email         TEXT        NOT NULL,
    category_id          TEXT        NOT NULL,
    category_name        TEXT        NOT NULL,
    price                BIGINT      NOT NULL,
    -- Opaque, unguessable, signed at render time. NOT the NIK: a QR is
    -- screenshotted, forwarded and photographed, so it carries no identity.
    qr_nonce             TEXT        NOT NULL,
    status               TEXT        NOT NULL DEFAULT 'VALID',
    issued_at            TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT tickets_status CHECK (status IN ('VALID', 'USED', 'EXPIRED', 'REVOKED'))
);

CREATE INDEX IF NOT EXISTS tickets_purchaser_idx
    ON tickets (purchaser_account_id) WHERE purchaser_account_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS tickets_nik_hmac_idx
    ON tickets (nik_hmac);

-- Event facts the ticket list renders. booking-service or the back-office owns
-- the writes; user-service joins against it so the ticket list does not have to
-- call another service to render a row.
CREATE TABLE IF NOT EXISTS events (
    id         TEXT        PRIMARY KEY,
    name       TEXT        NOT NULL,
    event_date DATE        NOT NULL,
    event_time TEXT        NOT NULL DEFAULT '',
    venue      TEXT        NOT NULL DEFAULT '',
    image_url  TEXT        NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMIT;
