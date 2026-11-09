-- 0005_outbox_drop_tracking.up.sql
--
-- Lets the publisher record that a row was deliberately NOT sent, without
-- pretending it was.
--
-- Background: outbox-publisher claims every row WHERE published_at IS NULL,
-- with no notion of age. After any backlog it therefore delivers events whose
-- contents have expired - a magic link created 26 minutes ago carries a token
-- that died at 15, so the recipient gets an email whose only possible outcome
-- is "invalid or expired login link". Worse at scale: an outage during a sale
-- ends with thousands of dead links delivered in one burst, each one a support
-- question arriving while you are still firefighting.
--
-- The publisher now skips those. It needs somewhere to say so.
--
-- published_at is deliberately left NULL for a dropped row: it was never
-- published, and recording otherwise would make the audit trail lie. The claim
-- query excludes both instead.

ALTER TABLE outbox ADD COLUMN IF NOT EXISTS dropped_at  TIMESTAMPTZ;
ALTER TABLE outbox ADD COLUMN IF NOT EXISTS drop_reason TEXT;

-- The claim query filters on both columns, so the partial index must too.
-- Without this the publisher scans every dropped row forever.
CREATE INDEX IF NOT EXISTS outbox_pending_idx
    ON outbox (created_at, id)
    WHERE published_at IS NULL AND dropped_at IS NULL;

COMMENT ON COLUMN outbox.dropped_at IS
    'Set when the publisher refused to send: the event outlived its usefulness. Mutually exclusive with published_at.';
COMMENT ON COLUMN outbox.drop_reason IS
    'Why it was dropped, e.g. "stale: 26m0s old, max 16m0s".';
