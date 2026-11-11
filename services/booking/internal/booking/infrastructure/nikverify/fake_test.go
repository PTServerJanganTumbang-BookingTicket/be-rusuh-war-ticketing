package nikverify

import (
	"context"
	"errors"
	"testing"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
)

// The local stand-in follows nik-verify's Dukcapil stub (0000 rejected, 9999 outage), so every outcome of POST
// /bookings is reachable on a laptop.
func TestTheLocalFakeAnswersLikeTheDukcapilStub(t *testing.T) {
	f := NewFake()
	ok, err := f.Validate(context.Background(), "3171000000000022")
	if err != nil || !ok.Valid || ok.Status != "ACTIVE" || ok.NIKHmac == "" || ok.NIKMasked != "3171**********22" {
		t.Fatalf("check = %+v (%v)", ok, err)
	}
	bad, err := f.Validate(context.Background(), "3171000000000000")
	if err != nil || bad.Valid || bad.Status != "NOT_FOUND" {
		t.Fatalf("check = %+v (%v), want a rejected NIK", bad, err)
	}
	if _, err := f.Validate(context.Background(), "3171000000009999"); !errors.Is(err, domain.ErrNIKUnavailable) {
		t.Fatalf("err = %v, want the simulated outage", err)
	}
}

func TestTheFakeGivesTheSameHashForTheSameNIKHoweverItIsTyped(t *testing.T) {
	f := NewFake()
	a, _ := f.Validate(context.Background(), "3171 0000 0000 0022")
	b, _ := f.Validate(context.Background(), "3171000000000022")
	c, _ := f.Validate(context.Background(), "3171000000000033")
	if a.NIKHmac != b.NIKHmac || a.NIKHmac == c.NIKHmac {
		t.Fatalf("hashes %q %q %q: same NIK must match, another NIK must differ", a.NIKHmac, b.NIKHmac, c.NIKHmac)
	}
}
