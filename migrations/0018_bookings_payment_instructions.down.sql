-- 0018_bookings_payment_instructions.down.sql
BEGIN;
ALTER TABLE bookings DROP COLUMN IF EXISTS payment_instructions;
COMMIT;
