package redis

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/redisscript"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/domain"
)

type fixture struct {
	mr *miniredis.Miniredis
	q  *Queue
	ev rediskeys.Event
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ev, err := rediskeys.NewEvent("E1")
	if err != nil {
		t.Fatal(err)
	}
	return fixture{mr: mr, q: NewQueue(rdb), ev: ev}
}

var ctx = context.Background()

func TestSnapshotReadsBothCountersAndTreatsMissingAsZero(t *testing.T) {
	f := newFixture(t)
	if ns, is, err := f.q.Snapshot(ctx, "E1"); err != nil || ns != 0 || is != 0 {
		t.Fatalf("empty hash: %d %d %v", ns, is, err)
	}
	f.mr.HSet(f.ev.QueueHash(), "now_serving", "12", "issued", "40")
	if ns, is, _ := f.q.Snapshot(ctx, "E1"); ns != 12 || is != 40 {
		t.Fatalf("snapshot %d %d", ns, is)
	}
}

func TestPendingAreTheTokensAtOrBelowNowServing(t *testing.T) {
	f := newFixture(t)
	for i, tok := range []string{"a", "b", "c"} {
		_, _ = f.mr.ZAdd(f.ev.ByTicket(), float64(i+1), tok)
	}
	got, err := f.q.Pending(ctx, "E1", 2)
	if err != nil || len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("pending %v %v", got, err)
	}
	if n, _ := f.q.PendingCount(ctx, "E1", 2); n != 2 {
		t.Fatalf("pending count %d", n)
	}
	if err := f.q.Dequeue(ctx, "E1", "a"); err != nil {
		t.Fatal(err)
	}
	if err := f.q.Dequeue(ctx, "E1", "a"); err != nil { // a second ZREM is harmless
		t.Fatal(err)
	}
}

func TestTokenFoundOnlyWhileTheHashExists(t *testing.T) {
	f := newFixture(t)
	if _, found, _ := f.q.Token(ctx, "gone"); found {
		t.Fatal("a missing hash read as found")
	}
	f.mr.HSet(rediskeys.Token("g"), "ticket_no", "3", "device_hash", "dh")
	info, found, _ := f.q.Token(ctx, "g")
	if !found || info.DeviceHash != "dh" || info.AccountID != "" {
		t.Fatalf("guest token %+v found=%v", info, found)
	}
	f.mr.HSet(rediskeys.Token("u"), "ticket_no", "4", "account_id", "acc")
	if info, _, _ := f.q.Token(ctx, "u"); info.AccountID != "acc" || info.DeviceHash != "" {
		t.Fatalf("logged-in token %+v", info)
	}
}

func TestPutGrantWritesTheRightFieldsWithATTL(t *testing.T) {
	f := newFixture(t)
	b := domain.Booking{ID: "B1", GrantToken: "G1"}
	if err := f.q.PutGrant(ctx, "E1", "qt", b, domain.TokenInfo{DeviceHash: "dh"}, 90*time.Second); err != nil {
		t.Fatal(err)
	}
	h := f.mr.HGet(rediskeys.Grant("G1"), "device_hash")
	if h != "dh" || f.mr.HGet(rediskeys.Grant("G1"), "account_id") != "" || f.mr.HGet(rediskeys.Grant("G1"), "booking_id") != "B1" {
		t.Fatal("guest grant has the wrong fields")
	}
	if ttl := f.mr.TTL(rediskeys.Grant("G1")); ttl != 90*time.Second {
		t.Fatalf("TTL %s", ttl)
	}
	if err := f.q.PutGrant(ctx, "E1", "qt2", domain.Booking{ID: "B2", GrantToken: "G2"}, domain.TokenInfo{AccountID: "acc"}, time.Minute); err != nil {
		t.Fatal(err)
	}
	if f.mr.HGet(rediskeys.Grant("G2"), "account_id") != "acc" || f.mr.HGet(rediskeys.Grant("G2"), "device_hash") != "" {
		t.Fatal("logged-in grant has the wrong fields")
	}
}

func TestAttachGrantNeverRecreatesAnExpiredToken(t *testing.T) {
	f := newFixture(t)
	b := domain.Booking{ID: "B1", GrantToken: "G1", ExpiresAt: time.Date(2026, 11, 10, 19, 5, 0, 0, time.UTC)}
	if err := f.q.AttachGrant(ctx, "gone", b); err != nil || f.mr.Exists(rediskeys.Token("gone")) {
		t.Fatalf("an expired token hash was recreated (err %v)", err)
	}
	f.mr.HSet(rediskeys.Token("live"), "ticket_no", "1")
	f.mr.SetTTL(rediskeys.Token("live"), time.Hour)
	if err := f.q.AttachGrant(ctx, "live", b); err != nil {
		t.Fatal(err)
	}
	if f.mr.HGet(rediskeys.Token("live"), "grant_token") != "G1" || f.mr.HGet(rediskeys.Token("live"), "booking_expires_at") != "2026-11-10T19:05:00Z" {
		t.Fatal("grant not copied")
	}
	if f.mr.TTL(rediskeys.Token("live")) != time.Hour {
		t.Fatal("the token hash lost its TTL")
	}
}

func TestViewAndRate(t *testing.T) {
	f := newFixture(t)
	f.mr.HSet(f.ev.QueueHash(), "state", "FULL_BOOKED", "full_booked_at", "1800000000")
	if v, _ := f.q.View(ctx, "E1"); v.State != "FULL_BOOKED" || v.FullBookedAt != 1800000000 {
		t.Fatalf("view %+v", v)
	}
	if err := f.q.SetRate(ctx, "E1", 613); err != nil || f.mr.HGet(f.ev.QueueHash(), "rate_per_min") != "613" {
		t.Fatalf("rate not stored (%v)", err)
	}
}

func TestCloseOnlyWhenStillFullyBookedSinceTheSameMoment(t *testing.T) {
	f := newFixture(t)
	f.mr.HSet(f.ev.QueueHash(), "state", "OPEN") // reopened between the read and the write
	if err := f.q.Close(ctx, "E1", 1800000000); err != nil || f.mr.HGet(f.ev.QueueHash(), "state") != "OPEN" {
		t.Fatal("Close overwrote a reopened sale")
	}
	f.mr.HSet(f.ev.QueueHash(), "state", "FULL_BOOKED", "full_booked_at", "1800000999") // a newer sell-out
	if err := f.q.Close(ctx, "E1", 1800000000); err != nil || f.mr.HGet(f.ev.QueueHash(), "state") != "FULL_BOOKED" {
		t.Fatal("Close used a stale full_booked_at")
	}
	if err := f.q.Close(ctx, "E1", 1800000999); err != nil {
		t.Fatal(err)
	}
	if f.mr.HGet(f.ev.QueueHash(), "state") != "CLOSED" || f.mr.HGet(f.ev.QueueHash(), "full_booked_at") != "" {
		t.Fatal("the sale did not close")
	}
}

func TestDropGrantAndPublish(t *testing.T) {
	f := newFixture(t)
	f.mr.HSet(rediskeys.Grant("G1"), "booking_id", "B1")
	if err := f.q.DropGrant(ctx, "G1"); err != nil || f.mr.Exists(rediskeys.Grant("G1")) {
		t.Fatalf("grant not deleted (%v)", err)
	}
	sub := f.mr.NewSubscriber()
	_ = sub
	if err := f.q.PublishBroadcast(ctx, []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := f.q.PublishStatus(ctx, nil); err == nil {
		t.Fatal("an empty status payload was published")
	}
}

func TestAdvanceRunsTheScriptOnTheEventsKeys(t *testing.T) {
	f := newFixture(t)
	f.mr.HSet(f.ev.QueueHash(), "state", "OPEN", "now_serving", "0", "issued", "2")
	_ = f.mr.Set(f.ev.Stock("VIP"), "300")
	_, _ = f.mr.ZAdd(f.ev.ByTicket(), 1, "a")
	_, _ = f.mr.ZAdd(f.ev.ByTicket(), 2, "b")
	res, err := f.q.Advance(ctx, domain.Event{ID: "E1", Categories: []string{"VIP"}}, redisscript.Args{Max: 10, NowServing: 0, Now: 1800000000})
	if err != nil || res.Code != redisscript.ResultAdmitted || len(res.Tokens) != 2 {
		t.Fatalf("advance %+v %v", res, err)
	}
	if _, err := f.q.Advance(ctx, domain.Event{ID: "bad{id"}, redisscript.Args{}); err == nil {
		t.Fatal("an invalid event id was accepted")
	}
}
