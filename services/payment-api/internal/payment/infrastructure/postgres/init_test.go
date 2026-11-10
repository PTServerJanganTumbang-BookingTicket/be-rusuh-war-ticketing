package postgres

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
)

// 23505 on payment_events means the gateway_event_id was already stored - a
// redelivery, not a failure. Anything else is a real error.
func TestIsUniqueViolation(t *testing.T) {
	assert.True(t, isUniqueViolation(&pgconn.PgError{Code: "23505"}))
	// Wrapped, because the repository sees the error through fmt.Errorf and
	// pgx's own wrapping.
	assert.True(t, isUniqueViolation(fmt.Errorf("insert: %w", &pgconn.PgError{Code: "23505"})))

	// A foreign-key violation is NOT a duplicate: it means the booking does
	// not exist, which answers 404 rather than "already applied".
	assert.False(t, isUniqueViolation(&pgconn.PgError{Code: "23503"}))
	assert.False(t, isUniqueViolation(errors.New("connection reset")))
	assert.False(t, isUniqueViolation(nil))
}

// A driver error must never reach the client verbatim: a SQL state or a table
// name tells an attacker more than it tells the customer.
func TestWrapDB_HidesDriverDetail(t *testing.T) {
	err := wrapDB(errors.New(`pq: relation "bookings" does not exist`), "could not read booking")
	require.Error(t, err)

	var appErr *apperror.AppError
	require.True(t, errors.As(err, &appErr))
	assert.Equal(t, 500, appErr.HTTPStatus)
	assert.Equal(t, "could not read booking", appErr.Message)
	assert.NotContains(t, appErr.Message, "bookings")
	assert.NotContains(t, appErr.Message, "pq:")
}

func TestWrapDB_NilStaysNil(t *testing.T) {
	assert.NoError(t, wrapDB(nil, "anything"))
}

// The middle segment of a notification key is always a transaction_status, so
// ":charge:" cannot collide with one.
func TestChargeEventID_CannotCollideWithANotification(t *testing.T) {
	assert.Equal(t, "RB-00912:1:charge:201", chargeEventID("RB-00912:1", "201"))
	assert.NotEqual(t,
		chargeEventID("RB-00912:1", "200"),
		"RB-00912:1:settlement:200",
	)
	// Different status codes are different stored charges, so a decline and a
	// success for one attempt both survive.
	assert.NotEqual(t, chargeEventID("RB-00912:1", "201"), chargeEventID("RB-00912:1", "402"))
}
