-- 0009_booking_domain.down.sql
--
-- Restores the 0004 checks. Fails while a row exists that only the 0009 rule allows (e.g. an EXPIRED booking
-- without email): such rows must be dealt with first, rather than silently violating the old rule.
BEGIN;
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_terms_required;
ALTER TABLE bookings ADD CONSTRAINT bookings_terms_required CHECK (
    status IN ('GRANTED', 'SELECTING', 'LAPSED') OR terms_accepted_at IS NOT NULL
);
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_contact_email_required;
ALTER TABLE bookings ADD CONSTRAINT bookings_contact_email_required CHECK (
    status IN ('GRANTED', 'LAPSED') OR contact_email IS NOT NULL
);
DROP INDEX IF EXISTS bookings_selecting_deadline_idx;
DROP INDEX IF EXISTS tickets_booking_item_key;
ALTER TABLE tickets DROP COLUMN IF EXISTS booking_item_id;
DROP TABLE IF EXISTS nik_claims;
DROP TABLE IF EXISTS booking_items;
COMMIT;
