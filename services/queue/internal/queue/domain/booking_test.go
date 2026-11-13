package domain

import "testing"

func TestABookingHasEndedOnlyWhenItCanNoLongerBeResumed(t *testing.T) {
	for status, want := range map[string]bool{
		"LAPSED": true, "EXPIRED": true, "ABANDONED": true, "CONFIRMED": true,
		"GRANTED": false, "SELECTING": false, "HELD": false, "AWAITING_PAYMENT": false, "PENDING_CANCELLATION": false,
		"": false,
	} {
		if got := (Booking{Status: status}).Ended(); got != want {
			t.Errorf("Booking{%q}.Ended() = %v, want %v", status, got, want)
		}
	}
}
