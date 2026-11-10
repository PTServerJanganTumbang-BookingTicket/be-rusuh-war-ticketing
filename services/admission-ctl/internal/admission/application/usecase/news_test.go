package usecase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

func idle(view domain.EventView) *fakeQueue {
	return &fakeQueue{ns: 120, issued: 120, view: view, advance: redisscript.Result{Code: redisscript.ResultEmpty}}
}

func TestBroadcastCarriesOnlyPublicNumbers(t *testing.T) {
	q := idle(domain.EventView{State: "OPEN"})
	tick, _, _ := newTick(&fakeBookings{}, q)
	_ = tick.RunEvent(context.Background(), testConfig().Events[0])
	want := `{"eventId":"E1","nowServing":120,"state":"OPEN","ratePerMin":20,"serverTime":1794337200}`
	if len(q.broadcast) != 1 || string(q.broadcast[0]) != want {
		t.Fatalf("broadcast %q, want %s", q.broadcast, want)
	}
}

func TestBroadcastOnMoveAndHeartbeatEveryTenSeconds(t *testing.T) {
	q := idle(domain.EventView{State: "OPEN"})
	tick, clk, _ := newTick(&fakeBookings{}, q)
	ev := testConfig().Events[0]
	_ = tick.RunEvent(context.Background(), ev)
	for i := 0; i < 19; i++ { // 9.5 s of quiet
		clk.add(half)
		_ = tick.RunEvent(context.Background(), ev)
	}
	if len(q.broadcast) != 1 {
		t.Fatalf("%d broadcasts in 9.5 s of quiet, want only the first", len(q.broadcast))
	}
	clk.add(half) // 10 s
	_ = tick.RunEvent(context.Background(), ev)
	q.ns, q.issued = 130, 130
	_ = tick.RunEvent(context.Background(), ev) // the queue moved
	if len(q.broadcast) != 3 {
		t.Fatalf("%d broadcasts, want first + heartbeat + move", len(q.broadcast))
	}
}

func TestStatusIsPublishedOnceAndOnlyOnChange(t *testing.T) {
	q := idle(domain.EventView{State: "FULL_BOOKED", FullBookedAt: 1794337200})
	tick, clk, _ := newTick(&fakeBookings{}, q)
	ev := testConfig().Events[0]
	_ = tick.RunEvent(context.Background(), ev)
	clk.add(half)
	_ = tick.RunEvent(context.Background(), ev)
	if len(q.status) != 1 {
		t.Fatalf("%d status messages for an unchanged state, want 1", len(q.status))
	}
	var m map[string]any
	_ = json.Unmarshal(q.status[0], &m)
	if m["state"] != "FULL_BOOKED" || m["fullBookedAt"] != float64(1794337200) {
		t.Fatalf("status %v", m)
	}
	q.view = domain.EventView{State: "OPEN"}
	_ = tick.RunEvent(context.Background(), ev)
	_ = json.Unmarshal(q.status[1], &m)
	if len(q.status) != 2 || m["fullBookedAt"] != nil {
		t.Fatalf("reopen status %v", q.status)
	}
}

func TestSaleClosesAfterNineHundredThirtySeconds(t *testing.T) {
	since := newClock().t.Unix()
	for secs, closes := range map[int]bool{929: false, 930: true} {
		q := idle(domain.EventView{State: "FULL_BOOKED", FullBookedAt: since})
		tick, clk, _ := newTick(&fakeBookings{}, q)
		clk.add(time.Duration(secs) * time.Second)
		_ = tick.RunEvent(context.Background(), testConfig().Events[0])
		if got := len(q.closedAt) == 1; got != closes {
			t.Errorf("after %d s: closed=%v, want %v", secs, got, closes)
		}
		if closes && q.closedAt[0] != since {
			t.Errorf("Close was told fullBookedAt %d, want %d", q.closedAt[0], since)
		}
	}
}

func TestUnseededEventGetsNoRateWrite(t *testing.T) {
	q := idle(domain.EventView{})
	tick, _, _ := newTick(&fakeBookings{}, q)
	_ = tick.RunEvent(context.Background(), testConfig().Events[0])
	if len(q.rates) != 0 {
		t.Fatalf("rate_per_min written for an event with no state: %v", q.rates)
	}
}

func TestRateIsWrittenOnlyWhenItChanges(t *testing.T) {
	q := idle(domain.EventView{State: "OPEN"})
	tick, clk, _ := newTick(&fakeBookings{}, q)
	for i := 0; i < 3; i++ {
		_ = tick.RunEvent(context.Background(), testConfig().Events[0])
		clk.add(half)
	}
	if len(q.rates) != 1 {
		t.Fatalf("rate written %d times for a constant rate", len(q.rates))
	}
}
