package mapper

import (
	"testing"
	"time"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/entity"
)

func TestMaskNIK(t *testing.T) {
	tests := []struct {
		name string
		nik  string
		want string
	}{
		{name: "valid 16 digit", nik: "3171000000000022", want: "3171********xx22"},
		{name: "another valid", nik: "3271123456789001", want: "3271********xx01"},
		{name: "too short", nik: "12345", want: "****************"},
		{name: "empty", nik: "", want: "****************"},
		{name: "too long", nik: "31710000000000221", want: "****************"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maskNIK(tt.nik); got != tt.want {
				t.Errorf("maskNIK(%q) = %q, want %q", tt.nik, got, tt.want)
			}
		})
	}
}

func TestToBookingResponse(t *testing.T) {
	serverTime := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	expiresAt := serverTime.Add(600 * time.Second)

	b := entity.Booking{
		ID:     "RB-abc123",
		Status: entity.StatusPendingPayment,
		Event: entity.Event{
			ID:    "evt_rusuh_2026",
			Name:  "Rusuh Fest 2026",
			Date:  "2026-12-01",
			Venue: "GBK",
		},
		Tickets: []entity.Ticket{
			{CategoryName: "VIP", Price: 1500000, NIK: "3171000000000022"},
			{CategoryName: "Tribune", Price: 500000, NIK: "short"},
		},
		Subtotal:   2000000,
		ServiceFee: 20000,
		Total:      2020000,
		ExpiresAt:  expiresAt,
	}

	got := ToBookingResponse(b, serverTime)

	if got.BookingID != "RB-abc123" {
		t.Errorf("BookingID = %q, want RB-abc123", got.BookingID)
	}
	if got.Status != "PENDING_PAYMENT" {
		t.Errorf("Status = %q, want PENDING_PAYMENT", got.Status)
	}
	if got.Event.ID != "evt_rusuh_2026" || got.Event.Venue != "GBK" {
		t.Errorf("Event mapped incorrectly: %+v", got.Event)
	}
	if len(got.Tickets) != 2 {
		t.Fatalf("Tickets len = %d, want 2", len(got.Tickets))
	}
	if got.Tickets[0].NIKMasked != "3171********xx22" {
		t.Errorf("Tickets[0].NIKMasked = %q, want 3171********xx22", got.Tickets[0].NIKMasked)
	}
	if got.Tickets[1].NIKMasked != "****************" {
		t.Errorf("Tickets[1].NIKMasked = %q, want all-masked for invalid NIK", got.Tickets[1].NIKMasked)
	}
	if got.Summary.Total != 2020000 {
		t.Errorf("Summary.Total = %d, want 2020000", got.Summary.Total)
	}
	if got.PaymentTTL.TTLSeconds != 600 {
		t.Errorf("TTLSeconds = %d, want 600 (recomputed from serverTime)", got.PaymentTTL.TTLSeconds)
	}
	if got.PaymentTTL.ExpiresAt != "2026-10-31T10:10:00Z" {
		t.Errorf("ExpiresAt = %q, want 2026-10-31T10:10:00Z", got.PaymentTTL.ExpiresAt)
	}
	if got.RedirectURL != "/bookings/RB-abc123/payment" {
		t.Errorf("RedirectURL = %q", got.RedirectURL)
	}
}

func TestToBookingResponse_noTickets(t *testing.T) {
	serverTime := time.Date(2026, 10, 31, 10, 0, 0, 0, time.UTC)
	got := ToBookingResponse(entity.Booking{ID: "RB-x", ExpiresAt: serverTime}, serverTime)
	if len(got.Tickets) != 0 {
		t.Errorf("Tickets len = %d, want 0", len(got.Tickets))
	}
	if got.PaymentTTL.TTLSeconds != 0 {
		t.Errorf("TTLSeconds = %d, want 0", got.PaymentTTL.TTLSeconds)
	}
}
