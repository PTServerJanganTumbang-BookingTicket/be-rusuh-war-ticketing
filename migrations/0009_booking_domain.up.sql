-- 0009_booking_domain.up.sql
--
-- What booking-service needs from Aurora for its endpoints (docs/05-booking/README.md §5, impl-plan §4.2):
--   * booking_items  : one row per seat (BR-02), rendered by GET /bookings/{id}, written by POST /bookings.
--   * nik_claims     : one NIK = one seat per event, across bookings (BR-02). A database invariant, not a Redis promise.
--   * tickets.booking_item_id + UNIQUE : one ticket (one QR) per seat, also when the issuer retries.
--   * bookings_selecting_deadline_idx : the SELECTING -> EXPIRED sweep, every second on the writer.
--   * the two 0004 checks restated: email and terms are entered at POST /bookings (HELD), so a booking that
--     was claimed (SELECTING) and then expired or was abandoned has neither. Both are required from HELD on.

BEGIN;

CREATE TABLE IF NOT EXISTS booking_items (
    id            TEXT        PRIMARY KEY,
    booking_id    TEXT        NOT NULL REFERENCES bookings (id) ON DELETE CASCADE,
    event_id      TEXT        NOT NULL,
    category_id   TEXT        NOT NULL,
    -- Copied, not joined: the ticket shows the category and price that applied when it was bought.
    category_name TEXT        NOT NULL,
    unit_price    BIGINT      NOT NULL,
    nik_hmac      TEXT        NOT NULL,
    -- The HMAC cannot be turned back into digits, so the masked form is stored for display.
    nik_masked    TEXT        NOT NULL,
    holder_email  TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT booking_items_booking_nik_key UNIQUE (booking_id, nik_hmac)
);

-- No separate index on booking_id: the UNIQUE (booking_id, nik_hmac) index leads with it.
CREATE INDEX IF NOT EXISTS booking_items_event_category_idx ON booking_items (event_id, category_id);

CREATE TABLE IF NOT EXISTS nik_claims (
    event_id   TEXT        NOT NULL,
    nik_hmac   TEXT        NOT NULL,
    booking_id TEXT        NOT NULL REFERENCES bookings (id) ON DELETE CASCADE,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (event_id, nik_hmac)
);

CREATE INDEX IF NOT EXISTS nik_claims_booking_idx ON nik_claims (booking_id);

-- No ON DELETE action on purpose: an issued ticket must not disappear because its booking row is deleted.
-- Deleting a booking that has tickets fails as a whole (one statement), and the tickets are dealt with first.
ALTER TABLE tickets ADD COLUMN IF NOT EXISTS booking_item_id TEXT REFERENCES booking_items (id);

CREATE UNIQUE INDEX IF NOT EXISTS tickets_booking_item_key
    ON tickets (booking_item_id) WHERE booking_item_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS bookings_selecting_deadline_idx
    ON bookings (booking_expires_at)
    WHERE status = 'SELECTING';

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_contact_email_required;
ALTER TABLE bookings ADD CONSTRAINT bookings_contact_email_required CHECK (
    status NOT IN ('HELD', 'AWAITING_PAYMENT', 'PENDING_CANCELLATION', 'CONFIRMED') OR contact_email IS NOT NULL
);
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_terms_required;
ALTER TABLE bookings ADD CONSTRAINT bookings_terms_required CHECK (
    status NOT IN ('HELD', 'AWAITING_PAYMENT', 'PENDING_CANCELLATION', 'CONFIRMED') OR terms_accepted_at IS NOT NULL
);

COMMIT;
