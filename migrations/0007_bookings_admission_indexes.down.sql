-- 0007_bookings_admission_indexes.down.sql
BEGIN;
DROP INDEX IF EXISTS bookings_granted_deadline_idx;
DROP INDEX IF EXISTS bookings_event_active_idx;
COMMIT;
