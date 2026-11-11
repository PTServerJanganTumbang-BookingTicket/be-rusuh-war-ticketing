-- 0016_payment_method_switches.up.sql
--
-- The operator kill switch: turn a payment method, or one bank under it, off
-- mid-sale without a deploy.
--
-- What it is for. The payment breaker knows whether MIDTRANS is answering; it
-- knows nothing about one bank behind it. So when BCA's virtual accounts start
-- failing at minute 20 of a sale, every customer still sees BCA offered, still
-- gets a VA number they cannot pay into, and still loses their seats to the
-- sweep ten minutes later. Without this table the only lever is editing
-- entity.Catalogue() and waiting for the pipeline.
--
-- What it is NOT. It does not define the payment methods. The method list
-- stays in code, because a method is only chargeable once something shapes its
-- charge body and extracts its instrument from the response - functions, not
-- rows. This table can therefore only ever SUBTRACT from what the code offers:
-- a row naming a method the code does not know is ignored and logged, never
-- advertised. Offering a method the charge path cannot honour is the one
-- failure this design refuses to make possible.
--
-- APPEND-ONLY, like consent_records and payment_events, and for the same
-- reason: the interesting question is never only "is BCA on right now" but
-- "why could nobody pay with BCA between 20:14 and 21:30, and who decided
-- that". An UPDATE answers the first and destroys the second. So turning a
-- method back on is another INSERT, not a DELETE:
--
--   INSERT INTO payment_method_switches (method_id, bank_code, available, reason, changed_by)
--   VALUES ('bank_transfer', 'bca', false, 'BCA VA timeouts, confirmed with ops', 'hdwic');
--
--   INSERT INTO payment_method_switches (method_id, bank_code, available, reason, changed_by)
--   VALUES ('bank_transfer', 'bca', true, 'BCA recovered 21:28', 'hdwic');
--
-- Unlike consent_records there is no trigger forbidding mutation. That table
-- holds legal evidence; this one holds operational state, and an operator who
-- fat-fingers a row at 2am should be able to fix it rather than be told the
-- database refuses.

BEGIN;

CREATE TABLE IF NOT EXISTS payment_method_switches (
    -- Defaulted so the INSERT an operator types under pressure is as short as
    -- the examples above. gen_random_uuid() is built in from PG13.
    id          TEXT        PRIMARY KEY DEFAULT gen_random_uuid()::text,
    -- Must match a method id in the code registry (= Midtrans payment_type).
    -- Deliberately NOT a foreign key: the authority is code, and there is no
    -- table to point at. An unknown id is ignored at read time instead.
    method_id   TEXT        NOT NULL,
    -- NULL disables the whole method; a value disables one bank under it.
    bank_code   TEXT,
    available   BOOLEAN     NOT NULL,
    -- Not nullable on purpose. A switch with no stated reason is a mystery for
    -- whoever finds it, and they will find it during an incident.
    reason      TEXT        NOT NULL,
    changed_by  TEXT        NOT NULL,
    changed_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    -- A switch that reverts itself. The real failure mode is not forgetting to
    -- turn BCA off - it is turning it off during an incident and nobody
    -- remembering to turn it back on, so a bank stays dark for a week and the
    -- first sign is a conversion graph. NULL means "until someone says
    -- otherwise", which is sometimes right and should be a choice.
    expires_at  TIMESTAMPTZ,

    CONSTRAINT payment_method_switches_reason_not_blank CHECK (btrim(reason) <> ''),
    CONSTRAINT payment_method_switches_changed_by_not_blank CHECK (btrim(changed_by) <> ''),
    -- An expiry in the past is almost certainly a typo in the interval, and it
    -- would make the row a no-op the writer believes is in force.
    CONSTRAINT payment_method_switches_expiry_future CHECK (expires_at IS NULL OR expires_at > changed_at)
);

-- The read is "latest row per method and bank", so the index carries the sort.
-- changed_at DESC matches the ORDER BY, and the partial clause is not worth it
-- here: every row is a candidate for being the latest one.
CREATE INDEX IF NOT EXISTS payment_method_switches_latest_idx
    ON payment_method_switches (method_id, bank_code, changed_at DESC, id DESC);

COMMENT ON TABLE payment_method_switches IS
    'Append-only operator kill switch. Subtracts from the code catalogue; never defines it. Latest row per (method_id, bank_code) wins.';

COMMIT;
