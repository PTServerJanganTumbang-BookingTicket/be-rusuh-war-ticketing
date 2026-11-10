-- 0010_drop_terms_and_holder_email.down.sql
--
-- Puts the columns back. The restored check (from 0009) fails while a HELD-or-later booking exists, because the
-- dropped terms data cannot be recreated: deal with those rows first rather than invent consent.
-- booking_items.holder_email comes back empty (NOT NULL with an empty default); nothing reads it.
BEGIN;
ALTER TABLE booking_items ADD COLUMN IF NOT EXISTS holder_email TEXT NOT NULL DEFAULT '';
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS terms_version TEXT;
ALTER TABLE bookings ADD COLUMN IF NOT EXISTS terms_accepted_at TIMESTAMPTZ;
ALTER TABLE bookings ADD CONSTRAINT bookings_terms_required CHECK (
    status NOT IN ('HELD', 'AWAITING_PAYMENT', 'PENDING_CANCELLATION', 'CONFIRMED') OR terms_accepted_at IS NOT NULL
);
COMMIT;
