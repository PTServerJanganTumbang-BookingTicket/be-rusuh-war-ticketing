-- 0019_bookings_awaiting_payment_sweep.up.sql
--
-- The AWAITING_PAYMENT backstop sweep (docs/05-booking/02-create-booking.md, §3.4 fourth rung) finds bookings whose
-- payment deadline has passed without a gateway notification arriving. Each candidate costs a Midtrans Status call, so
-- the sweep's throughput is bounded by gateway round trips — not by database scan speed.
--
-- A narrow partial index rather than extending 0012's to two statuses: 0012 serves the HELD sweep, and each sweep reads
-- only its own status. Two narrow indexes each read by one query beat one wide index read by two, and the planner
-- eliminates the unused one from updates that do not match its predicate.
--
-- The sweep query is:
--
--   SELECT id, payment_ref, payment_expires_at
--     FROM bookings
--    WHERE status = 'AWAITING_PAYMENT'
--      AND payment_expires_at < $1
--    ORDER BY payment_expires_at
--    LIMIT $2
--
-- Without this index the planner must sequential-scan every booking row, which is dominated by finished (CONFIRMED,
-- EXPIRED, LAPSED) rows. With it, the lookup reads only the rows it can return.

BEGIN;

CREATE INDEX IF NOT EXISTS bookings_awaiting_payment_deadline_idx
    ON bookings (payment_expires_at)
    WHERE status = 'AWAITING_PAYMENT';

COMMIT;
