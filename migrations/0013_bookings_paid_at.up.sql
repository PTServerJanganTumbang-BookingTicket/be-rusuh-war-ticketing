-- 0013_bookings_paid_at.up.sql
--
-- When the money actually arrived.
--
-- §5.4 returns paid_at on a PAID booking, and until now there was no column to
-- answer it with, so the handler substituted payment_expires_at — the DEADLINE.
-- For a customer who paid at minute 2 of a ten-minute window that reads as a
-- timestamp eight minutes in the FUTURE of the response carrying it. Visibly
-- wrong on a receipt, and worse than wrong when someone later reconciles our
-- record against the gateway's.
--
-- Nullable, and deliberately so: it is NULL for every booking that is not
-- CONFIRMED, and for bookings confirmed before this column existed. A default
-- would invent a payment time for both.
--
-- The value comes from the gateway's own settlement_time (falling back to
-- transaction_time), not from now(): the notification can arrive seconds or
-- minutes after the money moved, and it is the money that the receipt is
-- about. now() is the last resort, for a notification that carries neither.

BEGIN;

ALTER TABLE bookings ADD COLUMN IF NOT EXISTS paid_at TIMESTAMPTZ;

COMMENT ON COLUMN bookings.paid_at IS
    'Gateway settlement time (WIB, stored UTC). NULL unless CONFIRMED. Source of paid_at in api-contracts 5.4.';

COMMIT;
