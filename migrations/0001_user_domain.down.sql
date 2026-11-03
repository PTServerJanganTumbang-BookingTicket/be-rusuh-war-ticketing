BEGIN;
DROP TABLE IF EXISTS magic_tokens;
DROP TABLE IF EXISTS saved_niks;
DROP TRIGGER IF EXISTS consent_records_no_mutation ON consent_records;
DROP TABLE IF EXISTS consent_records;
DROP FUNCTION IF EXISTS consent_records_append_only();
DROP TABLE IF EXISTS accounts;
COMMIT;
