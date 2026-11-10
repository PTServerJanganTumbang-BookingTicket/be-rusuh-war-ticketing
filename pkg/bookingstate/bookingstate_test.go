package bookingstate_test

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"testing"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
)

func TestEachStatusMapsToItsDetailStatus(t *testing.T) {
	want := map[bookingstate.Status]string{
		bookingstate.Granted:             bookingstate.DetailSelecting,
		bookingstate.Selecting:           bookingstate.DetailSelecting,
		bookingstate.Held:                bookingstate.DetailPendingPayment,
		bookingstate.AwaitingPayment:     bookingstate.DetailPendingPayment,
		bookingstate.PendingCancellation: bookingstate.DetailPendingPayment, // the payment may still arrive (30 s grace)
		bookingstate.Confirmed:           bookingstate.DetailPaid,
		bookingstate.Expired:             bookingstate.DetailExpired,
		bookingstate.Lapsed:              bookingstate.DetailExpired,
		bookingstate.Abandoned:           bookingstate.DetailExpired,
	}
	if len(bookingstate.All) != len(want) {
		t.Fatalf("All has %d statuses, want %d", len(bookingstate.All), len(want))
	}
	for _, s := range bookingstate.All {
		if got := s.DetailStatus(); got != want[s] {
			t.Errorf("%s → %q, want %q", s, got, want[s])
		}
	}
}

func TestExactlyTheFourFinishedStatusesAreTerminal(t *testing.T) {
	terminal := []bookingstate.Status{bookingstate.Confirmed, bookingstate.Expired, bookingstate.Lapsed, bookingstate.Abandoned}
	for _, s := range bookingstate.All {
		if got, want := s.IsTerminal(), slices.Contains(terminal, s); got != want {
			t.Errorf("%s.IsTerminal() = %v, want %v", s, got, want)
		}
	}
}

func TestParseAcceptsEveryStatusAndRejectsAnUnknownValue(t *testing.T) {
	for _, s := range bookingstate.All {
		if got, err := bookingstate.Parse(string(s)); err != nil || got != s {
			t.Errorf("Parse(%q) = %q, %v", s, got, err)
		}
	}
	for _, bad := range []string{"", "granted", "PAID", "SOLD_OUT"} {
		if _, err := bookingstate.Parse(bad); !errors.Is(err, bookingstate.ErrUnknownStatus) {
			t.Errorf("Parse(%q) error = %v, want ErrUnknownStatus", bad, err)
		}
	}
}

// The Go list and the database constraint must not drift: a status the database accepts but Go cannot parse
// would fail every read of that row.
func TestTheStatusListEqualsTheBookingsStatusCheck(t *testing.T) {
	pool := pgtest.New(t)
	var def string
	err := pool.QueryRow(context.Background(),
		`SELECT pg_get_constraintdef(oid) FROM pg_constraint WHERE conname = 'bookings_status'`).Scan(&def)
	if err != nil {
		t.Fatal(err)
	}
	var inDB []string
	for _, m := range regexp.MustCompile(`'([A-Z_]+)'`).FindAllStringSubmatch(def, -1) {
		inDB = append(inDB, m[1])
	}
	var inGo []string
	for _, s := range bookingstate.All {
		inGo = append(inGo, string(s))
	}
	slices.Sort(inDB)
	slices.Sort(inGo)
	if !slices.Equal(inDB, inGo) {
		t.Fatalf("CHECK has %v, Go has %v", inDB, inGo)
	}
}
