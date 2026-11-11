-- 0012_held_deadline_index.up.sql
--
-- The HELD -> EXPIRED sweep (docs/05-booking/02-create-booking.md §3.4) finds the held bookings whose 10-minute payment
-- window has passed. A partial index on the deadline of HELD rows only keeps that lookup off the rest of the table, as
-- bookings_granted_deadline_idx (0007) and bookings_selecting_deadline_idx (0009) do for the other two sweeps.

BEGIN;

CREATE INDEX IF NOT EXISTS bookings_held_deadline_idx
    ON bookings (payment_expires_at)
    WHERE status = 'HELD';

COMMIT;
