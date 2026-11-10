package redis

import (
	"os"
	"sort"
	"testing"
	"time"

	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

func discover(t *testing.T, f fixture) map[string][]string {
	t.Helper()
	events, err := NewEventSource(f.q.rdb).Discover(ctx)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	out := map[string][]string{}
	for _, e := range events {
		out[e.ID] = e.Categories
	}
	return out
}

func seedEvent(f fixture, id, state string, cats ...string) {
	ev, _ := rediskeys.NewEvent(id)
	f.mr.HSet(ev.QueueHash(), "state", state)
	for _, c := range cats {
		_ = f.mr.Set(ev.Stock(c), "10")
	}
}

func TestDiscoverFindsEventsAndTheirStockCategories(t *testing.T) {
	f := newFixture(t)
	seedEvent(f, "evt_a", "OPEN", "VIP", "CAT1")
	seedEvent(f, "evt_b", "SCHEDULED", "GA")
	seedEvent(f, "evt_c", "FULL_BOOKED", "VIP")
	got := discover(t, f)
	if len(got) != 3 || len(got["evt_a"]) != 2 || got["evt_a"][0] != "CAT1" || got["evt_a"][1] != "VIP" || got["evt_b"][0] != "GA" {
		t.Fatalf("discovered %v", got)
	}
}

func TestDiscoverReturnsEventsInAStableOrder(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{"evt_c", "evt_a", "evt_b"} {
		seedEvent(f, id, "OPEN", "VIP")
	}
	events, err := NewEventSource(f.q.rdb).Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for _, e := range events {
		ids = append(ids, e.ID)
	}
	if !sort.StringsAreSorted(ids) || len(ids) != 3 {
		t.Fatalf("order %v", ids)
	}
}

func TestDiscoverIgnoresByTicketKeysAndHashesWithoutState(t *testing.T) {
	f := newFixture(t)
	seedEvent(f, "evt_a", "OPEN", "VIP")
	ev, _ := rediskeys.NewEvent("evt_a")
	_, _ = f.mr.ZAdd(ev.ByTicket(), 1, "qt") // queue:{evt_a}:by_ticket is not an event
	stray, _ := rediskeys.NewEvent("evt_stray")
	f.mr.HSet(stray.QueueHash(), "rate_per_min", "20") // a stray write, no state
	got := discover(t, f)
	if len(got) != 1 || got["evt_a"] == nil {
		t.Fatalf("discovered %v, want only evt_a", got)
	}
}

func TestDiscoverDropsClosedSales(t *testing.T) {
	f := newFixture(t)
	seedEvent(f, "evt_a", "OPEN", "VIP")
	seedEvent(f, "evt_old", "CLOSED", "VIP")
	if got := discover(t, f); len(got) != 1 || got["evt_old"] != nil {
		t.Fatalf("discovered %v, want the closed sale dropped", got)
	}
}

func TestDiscoverKeepsAnEventThatHasNoStockYet(t *testing.T) {
	f := newFixture(t)
	seedEvent(f, "evt_a", "OPEN")
	got := discover(t, f)
	if cats, ok := got["evt_a"]; !ok || len(cats) != 0 {
		t.Fatalf("discovered %v, want evt_a with no categories (the Lua then reports STOCK_UNINITIALIZED)", got)
	}
}

func TestDiscoverWithNothingInRedisReturnsNoEvents(t *testing.T) {
	if got := discover(t, newFixture(t)); len(got) != 0 {
		t.Fatalf("discovered %v", got)
	}
}

func TestDiscoverIgnoresStockKeysOfAnUnknownEvent(t *testing.T) {
	f := newFixture(t)
	ev, _ := rediskeys.NewEvent("evt_x")
	_ = f.mr.Set(ev.Stock("VIP"), "5") // stock with no queue hash: not a sale
	if got := discover(t, f); len(got) != 0 {
		t.Fatalf("discovered %v", got)
	}
}

// The real cluster: keys of different events live on different shards, so every master must be scanned.
func TestDiscoverWalksTheRealCluster(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" || testing.Short() {
		t.Skip("set REDIS_ADDR (make verify does)")
	}
	rdb := goredis.NewClusterClient(&goredis.ClusterOptions{Addrs: []string{addr}})
	defer func() { _ = rdb.Close() }()
	id := "evt_disc_" + time.Now().Format("150405.000000")
	ev, _ := rediskeys.NewEvent(id)
	rdb.HSet(ctx, ev.QueueHash(), "state", "OPEN")
	rdb.Set(ctx, ev.Stock("VIP"), 5, 0)
	defer rdb.Del(ctx, ev.QueueHash(), ev.Stock("VIP"))

	events, err := NewEventSource(rdb).Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var found *domain.Event
	for i := range events {
		if events[i].ID == id {
			found = &events[i]
		}
	}
	if found == nil || len(found.Categories) != 1 || found.Categories[0] != "VIP" {
		t.Fatalf("event %s not discovered correctly: %+v", id, events)
	}
}
