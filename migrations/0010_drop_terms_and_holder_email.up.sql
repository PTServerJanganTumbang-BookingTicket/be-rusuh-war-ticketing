-- 0010_drop_terms_and_holder_email.up.sql
--
--   * bookings.terms_version, bookings.terms_accepted_at and their check bookings_terms_required: consent (PDP and
--     terms) is recorded at registration, not at POST /bookings (ADR-006 §8.16). Without dropping the check, the
--     HELD update would be refused, because it no longer writes terms_accepted_at.
--   * booking_items.holder_email: every e-ticket goes to bookings.contact_email (ADR-006 §8.15, FR-26 changed).
--     tickets.holder_email (migration 0002) stays: it holds the e-ticket's address and user-service reads it.

BEGIN;

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_terms_required;
ALTER TABLE bookings DROP COLUMN IF EXISTS terms_version;
ALTER TABLE bookings DROP COLUMN IF EXISTS terms_accepted_at;
ALTER TABLE booking_items DROP COLUMN IF EXISTS holder_email;

COMMIT;
