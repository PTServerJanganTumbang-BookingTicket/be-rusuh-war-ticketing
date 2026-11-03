-- 0004_engagement_read_model.up.sql
--
-- `bookings` and `queue_entries`, so a returning customer can be sent back to
-- where they were (BR-09, redirect_context in contract §1.3).
--
-- OWNERSHIP: queue-service owns queue_entries, booking-service owns bookings.
-- Neither exists in this repository yet, so the shape here is transcribed from
-- ADR-005 §4 and is the MINIMUM user-service reads. When those services land,
-- their own migrations become authoritative and the columns they add belong to
-- them; this file should then shrink to nothing rather than drift.
--
-- user-service only ever SELECTs from both.

BEGIN;

CREATE TABLE IF NOT EXISTS bookings (
    id                 TEXT        PRIMARY KEY,
    event_id           TEXT        NOT NULL,
    queue_token        TEXT,
    account_id         TEXT        REFERENCES accounts (id) ON DELETE SET NULL,
    contact_email      TEXT,
    status             TEXT        NOT NULL,
    granted_at         TIMESTAMPTZ,
    slot_claimed_at    TIMESTAMPTZ,
    held_at            TIMESTAMPTZ,
    -- Authoritative deadlines, written at CLAIM and at HELD (ADR-005 §4).
    booking_expires_at TIMESTAMPTZ,
    payment_expires_at TIMESTAMPTZ,
    payment_ref        TEXT,
    payment_attempts   INT         NOT NULL DEFAULT 0,
    payment_method     TEXT,
    payment_disputed   BOOLEAN     NOT NULL DEFAULT false,
    terms_version      TEXT,
    terms_accepted_at  TIMESTAMPTZ,
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT bookings_status CHECK (status IN (
        'GRANTED', 'SELECTING', 'HELD', 'AWAITING_PAYMENT',
        'PENDING_CANCELLATION', 'CONFIRMED', 'EXPIRED', 'LAPSED', 'ABANDONED'
    )),

    -- Conditional enforcement from ADR-005 §4: a GRANTED row has no email yet.
    CONSTRAINT bookings_contact_email_required CHECK (
        status IN ('GRANTED', 'LAPSED') OR contact_email IS NOT NULL
    ),
    CONSTRAINT bookings_terms_required CHECK (
        status IN ('GRANTED', 'SELECTING', 'LAPSED') OR terms_accepted_at IS NOT NULL
    )
);

-- The BR-09 lookup: this account's live engagement. Partial, because the
-- question is only ever asked about non-terminal rows and a confirmed booking
-- from last month must not slow it down.
CREATE INDEX IF NOT EXISTS bookings_active_account_idx
    ON bookings (account_id, created_at DESC)
    WHERE account_id IS NOT NULL
      AND status IN ('GRANTED', 'SELECTING', 'HELD', 'AWAITING_PAYMENT', 'PENDING_CANCELLATION');

-- Durable queue positions (ADR-005 §4; written asynchronously, §6).
CREATE TABLE IF NOT EXISTS queue_entries (
    event_id    TEXT        NOT NULL,
    queue_token TEXT        NOT NULL,
    ticket_no   BIGINT      NOT NULL,
    account_id  TEXT        REFERENCES accounts (id) ON DELETE SET NULL,
    joined_at   TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (event_id, queue_token),
    CONSTRAINT queue_entries_ticket_no_key UNIQUE (event_id, ticket_no)
);

-- Enforces BR-07 (one active engagement per event, logged-in only) and serves
-- the BR-09 lookup. Partial on account_id, per ADR-005 §4: guests are the
-- majority of rows and BR-07 does not apply to them.
CREATE INDEX IF NOT EXISTS queue_entries_account_idx
    ON queue_entries (account_id, event_id) WHERE account_id IS NOT NULL;

COMMIT;
