-- 0017_bookings_payment_detail.down.sql
BEGIN;
ALTER TABLE bookings DROP COLUMN IF EXISTS payment_status;
ALTER TABLE bookings DROP COLUMN IF EXISTS payment_amount;
COMMIT;
