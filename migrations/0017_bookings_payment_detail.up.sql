-- 0017_bookings_payment_detail.up.sql
--
-- The payment detail §5.4 reports, persisted rather than re-derived.
--
-- Before this, GET /payments/{booking_id}/status could answer almost nothing
-- about the payment itself. GetOwned reads the bookings row alone and never
-- sums booking_items, so entity.Booking.Total was zero on that path; the
-- gateway's own status existed only inside payment_events.raw, where no
-- endpoint reads it. The customer polling the page after paying could be told
-- PAID, but not what they paid, by which method, or under which payment id.
--
-- payment_amount is the gross_amount actually SENT to Midtrans, not a
-- recomputed sum. The two agree today, because booking_items.unit_price is
-- copied at purchase and never follows the catalogue. They would stop agreeing
-- the moment anything edits a booking's items after a charge, and in that case
-- the receipt must show what the customer was charged, not what the seats cost
-- now. A sum is a derivation; this is a record.
--
-- payment_status is the gateway's own transaction_status — pending,
-- settlement, deny, expire — which is NOT bookings.status. The two answer
-- different questions: ours is where the booking is in its lifecycle, this is
-- what Midtrans last said about the money. Keeping it lets support see that a
-- booking is AWAITING_PAYMENT with a gateway status of deny without opening
-- payment_events.
--
-- Both nullable: every booking that never reached a charge has neither, and a
-- default would invent an amount and a status for bookings that were never
-- charged at all.
BEGIN;

ALTER TABLE bookings ADD COLUMN IF NOT EXISTS payment_amount BIGINT;
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS payment_status TEXT;

COMMENT ON COLUMN bookings.payment_amount IS
    'gross_amount sent to Midtrans at charge time. NULL before any charge. The record of what was charged, not a sum of booking_items.';
COMMENT ON COLUMN bookings.payment_status IS
    'Midtrans transaction_status (pending/settlement/deny/expire). NOT bookings.status. NULL before any charge.';

COMMIT;
