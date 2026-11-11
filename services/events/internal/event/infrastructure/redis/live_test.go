package redis

import (
	"context"
	"reflect"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/domain"
)

var ctx = context.Background()

func client(t *testing.T) (*miniredis.Miniredis, *Live) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, NewLive(rdb)
}

func TestQueuesReadsStateAndDepthAndLeavesAMissingHashEmpty(t *testing.T) {
	mr, live := client(t)
	a, _ := rediskeys.NewEvent("a")
	mr.HSet(a.QueueHash(), "state", "OPEN", "issued", "120", "now_serving", "100")
	b, _ := rediskeys.NewEvent("b")
	mr.HSet(b.QueueHash(), "state", "FULL_BOOKED", "issued", "5", "now_serving", "9") // serving ahead of issued: depth never negative

	got, err := live.Queues(ctx, []string{"a", "b", "none"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]domain.Queue{"a": {State: "OPEN", Depth: 20}, "b": {State: "FULL_BOOKED"}, "none": {}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestQueuesWithNoEventsDoesNotCallRedisAndAnInvalidIDIsAnError(t *testing.T) {
	_, live := client(t)
	if got, err := live.Queues(ctx, nil); err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := live.Queues(ctx, []string{"bad{id}"}); err == nil {
		t.Fatal("an invalid event id must be an error")
	}
}

func TestQueuesReturnsAnErrorWhenRedisIsDown(t *testing.T) {
	mr, live := client(t)
	mr.Close()
	if _, err := live.Queues(ctx, []string{"a"}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestStockReadsTheKeysThatExistAndSkipsMissingOrGarbledOnes(t *testing.T) {
	mr, live := client(t)
	ev, _ := rediskeys.NewEvent("a")
	_ = mr.Set(ev.Stock("vip"), "12")
	_ = mr.Set(ev.Stock("trib"), "-1")
	_ = mr.Set(ev.Stock("odd"), "x")

	got, err := live.Stock(ctx, "a", []string{"vip", "fest", "trib", "odd"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, map[string]int64{"vip": 12, "trib": -1}) {
		t.Fatalf("got %v", got)
	}
}

func TestStockEdgeCases(t *testing.T) {
	mr, live := client(t)
	if got, err := live.Stock(ctx, "a", nil); err != nil || len(got) != 0 {
		t.Fatalf("got %v, %v", got, err)
	}
	if _, err := live.Stock(ctx, "bad{id}", []string{"vip"}); err == nil {
		t.Fatal("an invalid event id must be an error")
	}
	mr.Close()
	if _, err := live.Stock(ctx, "a", []string{"vip"}); err == nil {
		t.Fatal("expected an error when Redis is down")
	}
}
