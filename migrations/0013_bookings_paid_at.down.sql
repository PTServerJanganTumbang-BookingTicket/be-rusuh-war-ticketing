-- 0013_bookings_paid_at.down.sql
--
-- Dropping this loses the settlement times recorded while it existed. They are
-- still recoverable from payment_events.raw, which keeps every notification
-- body verbatim, so this is a reversal rather than a data loss.

BEGIN;
ALTER TABLE bookings DROP COLUMN IF EXISTS paid_at;
COMMIT;
