-- 0005_bookings_grant_lookup.up.sql
--
-- What queue-service (join) needs from `bookings` (04-queue README §5 step 2):
--   * grant_token: admission-ctl writes it at GRANT; join's empty-queue wait
--     (FR-05) and a rejoin read it back.
--   * UNIQUE (event_id, queue_token): one engagement per queue_token per event,
--     and the index join's lookup uses.
-- The admission count's partial index on active statuses lands with
-- admission-ctl (bookings_store.feature, build step 2).

BEGIN;

ALTER TABLE bookings ADD COLUMN IF NOT EXISTS grant_token TEXT;

CREATE UNIQUE INDEX IF NOT EXISTS bookings_grant_token_key
    ON bookings (grant_token);

CREATE UNIQUE INDEX IF NOT EXISTS bookings_event_queue_token_key
    ON bookings (event_id, queue_token);

COMMIT;
