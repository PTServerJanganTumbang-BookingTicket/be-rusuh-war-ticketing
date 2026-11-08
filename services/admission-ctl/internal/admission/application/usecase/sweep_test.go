package usecase

import (
	"context"
	"reflect"
	"testing"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

func TestSweepKillsOnlyTheGrantOfEachLapsedBooking(t *testing.T) {
	b := &fakeBookings{lapsed: []domain.Lapsed{{BookingID: "B1", GrantToken: "G1"}, {BookingID: "B2"}}}
	q := &fakeQueue{}
	tick, _, m := newTick(b, q)
	if n := tick.Sweep(context.Background()); n != 2 {
		t.Fatalf("lapsed %d, want 2", n)
	}
	if !reflect.DeepEqual(q.dropped, []string{"G1"}) || m.counts[MetricLapsed] != 2 {
		t.Fatalf("dropped %v metrics %v", q.dropped, m.counts)
	}
	for _, op := range q.ops {
		if op != "DropGrant" {
			t.Fatalf("sweep also called %s: the queue token and acct must stay", op)
		}
	}
}

func TestSweepFailureIsCountedNotFatal(t *testing.T) {
	tick, _, m := newTick(&fakeBookings{lapseErr: errDown}, &fakeQueue{})
	if n := tick.Sweep(context.Background()); n != 0 || m.counts[MetricSweepFailures] != 1 {
		t.Fatalf("n=%d metrics %v", n, m.counts)
	}
}

func TestRunSweepsBeforeTheEvents(t *testing.T) {
	b := &fakeBookings{}
	q := &fakeQueue{view: domain.EventView{State: "OPEN"}}
	tick, _, _ := newTick(b, q)
	tick.Run(context.Background())
	if b.lapseCalls != 1 {
		t.Fatalf("sweep ran %d times in one tick", b.lapseCalls)
	}
}
