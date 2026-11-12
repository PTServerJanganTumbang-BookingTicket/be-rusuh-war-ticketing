-- 0018_bookings_payment_instructions.up.sql
--
-- What the customer has to DO to pay, on the booking row.
--
-- The instrument detail — the virtual account number and its bank, or the QR
-- url and deeplink — is returned once by §5.2 and then unreachable. A customer
-- who closes the tab and reopens the status page is told PAID or PENDING, the
-- amount, the method and the payment id, but not the VA number to pay into.
-- The data already exists: it is in payment_events.raw under "instructions".
-- It is simply not on a row any endpoint reads.
--
-- Why a column rather than a query over payment_events. The log holds one
-- entry per attempt and is append-only; the booking holds current state, and
-- exactly one set of instructions is current at a time. That is the same split
-- payment_ref, payment_method, paid_at and payment_status already use — all
-- derivable from the log, all kept here anyway, because the read path for a
-- customer polling a status page should not be a scan of an audit trail.
--
-- The alternative considered was filtering payment_events on
-- gateway_event_id LIKE '%:charge:%'. It would not have been slow — booking_id
-- is indexed and selective, so the pattern filters a dozen rows rather than a
-- table — but recovering a record's TYPE by pattern-matching a composite key
-- is fragile: order_id is booking_id + ':' + attempt, and booking ids may
-- themselves contain colons (ParseOrderID supports it), so a booking id
-- containing ':charge:' would match notification rows too.
--
-- The EXTRACTED instructions, not the raw gateway body. The raw response stays
-- in payment_events.raw under "gateway": it is the audit record, there is one
-- per attempt, and it is shaped by Midtrans rather than by us. Storing the
-- extracted form here also keeps a promise the replay path already makes —
-- re-parsing a Midtrans response in two places is how a VA number and a QR url
-- end up rendered differently on the first and second request.
--
-- JSONB rather than columns per field, because the shape is method-dependent:
--   bank_transfer -> {"bank": "bca", "va_number": "8801234567890"}
--   gopay         -> {"qr_url": "https://...", "deeplink_url": "https://..."}
-- Columns would mean a va_number column that is NULL for every e-wallet
-- payment and a qr_url column NULL for every transfer.
--
-- Nullable: a booking that never reached a charge has no instructions, and an
-- empty object would read as "nothing to do" rather than "nothing charged".
BEGIN;

ALTER TABLE bookings ADD COLUMN IF NOT EXISTS payment_instructions JSONB;

COMMENT ON COLUMN bookings.payment_instructions IS
    'Extracted instrument detail for the CURRENT attempt: {bank, va_number} or {qr_url, deeplink_url}. NULL before any charge. The raw gateway body stays in payment_events.raw.';

COMMIT;
