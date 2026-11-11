-- 0013_payment_events.down.sql
BEGIN;
DROP INDEX IF EXISTS bookings_payment_deadline_idx;
DROP TABLE IF EXISTS payment_events;
COMMIT;
