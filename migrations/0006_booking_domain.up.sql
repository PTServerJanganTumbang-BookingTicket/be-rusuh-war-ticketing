-- 0006_booking_domain.up.sql
--
-- Tables owned by booking-service and payment-api, plus the bookings columns
-- 0004 left out because user-service never reads them.
--
-- Landed on the payment branch because payment-api cannot function without
-- payment_events, booking_items, nik_claims and bookings.grant_token. The shape
-- is impl-plan section 4.2 verbatim; booking-service inherits it rather than
-- writing a second migration over the same tables.

BEGIN;

-- grant_token: the credential entitled to this booking.
--
-- DISTINCT from queue_token, which 0004 already has. queue_token answers "which
-- engagement produced this row" and is what rebuild joins on (ADR-005 section
-- 12 step 3). grant_token answers "which credential is entitled to this row"
-- and is what every ownership clause in contract sections 4 and 5 matches. The
-- grant is revoked with a single DEL at a terminal state; queue_token must stay
-- joinable to queue_entries afterwards. One column cannot be both.
ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS grant_token TEXT;

-- Partial, so terminal rows may drop their grant without colliding on NULL.
CREATE UNIQUE INDEX IF NOT EXISTS bookings_grant_token_key
    ON bookings (grant_token) WHERE grant_token IS NOT NULL;

-- One row per seat (BR-02: 1 NIK = 1 seat). Seat numbers are not in the POC.
CREATE TABLE IF NOT EXISTS booking_items (
    id            TEXT        PRIMARY KEY,
    booking_id    TEXT        NOT NULL REFERENCES bookings (id) ON DELETE CASCADE,
    event_id      TEXT        NOT NULL,
    category_id   TEXT        NOT NULL,
    -- Copied, not joined: an e-ticket must show the category and price AS THEY
    -- APPLIED AT PURCHASE, even after the catalogue changes.
    category_name TEXT        NOT NULL,
    unit_price    BIGINT      NOT NULL,
    nik_hmac      TEXT        NOT NULL,
    nik_masked    TEXT        NOT NULL,
    -- FR-26: a ticket goes to each NIK holder, not only the purchaser. Defaults
    -- to bookings.contact_email, overridable per seat.
    holder_email  TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),

    -- Block a duplicate NIK WITHIN one booking (ADR-005 section 4).
    CONSTRAINT booking_items_booking_nik_key UNIQUE (booking_id, nik_hmac)
);

CREATE INDEX IF NOT EXISTS booking_items_booking_idx
    ON booking_items (booking_id);

-- Stock reconciliation counts per category per event over HELD /
-- AWAITING_PAYMENT / CONFIRMED. Without this index that query scans the table
-- every 60 seconds, per category.
CREATE INDEX IF NOT EXISTS booking_items_event_category_idx
    ON booking_items (event_id, category_id);

-- BR-02 ENFORCED ACROSS BOOKINGS: one NIK = one seat per event, holding across
-- separate transactions. This is a database invariant, not a Redis promise.
--
-- nik:used in Redis is the fast path that rejects duplicates before a
-- transaction opens. This table is what makes a BR-02 violation IMPOSSIBLE TO
-- STORE, and what gives nik:used something to be reconciled against
-- (ADR-005 section 6).
CREATE TABLE IF NOT EXISTS nik_claims (
    event_id   TEXT        NOT NULL,
    nik_hmac   TEXT        NOT NULL,
    booking_id TEXT        NOT NULL REFERENCES bookings (id) ON DELETE CASCADE,
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (event_id, nik_hmac)
);

-- The sweep deletes claims per booking at expiry - without it a NIK stays
-- locked forever even though its booking was cancelled (ADR-005 section 8,
-- NIK lifecycle).
CREATE INDEX IF NOT EXISTS nik_claims_booking_idx
    ON nik_claims (booking_id);

-- Payment idempotency log.
--
-- gateway_event_id is the GATEWAY'S event id, not a booking_id. Using
-- booking_id here would swallow every subsequent LEGITIMATE event for the same
-- booking: pending -> settlement -> refund (ADR-005 section 9, FR-25).
--
-- Derived, because Midtrans sends no event id at all:
--   gateway_event_id = order_id + ':' + transaction_status + ':' + status_code
-- A redelivery trips the UNIQUE constraint, the whole transaction rolls back,
-- and the endpoint still answers 200 (api-contracts section 5.5).
CREATE TABLE IF NOT EXISTS payment_events (
    id                 TEXT        PRIMARY KEY,
    booking_id         TEXT        NOT NULL REFERENCES bookings (id),
    gateway_event_id   TEXT        NOT NULL UNIQUE,
    transaction_status TEXT        NOT NULL,
    fraud_status       TEXT,
    -- The webhook's payment_type MAY DIFFER from the one sent at charge: a
    -- GoPay QR paid with another QRIS app arrives as 'qris'. Recorded, NOT
    -- matched (ADR-005 section 9).
    payment_type       TEXT,
    gross_amount       BIGINT,
    raw                JSONB       NOT NULL,
    received_at        TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS payment_events_booking_idx
    ON payment_events (booking_id, received_at DESC);

-- tickets.booking_item_id - the double-QR guard.
--
-- ADR-005 section 16 lists "UNIQUE (booking_item_id) on tickets" as the
-- mitigation for "outbox retry issues a duplicate QR". 0002 created tickets
-- with booking_id only, so that mitigation could not be built: two ticket.issue
-- attempts for the same booking did not collide.
--
-- Nullable + partial unique, because pre-existing tickets rows have no value.
ALTER TABLE tickets
    ADD COLUMN IF NOT EXISTS booking_item_id TEXT REFERENCES booking_items (id);

CREATE UNIQUE INDEX IF NOT EXISTS tickets_booking_item_key
    ON tickets (booking_item_id) WHERE booking_item_id IS NOT NULL;

-- NOTE: qr_nonce is NOT renamed. ADR-005 section 4 calls this column
-- qr_payload; the existing name wins because it already has a writer and a
-- reader (user-service GET /tickets/{id}/eticket). The name is mapped in the
-- mapper layer. Decision: impl-plan section 2.3.

COMMENT ON COLUMN bookings.grant_token IS
    'Credential entitled to this booking. Matched by every ownership clause in contract sections 4 and 5. Distinct from queue_token, which is for rebuild.';
COMMENT ON TABLE nik_claims IS
    'Database enforcement of BR-02 across bookings. nik:used in Redis is only the fast path.';
COMMENT ON COLUMN payment_events.gateway_event_id IS
    'order_id + '':'' + transaction_status + '':'' + status_code. Midtrans sends no event id, so the key is derived. UNIQUE makes a redelivery a no-op.';

COMMIT;
