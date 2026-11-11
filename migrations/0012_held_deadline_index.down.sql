-- 0012_held_deadline_index.down.sql
BEGIN;
DROP INDEX IF EXISTS bookings_held_deadline_idx;
COMMIT;
