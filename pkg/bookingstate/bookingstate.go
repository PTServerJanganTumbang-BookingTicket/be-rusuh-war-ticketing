// Package bookingstate is the one definition of a booking's status (the bookings_status CHECK in migrations/0004)
// and of what each status means to the frontend on GET /bookings/{id} (docs/05-booking/03-get-booking-detail.md).
package bookingstate

import (
	"errors"
	"fmt"
)

// Status is bookings.status.
type Status string

// The nine statuses of the bookings_status CHECK constraint.
const (
	Granted             Status = "GRANTED"
	Selecting           Status = "SELECTING"
	Held                Status = "HELD"
	AwaitingPayment     Status = "AWAITING_PAYMENT"
	PendingCancellation Status = "PENDING_CANCELLATION"
	Confirmed           Status = "CONFIRMED"
	Expired             Status = "EXPIRED"
	Lapsed              Status = "LAPSED"
	Abandoned           Status = "ABANDONED"
)

// All lists every status, in the order of the CHECK constraint.
var All = []Status{Granted, Selecting, Held, AwaitingPayment, PendingCancellation, Confirmed, Expired, Lapsed, Abandoned}

// data.status values of GET /bookings/{id} (api-contracts §4.4).
const (
	DetailSelecting      = "SELECTING"
	DetailPendingPayment = "PENDING_PAYMENT"
	DetailPaid           = "PAID"
	DetailExpired        = "EXPIRED"
)

// ErrUnknownStatus is returned by Parse for a value outside the CHECK constraint.
var ErrUnknownStatus = errors.New("bookingstate: unknown status")

var detail = map[Status]string{
	Granted:             DetailSelecting,
	Selecting:           DetailSelecting,
	Held:                DetailPendingPayment,
	AwaitingPayment:     DetailPendingPayment,
	PendingCancellation: DetailPendingPayment, // the payment may still arrive during the 30 s grace
	Confirmed:           DetailPaid,
	Expired:             DetailExpired,
	Lapsed:              DetailExpired,
	Abandoned:           DetailExpired,
}

// Parse turns a stored value into a Status. An unknown value is an error, never a default.
func Parse(s string) (Status, error) {
	if _, ok := detail[Status(s)]; !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownStatus, s)
	}
	return Status(s), nil
}

// IsTerminal reports whether the booking is finished: CONFIRMED, EXPIRED, LAPSED, ABANDONED.
func (s Status) IsTerminal() bool {
	switch s {
	case Confirmed, Expired, Lapsed, Abandoned:
		return true
	}
	return false
}

// DetailStatus maps the stored status to data.status of GET /bookings/{id}. Empty for an unknown status.
func (s Status) DetailStatus() string { return detail[s] }
