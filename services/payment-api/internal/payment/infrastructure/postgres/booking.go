package postgres

import (
	"context"
	"encoding/json"
	"errors"
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
    held_at, payment_expires_at, COALESCE(payment_ref, ''), payment_attempts,
    COALESCE(payment_method, ''), payment_disputed, COALESCE(grant_token, ''),
    COALESCE(queue_token, '')`

type bookingRow struct {
	b          entity.Booking
	queueToken string
}

func scanBooking(row pgx.Row) (bookingRow, error) {
	var out bookingRow
	err := row.Scan(
		&out.b.ID, &out.b.EventID, &out.b.AccountID, &out.b.ContactEmail, &out.b.Status,
		&out.b.HeldAt, &out.b.PaymentExpiresAt, &out.b.PaymentRef, &out.b.PaymentAttempts,
		&out.b.PaymentMethod, &out.b.PaymentDisputed, &out.b.GrantToken, &out.queueToken,
	)
	return out, err
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
	if _, err := tx.Exec(ctx,
		`UPDATE bookings
            SET status = 'AWAITING_PAYMENT', payment_ref = $2, payment_method = $3
          WHERE id = $1 AND status = 'HELD'`, b.ID, orderID, in.Method); err != nil {
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
            (id, booking_id, gateway_event_id, transaction_status, fraud_status,
             payment_type, gross_amount, raw)
          VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), NULL, $7)
          ON CONFLICT (gateway_event_id) DO NOTHING`,
		r.newID(), bookingID, chargeEventID(orderID, res.StatusCode),
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
            (id, booking_id, gateway_event_id, transaction_status, fraud_status,
             payment_type, gross_amount, raw)
          VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), $7, $8)`,
		r.newID(), in.BookingID, in.GatewayEventID, in.TransactionStatus,
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
		tag, err := tx.Exec(ctx,
			`UPDATE bookings SET status = 'CONFIRMED'
              WHERE id = $1 AND status IN ('AWAITING_PAYMENT', 'PENDING_CANCELLATION')`, b.ID)
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
		// Free the NIKs. Without this the NIK stays locked for the event even
		// though its booking is dead, and BR-02 would block the customer from
		// ever buying again.
		if _, err := tx.Exec(ctx,
			`DELETE FROM nik_claims WHERE booking_id = $1`, b.ID); err != nil {
			return out, wrapDB(err, "could not release NIK claims")
		}
		if err := r.enqueueExpiredNotice(ctx, tx, b); err != nil {
			return out, err
		}
		out.Applied = true
		out.Booking.Status = entity.StatusExpired

	case valueobject.ActionIgnoreUnfunded, valueobject.ActionRecordOnly:
		// Recorded above; nothing else to do.
	}

	if err := tx.Commit(ctx); err != nil {
		return out, wrapDB(err, "could not commit")
	}
	return out, nil
}
