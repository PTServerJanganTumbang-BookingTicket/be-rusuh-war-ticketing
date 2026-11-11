package entity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBookingStatus_IsTerminal(t *testing.T) {
	terminal := []BookingStatus{StatusConfirmed, StatusExpired, StatusLapsed, StatusAbandoned}
	live := []BookingStatus{
		StatusGranted, StatusSelecting, StatusHeld,
		StatusAwaitingPayment, StatusPendingCancellation,
	}
	for _, s := range terminal {
		assert.True(t, s.IsTerminal(), string(s))
	}
	for _, s := range live {
		assert.False(t, s.IsTerminal(), string(s))
	}
}

// An unrecognised status must not read as terminal: treating an unknown value
// as "nothing more can happen" would silently drop a real notification.
func TestBookingStatus_UnknownIsNotTerminal(t *testing.T) {
	assert.False(t, BookingStatus("WHATEVER").IsTerminal())
}

func TestRemainingPaymentWindow(t *testing.T) {
	now := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)

	t.Run("time left", func(t *testing.T) {
		exp := now.Add(4 * time.Minute)
		b := Booking{PaymentExpiresAt: &exp}
		assert.Equal(t, 4*time.Minute, b.RemainingPaymentWindow(now))
	})

	// Never negative: the result is rendered as a countdown, and "-12s" would
	// reach a customer's screen.
	t.Run("already past", func(t *testing.T) {
		exp := now.Add(-1 * time.Second)
		b := Booking{PaymentExpiresAt: &exp}
		assert.Equal(t, time.Duration(0), b.RemainingPaymentWindow(now))
	})

	t.Run("exactly now", func(t *testing.T) {
		b := Booking{PaymentExpiresAt: &now}
		assert.Equal(t, time.Duration(0), b.RemainingPaymentWindow(now))
	})

	// No deadline means no window, not an unlimited one: a booking with a nil
	// payment_expires_at has not been charged yet.
	t.Run("no deadline", func(t *testing.T) {
		assert.Equal(t, time.Duration(0), Booking{}.RemainingPaymentWindow(now))
	})
}

// The deadline is stored, not recomputed: a customer who switches method at
// minute 8 of a 10-minute window gets the 2 minutes that remain, not a fresh
// 10 (BR-05).
func TestRemainingPaymentWindow_DoesNotRefresh(t *testing.T) {
	held := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	exp := held.Add(10 * time.Minute)
	b := Booking{HeldAt: &held, PaymentExpiresAt: &exp}
	assert.Equal(t, 2*time.Minute, b.RemainingPaymentWindow(held.Add(8*time.Minute)))
}
