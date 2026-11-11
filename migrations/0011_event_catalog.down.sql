-- 0011_event_catalog.down.sql
BEGIN;

DROP INDEX IF EXISTS events_listing_idx;

ALTER TABLE events
    DROP COLUMN IF EXISTS updated_at,
    DROP COLUMN IF EXISTS sale_starts_at,
    DROP COLUMN IF EXISTS location_city,
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS category;

COMMIT;
