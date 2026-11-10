-- 0006_booking_domain.down.sql
--
-- tickets.booking_item_id is dropped before booking_items, because it
-- references it.

BEGIN;

DROP INDEX IF EXISTS tickets_booking_item_key;
ALTER TABLE tickets DROP COLUMN IF EXISTS booking_item_id;

DROP TABLE IF EXISTS payment_events;
DROP TABLE IF EXISTS nik_claims;
DROP TABLE IF EXISTS booking_items;

DROP INDEX IF EXISTS bookings_grant_token_key;
ALTER TABLE bookings DROP COLUMN IF EXISTS grant_token;

COMMIT;
