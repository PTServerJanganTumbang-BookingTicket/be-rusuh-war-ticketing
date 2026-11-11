-- 0014_payment_method_switches.down.sql
--
-- Dropping the table re-enables everything the code offers, which is the state
-- before it existed. Any switch still in force at that moment stops applying,
-- so check for one first:
--
--   SELECT DISTINCT ON (method_id, bank_code) method_id, bank_code, available, reason
--   FROM payment_method_switches
--   WHERE expires_at IS NULL OR expires_at > now()
--   ORDER BY method_id, bank_code, changed_at DESC, id DESC;

BEGIN;
DROP TABLE IF EXISTS payment_method_switches;
COMMIT;
