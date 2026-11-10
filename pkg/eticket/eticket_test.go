package eticket_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/eticket"
)

var (
	now = time.Date(2026, 11, 10, 19, 20, 0, 0, time.UTC)
	exp = time.Date(2026, 12, 25, 17, 0, 0, 0, time.UTC)
)

func signer(t *testing.T, key string) *eticket.Signer {
	t.Helper()
	s, err := eticket.NewSigner([]byte(key))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mint(t *testing.T, s *eticket.Signer, ticket string, at time.Time) string {
	t.Helper()
	tok, err := s.Mint(ticket, at)
	if err != nil || tok == "" {
		t.Fatalf("Mint = %q, %v", tok, err)
	}
	return tok
}

func TestAMintedTokenVerifiesForItsTicket(t *testing.T) {
	s := signer(t, "k1")
	if err := s.Verify(mint(t, s, "TKT-001", exp), "TKT-001", now); err != nil {
		t.Fatalf("Verify = %v", err)
	}
}

func TestATokenForTicketAIsRejectedOnTicketB(t *testing.T) {
	s := signer(t, "k1")
	if err := s.Verify(mint(t, s, "TKT-001", exp), "TKT-002", now); !errors.Is(err, eticket.ErrInvalid) {
		t.Fatalf("Verify on another ticket = %v, want ErrInvalid", err)
	}
}

func TestAnExpiredTokenIsRejected(t *testing.T) {
	s := signer(t, "k1")
	tok := mint(t, s, "TKT-001", now.Add(-time.Second))
	if err := s.Verify(tok, "TKT-001", now); !errors.Is(err, eticket.ErrInvalid) {
		t.Fatalf("Verify after expiry = %v, want ErrInvalid", err)
	}
}

func TestATokenFromAnotherKeyIsRejected(t *testing.T) {
	tok := mint(t, signer(t, "k1"), "TKT-001", exp)
	if err := signer(t, "k2").Verify(tok, "TKT-001", now); !errors.Is(err, eticket.ErrInvalid) {
		t.Fatalf("Verify with another key = %v, want ErrInvalid", err)
	}
}

func TestAChangedExpiryIsRejected(t *testing.T) {
	s := signer(t, "k1")
	tok := mint(t, s, "TKT-001", now.Add(time.Minute))
	later := mint(t, s, "TKT-001", exp)
	// Graft the later expiry onto the first token's MAC: the expiry is inside the MAC, so this must fail.
	forged := strings.SplitN(later, ".", 2)[0] + "." + strings.SplitN(tok, ".", 2)[1]
	if err := s.Verify(forged, "TKT-001", now); !errors.Is(err, eticket.ErrInvalid) {
		t.Fatalf("Verify with a changed exp = %v, want ErrInvalid", err)
	}
}

func TestOneFlippedMACBitIsRejected(t *testing.T) {
	s := signer(t, "k1")
	tok := []byte(mint(t, s, "TKT-001", exp))
	last := len(tok) - 1
	if tok[last] == 'A' {
		tok[last] = 'B'
	} else {
		tok[last] = 'A'
	}
	if err := s.Verify(string(tok), "TKT-001", now); !errors.Is(err, eticket.ErrInvalid) {
		t.Fatalf("Verify with a changed MAC = %v, want ErrInvalid", err)
	}
}

func TestAMalformedTokenIsRejectedWithoutPanicking(t *testing.T) {
	s := signer(t, "k1")
	for _, tok := range []string{"", ".", "abc", "abc.", ".abc", "!!!.!!!", "a.b.c", strings.Repeat("A", 4096)} {
		if err := s.Verify(tok, "TKT-001", now); !errors.Is(err, eticket.ErrInvalid) {
			t.Errorf("Verify(%q) = %v, want ErrInvalid", tok, err)
		}
	}
}

func TestAnEmptyKeyIsRefused(t *testing.T) {
	if _, err := eticket.NewSigner(nil); !errors.Is(err, eticket.ErrNoKey) {
		t.Fatalf("NewSigner(nil) = %v, want ErrNoKey", err)
	}
}

func TestTheLinkWorksUntilMidnightJakartaAfterTheEvent(t *testing.T) {
	got, err := eticket.ExpiresAt("2026-12-25")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 12, 25, 17, 0, 0, 0, time.UTC); !got.Equal(want) { // 26 Dec 00:00 WIB
		t.Fatalf("ExpiresAt = %v, want %v", got, want)
	}
	if _, err := eticket.ExpiresAt("25-12-2026"); err == nil {
		t.Fatal("a date that is not YYYY-MM-DD must be an error")
	}
}
