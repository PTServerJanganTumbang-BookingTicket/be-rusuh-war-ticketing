-- 0014_drop_consent_policy_version.up.sql
--
-- Drops consent_records.policy_version.
--
-- The column was NOT NULL with no default, and the registration INSERT stopped
-- supplying it when the field was removed from POST /auth/register's body. So
-- every registration against this schema fails on the not-null constraint:
-- the account, its consent record, its verification token and the
-- account.verify event commit together, which means the whole registration
-- rolls back and the customer sees a 500. There is no partial outcome and no
-- half-written account — the transaction is doing its job — but nobody can
-- register at all. This migration is what completes that removal.
--
-- Why the field went, rather than the server supplying a value for it: the
-- value arrived from the client, so the record could only ever be as reliable
-- as whatever posted the form — for a record whose entire purpose is to be
-- trustworthy evidence under UU PDP No. 27/2022. A field like that is worse
-- than an absent one, because it looks like provenance and is not. What
-- remains answers the question the law actually asks: WHO consented
-- (account_id) and WHEN (consented_at). Which policy text that was is answered
-- from the server's side, by a versioned policy document and the timestamp.
--
-- An unchanged client that still posts "policy_version" is unaffected: the
-- decoder ignores the extra member rather than rejecting the body.
--
-- The append-only trigger does not stand in the way: it fires BEFORE UPDATE OR
-- DELETE on rows, and dropping a column is DDL. No row is rewritten, and the
-- evidence that consent was given survives intact.

BEGIN;

ALTER TABLE consent_records DROP COLUMN IF EXISTS policy_version;

COMMIT;
