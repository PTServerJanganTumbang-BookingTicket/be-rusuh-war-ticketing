-- 0019_bookings_awaiting_payment_sweep.down.sql

BEGIN;

DROP INDEX IF EXISTS bookings_awaiting_payment_deadline_idx;

COMMIT;
