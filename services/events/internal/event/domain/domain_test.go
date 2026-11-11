package domain_test

import (
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/domain"
)

func TestDeriveQueueState(t *testing.T) {
	cases := []struct {
		name     string
		internal string
		depth    int64
		want     domain.QueueState
	}{
		{"a missing hash shows scheduled", "", 0, domain.StateScheduled},
		{"a scheduled sale shows scheduled", "SCHEDULED", 0, domain.StateScheduled},
		{"an unknown state shows scheduled", "PAUSED", 0, domain.StateScheduled},
		{"an open sale with room is open", "OPEN", 10, domain.StateOpen},
		{"an open sale one below capacity is open", "OPEN", 99, domain.StateOpen},
		{"an open sale at capacity is a full queue", "OPEN", 100, domain.StateFullQueue},
		{"an open sale over capacity is a full queue", "OPEN", 150, domain.StateFullQueue},
		{"a fully booked sale is fully booked", "FULL_BOOKED", 0, domain.StateFullBooked},
		{"a fully booked sale with a full queue is still fully booked", "FULL_BOOKED", 100, domain.StateFullBooked},
		{"a closed sale is closed", "CLOSED", 0, domain.StateClosed},
		{"a closed sale with a full queue is closed", "CLOSED", 500, domain.StateClosed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := domain.DeriveQueueState(c.internal, c.depth, 100); got != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
		})
	}
}

func TestEnabled_isTrueOnlyForOpenAndFullBooked(t *testing.T) {
	want := map[domain.QueueState]bool{
		domain.StateScheduled: false, domain.StateOpen: true, domain.StateFullQueue: false,
		domain.StateFullBooked: true, domain.StateClosed: false,
	}
	for s, w := range want {
		if s.Enabled() != w {
			t.Errorf("%s.Enabled() = %v, want %v", s, s.Enabled(), w)
		}
	}
}

func TestKnown(t *testing.T) {
	for _, s := range []string{"SCHEDULED", "OPEN", "FULL_BOOKED", "CLOSED"} {
		if !domain.Known(s) {
			t.Errorf("%s should be known", s)
		}
	}
	for _, s := range []string{"", "PAUSED", "FULL_QUEUE"} {
		if domain.Known(s) {
			t.Errorf("%q should not be known", s)
		}
	}
}

func TestScheduleState(t *testing.T) {
	now := time.Date(2026, 11, 10, 2, 0, 0, 0, time.UTC)
	before, after := now.Add(time.Hour), now.Add(-time.Hour)
	for name, c := range map[string]struct {
		at   *time.Time
		want domain.QueueState
	}{
		"no schedule is scheduled":           {nil, domain.StateScheduled},
		"a start in the future is scheduled": {&before, domain.StateScheduled},
		"a start in the past is open":        {&after, domain.StateOpen},
		"a start exactly now is open":        {&now, domain.StateOpen},
	} {
		if got := domain.ScheduleState(c.at, now); got != c.want {
			t.Errorf("%s: got %s, want %s", name, got, c.want)
		}
	}
}
