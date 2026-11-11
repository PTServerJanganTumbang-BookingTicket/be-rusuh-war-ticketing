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

-- The payment-deadline sweep (ADR-006 §8.12(c)).
--
-- §8.12(c) deleted `expiries:{ev}`, the Redis ZSET that used to hold these
-- deadlines: it was a second copy of a timestamp Aurora already had, with a
-- crash window between the UPDATE and the ZADD and a "deadline moved, score
-- stale" case of its own. The sweep now reads Aurora directly, the way the
-- admission COUNT already did, and this index is what keeps that read cheap:
-- finished bookings are the vast majority of the table and the sweep never
-- asks about them.
--
-- 0007 and 0009 added the same shape for the GRANTED and SELECTING deadlines
-- on booking_expires_at. This is the third and last one: the PAYMENT deadline,
-- which lives in a different column.
--
-- Both statuses that carry a live payment deadline are covered. A sweep
-- querying `status = 'HELD'` alone still uses it, because that predicate
-- implies this one.
CREATE INDEX IF NOT EXISTS bookings_payment_deadline_idx
    ON bookings (payment_expires_at)
    WHERE status IN ('HELD', 'AWAITING_PAYMENT');

COMMIT;
