-- 0013_payment_events.up.sql
--
-- payment_events: every notification and charge response the gateway gives us,
-- plus the payment-deadline index ADR-006 §8.12(c) asks for.
--
-- payment_events is the only TABLE payment-api needs that the booking
-- migrations (0006-0010) do not already provide. It is separate from `bookings` on purpose: the booking
-- row holds the CURRENT state, this holds the sequence of gateway facts that
-- produced it, and reconciliation needs the sequence.
BEGIN;

CREATE TABLE IF NOT EXISTS payment_events (
    id                TEXT        PRIMARY KEY,
    booking_id        TEXT        NOT NULL REFERENCES bookings (id) ON DELETE CASCADE,
    -- Derived, because Midtrans sends no event id:
    --   order_id + ':' + transaction_status + ':' + status_code
    -- UNIQUE is the webhook's whole idempotency guard. A redelivered
    -- notification trips it, the transaction rolls back, and the endpoint still
    -- answers 200 (api-contracts §5.5).
    --
    -- Not transaction_id: that is stable for the LIFETIME of one transaction,
    -- so the pending and settlement notifications of one payment share it and
    -- the second would be swallowed.
    gateway_event_id  TEXT        NOT NULL,
    order_id          TEXT        NOT NULL,
    transaction_status TEXT       NOT NULL,
    fraud_status      TEXT,
    payment_type      TEXT,
    gross_amount      BIGINT      NOT NULL DEFAULT 0,
    -- The body as received. The signature was computed over its exact bytes,
    -- so a later re-verification needs them unaltered - gross_amount above is
    -- a parsed convenience, not the source of truth.
    raw               JSONB       NOT NULL,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),

    CONSTRAINT payment_events_gateway_event_key UNIQUE (gateway_event_id)
);

-- Reconciliation reads one booking's events in order (§6.3).
CREATE INDEX IF NOT EXISTS payment_events_booking_idx
    ON payment_events (booking_id, created_at);

COMMENT ON COLUMN payment_events.gateway_event_id IS
    'order_id:transaction_status:status_code. UNIQUE; the webhook''s idempotency key (api-contracts 5.5).';

-- The payment-deadline sweep (ADR-006 §8.12(c)) is served by
-- bookings_held_deadline_idx from 0012, not by an index here.
--
-- §8.12(c) deleted `expiries:{ev}`, the Redis ZSET that used to hold these
-- deadlines: it was a second copy of a timestamp Aurora already had, with a
-- crash window between the UPDATE and the ZADD and a "deadline moved, score
-- stale" case of its own. The sweep reads Aurora directly instead, and 0012
-- is the index that keeps that read cheap.
--
-- This migration originally created a wider version of that index, covering
-- `status IN ('HELD', 'AWAITING_PAYMENT')`. It was dropped because nothing
-- reads the second half: admission-ctl's sweep is `status = 'HELD'` alone
-- (`held.go`), and an AWAITING_PAYMENT booking is ended by MIDTRANS, which
-- stops accepting payment at the custom_expiry deadline and sends an `expire`
-- notification that the webhook turns into EXPIRED. No local query ever asks
-- for due AWAITING_PAYMENT rows, so a wider index would have been maintained
-- on every write to the hottest table in the sale and read by no one.
--
-- If a backstop for lost expire notifications is ever added — an
-- AWAITING_PAYMENT booking whose deadline passed long ago and which Midtrans
-- never told us about — it is that query which needs the wider index, and it
-- should be added with the sweep that reads it.

COMMIT;
