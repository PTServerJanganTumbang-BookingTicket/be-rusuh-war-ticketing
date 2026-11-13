package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/events"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/entity"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/valueobject"
	portout "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/outbound"
)

// bookingColumns is the projection payment-api needs. Listed explicitly so a
// column added by another service's migration cannot change Scan order.
const bookingColumns = `
    id, event_id, COALESCE(account_id, ''), COALESCE(contact_email, ''), status,
    held_at, payment_expires_at, COALESCE(payment_ref, ''), paid_at, payment_attempts,
    COALESCE(payment_method, ''), payment_disputed, COALESCE(grant_token, ''),
    COALESCE(queue_token, ''), payment_amount, COALESCE(payment_status, ''),
    payment_instructions`

type bookingRow struct {
	b          entity.Booking
	queueToken string
	// instructions is scanned raw because the column is nullable JSONB.
	// Scanning straight into a map would have to tell SQL NULL from an empty
	// object, and those mean different things here: "never charged" versus
	// "charged, but the gateway returned nothing to act on".
	instructions []byte
}

func scanBooking(row pgx.Row) (bookingRow, error) {
	var out bookingRow
	err := row.Scan(
		&out.b.ID, &out.b.EventID, &out.b.AccountID, &out.b.ContactEmail, &out.b.Status,
		&out.b.HeldAt, &out.b.PaymentExpiresAt, &out.b.PaymentRef, &out.b.PaidAt, &out.b.PaymentAttempts,
		&out.b.PaymentMethod, &out.b.PaymentDisputed, &out.b.GrantToken, &out.queueToken,
		&out.b.PaymentAmount, &out.b.PaymentStatus, &out.instructions,
	)
	if err != nil {
		return out, err
	}
	if len(out.instructions) > 0 {
		// Unreadable instructions must not take the whole read down with
		// them: the customer then sees no VA number, which is bad, rather
		// than no booking at all, which is worse.
		if uerr := json.Unmarshal(out.instructions, &out.b.PaymentInstructions); uerr != nil {
			slog.Warn("booking has unreadable payment_instructions, omitting them",
				slog.String("booking_id", out.b.ID), slog.Any("err", uerr))
			out.b.PaymentInstructions = nil
		}
	}
	return out, nil
}

// GetOwned resolves a booking by id AND grant token.
//
// Ownership is in the WHERE clause, not in a later comparison. A valid grant
// proves the bearer holds a slot, not that the slot is this booking's — and
// RB-00912 style ids are trivially enumerable. No match is 404, never 403,
// because 403 would confirm the row exists (ADR-005 §12).
func (r *BookingRepository) GetOwned(ctx context.Context, bookingID, grantToken string) (entity.Booking, error) {
	row := r.db.QueryRow(ctx,
		`SELECT `+bookingColumns+` FROM bookings WHERE id = $1 AND grant_token = $2`,
		bookingID, grantToken)
	got, err := scanBooking(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return entity.Booking{}, portout.ErrNotFound
	}
	if err != nil {
		return entity.Booking{}, wrapDB(err, "could not read booking")
	}
	return got.b, nil
}

// chargeEventID is the synthetic payment_events key for a charge RESPONSE.
//
// payment_events is keyed by gateway_event_id UNIQUE, which exists for
// notifications. The charge response needs storing too, because a replay of
// §5.2 must return byte-identical instructions without calling the gateway
// again. ":charge:" cannot collide with a notification key, whose middle
// segment is always a transaction_status.
func chargeEventID(orderID, statusCode string) string {
	return orderID + ":charge:" + statusCode
}

// storedCharge is what goes into payment_events.raw for a charge.
//
// The already-shaped instructions are stored alongside the gateway body rather
// than re-derived on replay: re-parsing a Midtrans response in two places is
// how a VA number and a QR url end up rendered differently on the first and
// second request.
type storedCharge struct {
	Instructions map[string]string `json:"instructions"`
	Gateway      json.RawMessage   `json:"gateway,omitempty"`
}

// Initiate is §5.2 in one transaction.
func (r *BookingRepository) Initiate(ctx context.Context, in portout.InitiateInput, charge portout.ChargeFunc) (portout.InitiateOutcome, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return portout.InitiateOutcome{}, wrapDB(err, "could not start transaction")
	}
	// Rollback after a successful Commit is a no-op, so this is safe to defer
	// unconditionally and removes every early-return leak.
	defer func() { _ = tx.Rollback(ctx) }()

	got, err := scanBooking(tx.QueryRow(ctx,
		`SELECT `+bookingColumns+` FROM bookings WHERE id = $1 AND grant_token = $2 FOR UPDATE`,
		in.BookingID, in.GrantToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return portout.InitiateOutcome{}, portout.ErrNotFound
	}
	if err != nil {
		return portout.InitiateOutcome{}, wrapDB(err, "could not lock booking")
	}
	b := got.b

	switch b.Status {
	case entity.StatusAwaitingPayment:
		// Replay. No gateway call, no writes, and the response must be
		// identical to the first one — a double-clicked Pay button is not a
		// failure.
		instructions, err := r.replayInstructions(ctx, tx, b.ID, b.PaymentRef)
		if err != nil {
			return portout.InitiateOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return portout.InitiateOutcome{}, wrapDB(err, "could not commit")
		}
		return portout.InitiateOutcome{
			Booking: b, OrderID: b.PaymentRef, Instructions: instructions, Replayed: true,
		}, nil

	case entity.StatusHeld:
		// The only state a charge may start from.

	default:
		// CONFIRMED, EXPIRED, LAPSED, ABANDONED, GRANTED, SELECTING,
		// PENDING_CANCELLATION. 409, with no gateway contact.
		return portout.InitiateOutcome{}, portout.ErrInvalidStatus
	}

	total, err := r.bookingTotal(ctx, tx, b.ID)
	if err != nil {
		return portout.InitiateOutcome{}, err
	}

	// order_id is derived from stored state, never supplied by the client.
	// Core API carries idempotency on order_id and refuses a reused one, so a
	// bare booking_id would be rejected on the second attempt and lock the
	// customer out of paying for their own booking.
	orderID := valueobject.OrderID(b.ID, b.PaymentAttempts)

	b.PaymentMethod = in.Method
	b.Total = total
	res, err := charge(ctx, b, orderID)
	if err != nil {
		// Includes the breaker-open and timeout paths. Roll back with
		// payment_ref still NULL and let the reconciler establish the truth:
		// a timeout is not a failed charge.
		return portout.InitiateOutcome{}, err
	}

	if res.Declined() {
		// A synchronous refusal. Raising payment_attempts HERE is what makes
		// the next charge derive a fresh order_id; without it Midtrans rejects
		// attempt two as a duplicate and the customer cannot retry at all.
		if _, err := tx.Exec(ctx,
			`UPDATE bookings
                SET status = 'HELD', payment_ref = NULL, payment_method = NULL,
                    payment_attempts = payment_attempts + 1
              WHERE id = $1 AND status = 'HELD'`, b.ID); err != nil {
			return portout.InitiateOutcome{}, wrapDB(err, "could not record decline")
		}
		if err := r.insertChargeEvent(ctx, tx, b.ID, orderID, res); err != nil {
			return portout.InitiateOutcome{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return portout.InitiateOutcome{}, wrapDB(err, "could not commit")
		}
		b.PaymentAttempts++
		b.Status = entity.StatusHeld
		return portout.InitiateOutcome{
			Booking: b, OrderID: orderID, Declined: true, DeclineReason: res.StatusMessage,
		}, nil
	}

	// held_at is NOT written here, and neither is payment_expires_at: both
	// were set at HELD. Writing held_at again would re-anchor custom_expiry
	// and hand a fresh 10 minutes to every charge, so BR-05 would never bind.
	// nil rather than an empty object when the gateway returned nothing to act
	// on, so the column keeps "never charged" and "charged, nothing to do"
	// distinguishable. json.Marshal of a nil map gives "null", which pgx
	// writes as SQL NULL for a JSONB parameter - the intent, but spelled out
	// because it is easy to read the wrong way.
	var instructions []byte
	if len(res.Instructions) > 0 {
		var merr error
		if instructions, merr = json.Marshal(res.Instructions); merr != nil {
			// The charge SUCCEEDED. Failing here would roll back a payment the
			// customer has already been committed to at the gateway, over a
			// field that only affects what we can show them afterwards.
			slog.ErrorContext(ctx, "could not encode payment instructions, storing none",
				slog.String("booking_id", b.ID), slog.Any("err", merr))
			instructions = nil
		}
	}

	if _, err := tx.Exec(ctx,
		// payment_amount, payment_status and payment_instructions are written
		// here rather than left to the webhook: the charge succeeded, so this
		// IS the record of what was charged and of what the customer must do
		// to pay it. A notification may never arrive, and neither the receipt
		// nor the VA number may depend on one.
		`UPDATE bookings
            SET status = 'AWAITING_PAYMENT', payment_ref = $2, payment_method = $3,
                payment_amount = $4, payment_status = $5,
                payment_instructions = $6
          WHERE id = $1 AND status = 'HELD'`,
		b.ID, orderID, in.Method, total, res.TransactionStatus, instructions); err != nil {
		return portout.InitiateOutcome{}, wrapDB(err, "could not record charge")
	}
	if err := r.insertChargeEvent(ctx, tx, b.ID, orderID, res); err != nil {
		return portout.InitiateOutcome{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return portout.InitiateOutcome{}, wrapDB(err, "could not commit")
	}

	b.Status = entity.StatusAwaitingPayment
	b.PaymentRef = orderID
	return portout.InitiateOutcome{Booking: b, OrderID: orderID, Instructions: res.Instructions}, nil
}

// bookingTotal sums the seats. booking_items is the authority on price,
// because unit_price is copied at purchase and must not follow a later
// catalogue change.
func (r *BookingRepository) bookingTotal(ctx context.Context, tx pgx.Tx, bookingID string) (int64, error) {
	var total int64
	err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(unit_price), 0) FROM booking_items WHERE booking_id = $1`,
		bookingID).Scan(&total)
	if err != nil {
		return 0, wrapDB(err, "could not total booking")
	}
	if total <= 0 {
		// A booking with no seats cannot be charged. Treated as a status
		// problem rather than an internal error: it means the hold never
		// completed.
		return 0, portout.ErrInvalidStatus
	}
	return total, nil
}

func (r *BookingRepository) insertChargeEvent(ctx context.Context, tx pgx.Tx, bookingID, orderID string, res portout.ChargeResult) error {
	payload, err := json.Marshal(storedCharge{Instructions: res.Instructions, Gateway: res.Raw})
	if err != nil {
		return wrapDB(err, "could not encode charge")
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO payment_events
            (id, booking_id, order_id, gateway_event_id, transaction_status, fraud_status,
             payment_type, gross_amount, raw)
          VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), 0, $8)
          ON CONFLICT (gateway_event_id) DO NOTHING`,
		r.newID(), bookingID, orderID, chargeEventID(orderID, res.StatusCode),
		res.TransactionStatus, res.FraudStatus, "", payload)
	if err != nil {
		return wrapDB(err, "could not record charge event")
	}
	return nil
}

// replayInstructions reads back the first charge's instructions.
func (r *BookingRepository) replayInstructions(ctx context.Context, tx pgx.Tx, bookingID, orderID string) (map[string]string, error) {
	var raw []byte
	err := tx.QueryRow(ctx,
		`SELECT raw FROM payment_events
          WHERE booking_id = $1 AND gateway_event_id LIKE $2
          ORDER BY received_at ASC LIMIT 1`,
		bookingID, orderID+":charge:%").Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		// AWAITING_PAYMENT with no stored charge means the row was written by
		// something other than this service. Empty instructions beat a 500:
		// the client still has the payment_id and can poll §5.4.
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, wrapDB(err, "could not replay charge")
	}
	var stored storedCharge
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Instructions == nil {
		return map[string]string{}, nil
	}
	return stored.Instructions, nil
}

// Retry is §5.3 in one transaction.
func (r *BookingRepository) Retry(ctx context.Context, in portout.RetryInput, cancel portout.CancelFunc) (portout.RetryOutcome, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return portout.RetryOutcome{}, wrapDB(err, "could not start transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	got, err := scanBooking(tx.QueryRow(ctx,
		`SELECT `+bookingColumns+` FROM bookings WHERE id = $1 AND grant_token = $2 FOR UPDATE`,
		in.BookingID, in.GrantToken))
	if errors.Is(err, pgx.ErrNoRows) {
		return portout.RetryOutcome{}, portout.ErrNotFound
	}
	if err != nil {
		return portout.RetryOutcome{}, wrapDB(err, "could not lock booking")
	}
	b := got.b

	// Only a live, pending payment can be cancelled. HELD after a decline is
	// explicitly excluded: there is nothing at the gateway to void, and the
	// attempt counter has already moved, so §5.2 is the call.
	if b.Status != entity.StatusAwaitingPayment {
		return portout.RetryOutcome{}, portout.ErrInvalidStatus
	}
	if b.PaymentAttempts+1 > in.MaxAttempts {
		return portout.RetryOutcome{}, portout.ErrMaxAttempts
	}
	// Refuse a switch that would issue an instrument with almost no time left.
	// The window is never extended, so the remainder only shrinks.
	if b.RemainingPaymentWindow(in.Now) < in.MinRemaining {
		return portout.RetryOutcome{}, portout.ErrWindowClosed
	}

	cancelled := b.PaymentRef
	// Cancel at the gateway BEFORE committing. Reversed, a failed cancel would
	// leave the customer with a raised attempt AND a still-payable VA — two
	// live instruments for one booking.
	if err := cancel(ctx, cancelled); err != nil {
		if errors.Is(err, portout.ErrAlreadySettled) {
			return portout.RetryOutcome{}, portout.ErrAlreadySettled
		}
		// payment_attempts is NOT raised here: the old instrument is still
		// valid and the customer may simply pay it.
		return portout.RetryOutcome{}, portout.ErrGatewayUnavailable
	}

	if _, err := tx.Exec(ctx,
		`UPDATE bookings
            SET status = 'HELD', payment_ref = NULL, payment_method = NULL,
                payment_attempts = payment_attempts + 1
          WHERE id = $1 AND status = 'AWAITING_PAYMENT'`, b.ID); err != nil {
		return portout.RetryOutcome{}, wrapDB(err, "could not record cancellation")
	}
	if err := tx.Commit(ctx); err != nil {
		return portout.RetryOutcome{}, wrapDB(err, "could not commit")
	}

	b.Status = entity.StatusHeld
	b.PaymentAttempts++
	b.PaymentRef = ""
	return portout.RetryOutcome{Booking: b, CancelledOrderID: cancelled, Attempt: b.PaymentAttempts}, nil
}

// ApplyNotification is §5.5 in one transaction.
func (r *BookingRepository) ApplyNotification(ctx context.Context, in portout.NotificationInput) (portout.NotificationOutcome, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return portout.NotificationOutcome{}, wrapDB(err, "could not start transaction")
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// The idempotency row goes in FIRST, so a redelivery aborts before it can
	// touch a status. The UNIQUE on gateway_event_id is the guard; the
	// rollback is the whole point.
	if _, err := tx.Exec(ctx,
		`INSERT INTO payment_events
            (id, booking_id, order_id, gateway_event_id, transaction_status, fraud_status,
             payment_type, gross_amount, raw)
          VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''), $8, $9)`,
		r.newID(), in.BookingID, in.OrderID, in.GatewayEventID, in.TransactionStatus,
		in.FraudStatus, in.PaymentType, in.GrossAmount, in.Raw); err != nil {
		if isUniqueViolation(err) {
			return portout.NotificationOutcome{}, portout.ErrDuplicateEvent
		}
		// A foreign key failure lands here too: a notification for an
		// order_id whose booking does not exist.
		return portout.NotificationOutcome{}, portout.ErrNotFound
	}

	got, err := scanBooking(tx.QueryRow(ctx,
		`SELECT `+bookingColumns+` FROM bookings WHERE id = $1 FOR UPDATE`, in.BookingID))
	if errors.Is(err, pgx.ErrNoRows) {
		return portout.NotificationOutcome{}, portout.ErrNotFound
	}
	if err != nil {
		return portout.NotificationOutcome{}, wrapDB(err, "could not lock booking")
	}
	b := got.b

	action := valueobject.Decide(in.TransactionStatus, in.FraudStatus, b.Status)

	// Locked by id rather than by payment_ref, because payment_ref is cleared
	// on a decline and a later notification for that same attempt would then
	// match nothing. The trade is that a stale attempt could be applied to a
	// live one, so transitions are gated on the notification describing the
	// CURRENT instrument. The late-settlement path is exempt: there, by
	// definition, no instrument is live any more.
	if action != valueobject.ActionRecordOnly && action != valueobject.ActionLateSettlement {
		if b.PaymentRef != "" && b.PaymentRef != in.OrderID {
			action = valueobject.ActionRecordOnly
		}
	}

	out := portout.NotificationOutcome{
		Action: action, Booking: b, EventID: b.EventID,
		QueueToken: got.queueToken, AccountID: b.AccountID,
	}

	switch action {
	case valueobject.ActionConfirm:
		// PENDING_CANCELLATION is accepted alongside AWAITING_PAYMENT: letting
		// a callback land during the 30-second grace is the entire purpose of
		// having a grace.
		// paid_at comes from the gateway's settlement_time, not from now():
		// the notification can arrive minutes after the money moved, and it is
		// the money the receipt is about. COALESCE covers a notification that
		// carried no usable timestamp — a CONFIRMED row must not be left with
		// a NULL payment time, because §5.4 has to answer paid_at with
		// something and the alternative is faking it from the deadline again.
		//
		// Set only on the transition. A later notification for the same
		// booking takes the ActionRecordOnly path and never reaches here, so
		// the first settlement's time is the one that stands.
		tag, err := tx.Exec(ctx,
			`UPDATE bookings SET status = 'CONFIRMED', paid_at = COALESCE($2, now())
              WHERE id = $1 AND status IN ('AWAITING_PAYMENT', 'PENDING_CANCELLATION')`,
			b.ID, in.PaidAt)
		if err != nil {
			return out, wrapDB(err, "could not confirm booking")
		}
		if tag.RowsAffected() == 0 {
			// Raced to a terminal state between the lock and here. Recorded,
			// not forced.
			out.Action = valueobject.ActionRecordOnly
			break
		}
		tickets, err := r.issueTickets(ctx, tx, b)
		if err != nil {
			return out, err
		}
		if err := r.enqueueTicketEmails(ctx, tx, b, tickets); err != nil {
			return out, err
		}
		out.Applied = true
		out.Booking.Status = entity.StatusConfirmed

	case valueobject.ActionLateSettlement:
		// Money for seats that were already resold. No transition: the
		// booking stays EXPIRED. What changes is that an operator can find it.
		if _, err := tx.Exec(ctx,
			`UPDATE bookings SET payment_disputed = true WHERE id = $1`, b.ID); err != nil {
			return out, wrapDB(err, "could not mark disputed")
		}
		if err := r.enqueueFailureNotice(ctx, tx, b, in.OrderID, in.GrossAmount, events.FailureDisputed); err != nil {
			return out, err
		}
		out.Applied = true

	case valueobject.ActionDecline:
		// Back to HELD, not EXPIRED: the payment failed, the claim did not.
		// The seats stay with the customer until the TTL.
		tag, err := tx.Exec(ctx,
			`UPDATE bookings
                SET status = 'HELD', payment_ref = NULL, payment_method = NULL,
                    payment_attempts = payment_attempts + 1
              WHERE id = $1 AND status = 'AWAITING_PAYMENT'`, b.ID)
		if err != nil {
			return out, wrapDB(err, "could not record decline")
		}
		if tag.RowsAffected() == 0 {
			out.Action = valueobject.ActionRecordOnly
			break
		}
		if b.PaymentAttempts+1 >= in.MaxAttempts {
			// Out of attempts. Tell the customer, rather than letting them
			// discover it by pressing Pay again.
			if err := r.enqueueFailureNotice(ctx, tx, b, in.OrderID, in.GrossAmount, events.FailureGatewayDeclined); err != nil {
				return out, err
			}
		}
		out.Applied = true
		out.Booking.Status = entity.StatusHeld
		out.Booking.PaymentAttempts = b.PaymentAttempts + 1

	case valueobject.ActionExpire:
		tag, err := tx.Exec(ctx,
			`UPDATE bookings SET status = 'EXPIRED'
              WHERE id = $1 AND status IN ('AWAITING_PAYMENT', 'PENDING_CANCELLATION')`, b.ID)
		if err != nil {
			return out, wrapDB(err, "could not expire booking")
		}
		if tag.RowsAffected() == 0 {
			out.Action = valueobject.ActionRecordOnly
			break
		}
		// Capture the seats and NIKs BEFORE deleting. The caller needs them
		// for the Redis release that follows the commit; deleting first would
		// lose the data.
		seats, err := r.countSeatsByCategory(ctx, tx, b.ID)
		if err != nil {
			return out, err
		}
		niks, err := r.deleteNIKClaimsReturning(ctx, tx, b.ID)
		if err != nil {
			return out, err
		}
		if err := r.enqueueExpiredNotice(ctx, tx, b); err != nil {
			return out, err
		}
		out.Applied = true
		out.Booking.Status = entity.StatusExpired
		out.Seats = seats
		out.NIKs = niks

	case valueobject.ActionIgnoreUnfunded, valueobject.ActionRecordOnly:
		// Recorded above; nothing else to do.
	}

	// The gateway's view of the money (0017), recorded only for a notification
	// we ACTED on.
	//
	// This was originally written for every notification, on the reasoning
	// that one recorded but not applied still said something true. It does
	// not. ActionIgnoreUnfunded is `capture` whose fraud_status is not
	// `accept` — authorised but NOT funded — and storing payment_status
	// 'capture' for it would read as money received, which is the exact
	// misreading that action exists to prevent. ActionRecordOnly has the same
	// problem from the other end: a late notification on a booking that is
	// already settled or terminal would overwrite the status we acted on with
	// one we deliberately ignored.
	//
	// out.Applied is the right condition rather than the action itself,
	// because a branch whose UPDATE matched no row downgrades itself to
	// ActionRecordOnly without setting it. The field therefore means "the
	// gateway status behind the booking's current state", which is what makes
	// an AWAITING_PAYMENT booking showing 'deny' worth seeing: the decline was
	// applied, and the customer may retry.
	//
	// Every other case still reaches payment_events in full, which is where
	// the complete sequence belongs.
	if out.Applied {
		if _, err := tx.Exec(ctx,
			`UPDATE bookings SET payment_status = $2 WHERE id = $1`,
			b.ID, in.TransactionStatus); err != nil {
			return out, wrapDB(err, "could not record gateway payment status")
		}
		out.Booking.PaymentStatus = in.TransactionStatus
	}

	if err := tx.Commit(ctx); err != nil {
		return out, wrapDB(err, "could not commit")
	}
	return out, nil
}

// countSeatsByCategory returns the per-category seat count of a booking.
// Used by ActionExpire to capture the data needed for the Redis release.
func (r *BookingRepository) countSeatsByCategory(ctx context.Context, tx pgx.Tx, bookingID string) ([]portout.SeatCount, error) {
	rows, err := tx.Query(ctx,
		`SELECT category_id, count(*) FROM booking_items WHERE booking_id = $1 GROUP BY category_id`,
		bookingID)
	if err != nil {
		return nil, wrapDB(err, "could not count seats")
	}
	defer rows.Close()
	var seats []portout.SeatCount
	for rows.Next() {
		var s portout.SeatCount
		if err := rows.Scan(&s.CategoryID, &s.Qty); err != nil {
			return nil, wrapDB(err, "could not scan seat count")
		}
		seats = append(seats, s)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDB(err, "could not iterate seat counts")
	}
	return seats, nil
}

// deleteNIKClaimsReturning deletes the NIK claims for a booking and returns
// the nik_hmac values that were deleted. Without this the NIK stays locked
// for the event even though its booking is dead, and BR-02 would block the
// customer from ever buying again. The returned hashes are needed for the
// Redis release that follows the commit.
func (r *BookingRepository) deleteNIKClaimsReturning(ctx context.Context, tx pgx.Tx, bookingID string) ([]string, error) {
	rows, err := tx.Query(ctx,
		`DELETE FROM nik_claims WHERE booking_id = $1 RETURNING nik_hmac`,
		bookingID)
	if err != nil {
		return nil, wrapDB(err, "could not release NIK claims")
	}
	defer rows.Close()
	var niks []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, wrapDB(err, "could not scan NIK hash")
		}
		niks = append(niks, h)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDB(err, "could not iterate NIK claims")
	}
	return niks, nil
}

// DueAwaitingPayment returns AWAITING_PAYMENT bookings whose payment window
// plus grace has elapsed. The query uses the partial index on
// payment_expires_at WHERE status = 'AWAITING_PAYMENT' (migration 0019).
//
// Bookings with an empty payment_ref are included: they represent a charge
// that timed out before Midtrans returned an order_id. The caller skips them
// (there is no order to poll) and counts them for metrics; the charge
// reconciler is the one that resolves them.
func (r *BookingRepository) DueAwaitingPayment(ctx context.Context, now time.Time, graceSeconds, limit int) ([]portout.AwaitingCandidate, error) {
	// The deadline is now minus the grace window. A booking whose
	// payment_expires_at is before this cutoff is a candidate.
	cutoff := now.Add(-time.Duration(graceSeconds) * time.Second)

	rows, err := r.db.Query(ctx,
		`SELECT id, COALESCE(payment_ref, ''), payment_expires_at
		   FROM bookings
		  WHERE status = 'AWAITING_PAYMENT'
		    AND payment_expires_at < $1
		  ORDER BY payment_expires_at
		  LIMIT $2`,
		cutoff, limit)
	if err != nil {
		return nil, wrapDB(err, "could not query due bookings")
	}
	defer rows.Close()

	var out []portout.AwaitingCandidate
	for rows.Next() {
		var c portout.AwaitingCandidate
		if err := rows.Scan(&c.ID, &c.PaymentRef, &c.PaymentExpiresAt); err != nil {
			return nil, wrapDB(err, "could not scan candidate")
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, wrapDB(err, "could not iterate candidates")
	}
	return out, nil
}
