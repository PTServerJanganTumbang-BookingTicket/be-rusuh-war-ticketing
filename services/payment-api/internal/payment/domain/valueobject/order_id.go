package valueobject

import (
	"errors"
	"strconv"
	"strings"
)

// OrderID is the idempotency key Midtrans Core API enforces.
//
//	order_id = {booking_id}:{payment_attempts}
//
// Core API carries idempotency on order_id itself, not on a separate header,
// and rejects an order_id it has already seen. That is why the key is derived
// server-side and never supplied by the client: a double-clicked "Pay" button
// produces the same order_id, so the second charge is refused by Midtrans
// rather than creating a second instrument (api-contracts §5.2).
//
// The attempt number is what makes a *new* charge possible after a decline.
// The deny path raises payment_attempts, so the next derived order_id is fresh
// and Midtrans accepts it — which is why a declined customer calls §5.2 again
// rather than §5.3.
func OrderID(bookingID string, attempt int) string {
	return bookingID + ":" + strconv.Itoa(attempt)
}

// ErrMalformedOrderID is returned for an order_id that is not {booking_id}:{n}.
var ErrMalformedOrderID = errors.New("payment: malformed order_id")

// ParseOrderID splits an order_id from a webhook back into its parts.
//
// SplitN with a limit of 2 from the right is deliberate: booking ids are
// RB-00912 today and nothing guarantees a colon never appears in a future id
// format, whereas the attempt suffix is always the final segment.
func ParseOrderID(orderID string) (bookingID string, attempt int, err error) {
	i := strings.LastIndex(orderID, ":")
	if i <= 0 || i == len(orderID)-1 {
		return "", 0, ErrMalformedOrderID
	}
	n, convErr := strconv.Atoi(orderID[i+1:])
	if convErr != nil || n < 0 {
		return "", 0, ErrMalformedOrderID
	}
	return orderID[:i], n, nil
}
