-- 0014_drop_consent_policy_version.down.sql
--
-- Restores the column, NULLABLE — which is as faithful as a reversal can be.
--
-- It cannot come back NOT NULL. Rows written while the column was absent have
-- no value for it, there is no honest value to backfill (the whole point of
-- dropping it was that the client's value was not evidence), and
-- consent_records is append-only in the strong sense: the
-- consent_records_no_mutation trigger makes the database refuse an UPDATE, so a
-- backfill is not merely unwise here, it is impossible by design.
--
-- Re-adding it NOT NULL WITH a default would be worse than leaving it
-- nullable: every pre-existing row would silently claim to have agreed to
-- whatever that default named.

BEGIN;

ALTER TABLE consent_records ADD COLUMN IF NOT EXISTS policy_version TEXT;

COMMIT;
