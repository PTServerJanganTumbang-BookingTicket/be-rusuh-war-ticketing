-- 0011_event_catalog.up.sql
--
-- The event catalog read by GET /events and GET /events/{event_id} (api-contracts §2, docs/03-events).
-- events (0002) held what the ticket list renders; the home and detail pages need more.
--
-- sale_starts_at: NULL until the sale is scheduled. The sale STATE is not here: it lives in queue:{ev} (ADR-006 §8.12).
-- events_listing_idx: the home page reads newest event date first, then id.

BEGIN;

ALTER TABLE events
    ADD COLUMN IF NOT EXISTS category       TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS description    TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS location_city  TEXT        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS sale_starts_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS updated_at     TIMESTAMPTZ NOT NULL DEFAULT now();

CREATE INDEX IF NOT EXISTS events_listing_idx ON events (event_date DESC, id);

COMMIT;
