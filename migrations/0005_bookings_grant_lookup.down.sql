-- 0005_bookings_grant_lookup.down.sql
BEGIN;
DROP INDEX IF EXISTS bookings_event_queue_token_key;
DROP INDEX IF EXISTS bookings_grant_token_key;
ALTER TABLE bookings DROP COLUMN IF EXISTS grant_token;
COMMIT;
