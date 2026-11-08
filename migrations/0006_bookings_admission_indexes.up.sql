-- 0006_bookings_admission_indexes.up.sql
--
-- What admission-ctl needs from `bookings` (bookings_store.feature, build step 2):
--   * bookings_event_active_idx: the 500 ms admission COUNT (open, browsing) reads
--     only the few active rows of one event, however many finished bookings exist
--     (ADR-006 §3.2).
--   * bookings_granted_deadline_idx: the LAPSED sweep finds GRANTED rows past
--     booking_expires_at with one UPDATE ... RETURNING (ADR-006 §8.12).
-- Both are partial: finished bookings (CONFIRMED, EXPIRED, LAPSED, ABANDONED) are the
-- vast majority of the table and neither query ever asks about them.

BEGIN;

CREATE INDEX IF NOT EXISTS bookings_event_active_idx
    ON bookings (event_id, status)
    WHERE status IN ('GRANTED', 'SELECTING', 'HELD', 'AWAITING_PAYMENT', 'PENDING_CANCELLATION');

CREATE INDEX IF NOT EXISTS bookings_granted_deadline_idx
    ON bookings (booking_expires_at)
    WHERE status = 'GRANTED';

COMMIT;
