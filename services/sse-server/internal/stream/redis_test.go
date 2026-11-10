package stream

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
)

func newRedis(t *testing.T) (*miniredis.Miniredis, goredis.UniversalClient) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return mr, rdb
}

func mustEvent(t *testing.T, id string) rediskeys.Event {
	t.Helper()
	ev, err := rediskeys.NewEvent(id)
	if err != nil {
		t.Fatal(err)
	}
	return ev
}

func TestRedisStoreReadsStateNumbersAndTokenOwner(t *testing.T) {
	mr, rdb := newRedis(t)
	ev := mustEvent(t, "E1")
	st := NewRedisStore(rdb)
	ctx := context.Background()

	if _, found, err := st.State(ctx, ev); err != nil || found {
		t.Fatalf("missing hash: found=%v err=%v", found, err)
	}
	if ns, rate, err := st.Numbers(ctx, ev); err != nil || ns != 0 || rate != 0 {
		t.Fatalf("missing numbers = %d %d %v, want zeros", ns, rate, err)
	}
	if _, found, err := st.TokenEvent(ctx, "tok"); err != nil || found {
		t.Fatalf("missing token: found=%v err=%v", found, err)
	}

	mr.HSet(ev.QueueHash(), rediskeys.FieldState, StateOpen, rediskeys.FieldNowServing, "41", rediskeys.FieldRatePerMin, "318")
	mr.HSet(rediskeys.Token("tok"), "event_id", "E1")
	if s, found, err := st.State(ctx, ev); err != nil || !found || s != StateOpen {
		t.Fatalf("state = %q %v %v", s, found, err)
	}
	if ns, rate, err := st.Numbers(ctx, ev); err != nil || ns != 41 || rate != 318 {
		t.Fatalf("numbers = %d %d %v", ns, rate, err)
	}
	if e, found, err := st.TokenEvent(ctx, "tok"); err != nil || !found || e != "E1" {
		t.Fatalf("token event = %q %v %v", e, found, err)
	}
}

func TestRedisStoreRejectsANumberThatIsNotAnInteger(t *testing.T) {
	mr, rdb := newRedis(t)
	ev := mustEvent(t, "E1")
	mr.HSet(ev.QueueHash(), rediskeys.FieldNowServing, "abc")
	if _, _, err := NewRedisStore(rdb).Numbers(context.Background(), ev); err == nil {
		t.Fatal("a corrupt now_serving was accepted")
	}
	mr.HSet(ev.QueueHash(), rediskeys.FieldNowServing, "1", rediskeys.FieldRatePerMin, "x")
	if _, _, err := NewRedisStore(rdb).Numbers(context.Background(), ev); err == nil {
		t.Fatal("a corrupt rate_per_min was accepted")
	}
}

func TestRedisStoreReportsAnUnreachableRedis(t *testing.T) {
	mr, rdb := newRedis(t)
	ev := mustEvent(t, "E1")
	mr.Close()
	st := NewRedisStore(rdb)
	ctx := context.Background()
	if _, _, err := st.State(ctx, ev); err == nil {
		t.Error("State: want an error")
	}
	if _, _, err := st.Numbers(ctx, ev); err == nil {
		t.Error("Numbers: want an error")
	}
	if _, _, err := st.TokenEvent(ctx, "tok"); err == nil {
		t.Error("TokenEvent: want an error")
	}
}

func TestRedisSourceDeliversMessagesFromBothChannels(t *testing.T) {
	mr, rdb := newRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sub, err := NewRedisSource(rdb).Subscribe(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()

	mr.Publish(rediskeys.ChannelBroadcast, "b")
	mr.Publish(rediskeys.ChannelStatus, "s")
	got := map[string]string{}
	for len(got) < 2 {
		select {
		case m := <-sub.Messages():
			got[m.Channel] = string(m.Payload)
		case <-time.After(3 * time.Second):
			t.Fatalf("got only %v", got)
		}
	}
	if got[rediskeys.ChannelBroadcast] != "b" || got[rediskeys.ChannelStatus] != "s" {
		t.Fatalf("got %v", got)
	}
}

func TestRedisSourceEndsTheSubscriptionWhenTheLinkDies(t *testing.T) {
	mr, rdb := newRedis(t)
	sub, err := NewRedisSource(rdb).Subscribe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sub.Close() }()
	mr.Close()
	select {
	case _, ok := <-sub.Messages():
		if ok {
			t.Fatal("got a message from a dead link")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the subscription did not end")
	}
}

func TestRedisSourceFailsToSubscribeWhenRedisIsDown(t *testing.T) {
	mr, rdb := newRedis(t)
	mr.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := NewRedisSource(rdb).Subscribe(ctx); err == nil {
		t.Fatal("want an error")
	}
}
