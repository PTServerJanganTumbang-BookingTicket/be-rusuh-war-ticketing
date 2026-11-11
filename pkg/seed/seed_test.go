package seed_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgconn"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/seed"
)

var now = time.Date(2026, 11, 11, 3, 0, 0, 0, time.UTC)

func TestTheSampleEventsAreFiveInJakartaEachInADifferentSaleState(t *testing.T) {
	evs := seed.JakartaEvents()
	if len(evs) != 5 {
		t.Fatalf("%d events", len(evs))
	}
	states, ids, cats := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, e := range evs {
		if _, err := rediskeys.NewEvent(e.ID); err != nil {
			t.Errorf("%s: %v", e.ID, err)
		}
		if ids[e.ID] {
			t.Errorf("duplicate event id %s", e.ID)
		}
		ids[e.ID] = true
		states[e.State] = true
		if e.City != "Jakarta" || e.Name == "" || e.Description == "" || e.Venue == "" || len(e.Categories) == 0 {
			t.Errorf("%s is incomplete: %+v", e.ID, e)
		}
		if _, err := time.Parse("2006-01-02", e.Date); err != nil {
			t.Errorf("%s date: %v", e.ID, err)
		}
		if _, err := time.Parse("15:04", e.Time); err != nil {
			t.Errorf("%s time: %v", e.ID, err)
		}
		if !strings.HasPrefix(e.ImageURL, "https://thumb.wikimedia.org/") || e.ImageCredit == "" {
			t.Errorf("%s: picture %q credit %q", e.ID, e.ImageURL, e.ImageCredit)
		}
		for _, c := range e.Categories {
			if cats[c.ID] {
				t.Errorf("category id %s is not globally unique", c.ID)
			}
			cats[c.ID] = true
			if c.Price <= 0 || c.Capacity <= 0 || c.Available < 0 || c.Available > c.Capacity {
				t.Errorf("%s: bad numbers %+v", c.ID, c)
			}
		}
	}
	for _, s := range []string{seed.StateOpen, seed.StateScheduled, seed.StateFullBooked, seed.StateClosed} {
		if !states[s] {
			t.Errorf("no sample event in state %s", s)
		}
	}
}

func TestOnlyTheOpenAndScheduledSalesAreDatedInTheFutureAndAFullBookedSaleHasNoStock(t *testing.T) {
	for _, e := range seed.JakartaEvents() {
		d, _ := time.Parse("2006-01-02", e.Date)
		if (e.State == seed.StateOpen || e.State == seed.StateScheduled) && !d.After(now) {
			t.Errorf("%s (%s) is dated %s, not in the future", e.ID, e.State, e.Date)
		}
		if e.State == seed.StateFullBooked || e.State == seed.StateClosed {
			for _, c := range e.Categories {
				if c.Available != 0 {
					t.Errorf("%s/%s has stock %d in state %s", e.ID, c.ID, c.Available, e.State)
				}
			}
		}
	}
}

func newRedis(t *testing.T) (*goredis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb, mr
}

func TestRedisSeedCreatesTheSaleStateAndStockOfEveryEvent(t *testing.T) {
	rdb, _ := newRedis(t)
	ctx := context.Background()
	n, err := seed.Redis(ctx, rdb, seed.JakartaEvents(), now)
	if err != nil || n == 0 {
		t.Fatalf("created %d, err %v", n, err)
	}
	open, _ := rediskeys.NewEvent("evt_jkt_istora_nightfall")
	h := rdb.HGetAll(ctx, open.QueueHash()).Val()
	if h[rediskeys.FieldState] != "OPEN" || h[rediskeys.FieldIssued] != "0" || h[rediskeys.FieldNowServing] != "0" ||
		h[rediskeys.FieldName] != "Nightfall Live di Istora" || h[rediskeys.FieldSaleStartsAt] != "2026-11-10T03:00:00Z" {
		t.Fatalf("queue hash = %v", h)
	}
	if _, has := h[rediskeys.FieldFullBookedAt]; has {
		t.Fatal("an open sale got full_booked_at")
	}
	if v, _ := rdb.Get(ctx, open.Stock("cat_istora_vip")).Int(); v != 300 {
		t.Fatalf("VIP stock = %d", v)
	}
	full, _ := rediskeys.NewEvent("evt_jkt_tim_wayang")
	fh := rdb.HGetAll(ctx, full.QueueHash()).Val()
	if fh[rediskeys.FieldState] != "FULL_BOOKED" || fh[rediskeys.FieldFullBookedAt] != strconv.FormatInt(now.Unix(), 10) {
		t.Fatalf("full booked hash = %v", fh)
	}
	if v, err := rdb.Get(ctx, full.Stock("cat_wayang_vip")).Int(); err != nil || v != 0 {
		t.Fatalf("sold-out stock = %d, %v (the key must exist with 0)", v, err)
	}
}

func TestSeedingTwiceNeverResetsALiveSale(t *testing.T) {
	rdb, _ := newRedis(t)
	ctx := context.Background()
	evs := seed.JakartaEvents()
	if _, err := seed.Redis(ctx, rdb, evs, now); err != nil {
		t.Fatal(err)
	}
	ev, _ := rediskeys.NewEvent("evt_jkt_istora_nightfall")
	rdb.HSet(ctx, ev.QueueHash(), rediskeys.FieldIssued, 120, rediskeys.FieldNowServing, 100, rediskeys.FieldState, "FULL_BOOKED")
	rdb.Set(ctx, ev.Stock("cat_istora_vip"), 42, 0)

	n, err := seed.Redis(ctx, rdb, evs, now.Add(time.Hour))
	if err != nil || n != 0 {
		t.Fatalf("second run created %d (err %v), want 0", n, err)
	}
	h := rdb.HGetAll(ctx, ev.QueueHash()).Val()
	if h[rediskeys.FieldIssued] != "120" || h[rediskeys.FieldNowServing] != "100" || h[rediskeys.FieldState] != "FULL_BOOKED" {
		t.Fatalf("a re-seed changed the live sale: %v", h)
	}
	if v, _ := rdb.Get(ctx, ev.Stock("cat_istora_vip")).Int(); v != 42 {
		t.Fatalf("stock reset to %d", v)
	}
}

func TestResetDeletesOnlyTheSampleEventsKeys(t *testing.T) {
	rdb, _ := newRedis(t)
	ctx := context.Background()
	evs := seed.JakartaEvents()
	if _, err := seed.Redis(ctx, rdb, evs, now); err != nil {
		t.Fatal(err)
	}
	other, _ := rediskeys.NewEvent("evt_someone_elses")
	rdb.HSet(ctx, other.QueueHash(), rediskeys.FieldIssued, 7)
	ev, _ := rediskeys.NewEvent("evt_jkt_istora_nightfall")
	rdb.ZAdd(ctx, ev.ByTicket(), goredis.Z{Score: 1, Member: "qt_a"})

	if err := seed.ResetRedis(ctx, rdb, evs); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{ev.QueueHash(), ev.ByTicket(), ev.Stock("cat_istora_vip")} {
		if rdb.Exists(ctx, k).Val() != 0 {
			t.Errorf("%s survived the reset", k)
		}
	}
	if rdb.HGet(ctx, other.QueueHash(), rediskeys.FieldIssued).Val() != "7" {
		t.Fatal("the reset touched an event it does not own")
	}
	if n, err := seed.Redis(ctx, rdb, evs, now); err != nil || n == 0 {
		t.Fatalf("re-seed after reset created %d, %v", n, err)
	}
}

func TestRedisSeedAndResetReportAnUnreachableRedis(t *testing.T) {
	rdb, mr := newRedis(t)
	mr.Close()
	if _, err := seed.Redis(context.Background(), rdb, seed.JakartaEvents(), now); err == nil {
		t.Error("Redis: want an error")
	}
	if err := seed.ResetRedis(context.Background(), rdb, seed.JakartaEvents()); err == nil {
		t.Error("ResetRedis: want an error")
	}
}

func TestTheGuardOnlyAllowsALocalDatabaseAndRedisUnlessOverridden(t *testing.T) {
	local := "postgres://rusuh:pw@127.0.0.1:15432/rusuh?sslmode=disable"
	cases := []struct {
		name, dsn, addr string
		allow, ok       bool
	}{
		{"local both", local, "127.0.0.1:16379", false, true},
		{"localhost names", "postgres://u@localhost/db", "localhost:6379", false, true},
		{"ipv6 loopback", "postgres://u@[::1]:5432/db", "[::1]:6379", false, true},
		{"redis without a port", local, "127.0.0.1", false, true},
		{"remote database", "postgres://u@aurora.example.com/db", "127.0.0.1:16379", false, false},
		{"remote redis", local, "cache.example.com:6379", false, false},
		{"no host in the url", "not a url", "127.0.0.1:1", false, false},
		{"remote but overridden", "postgres://u@aurora.example.com/db", "cache.example.com:6379", true, true},
	}
	for _, c := range cases {
		if err := seed.CheckLocal(c.dsn, c.addr, c.allow); (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

type fakeDB struct {
	sqls []string
	args [][]any
	fail string // fail when the SQL contains this
}

func (f *fakeDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if f.fail != "" && strings.Contains(sql, f.fail) {
		return pgconn.CommandTag{}, errors.New("db down")
	}
	f.sqls = append(f.sqls, sql)
	f.args = append(f.args, args)
	return pgconn.CommandTag{}, nil
}

func TestPostgresSeedUpsertsEveryEventAndCategoryAndNeverRewritesAvailable(t *testing.T) {
	db := &fakeDB{}
	evs := seed.JakartaEvents()
	if err := seed.Postgres(context.Background(), db, evs); err != nil {
		t.Fatal(err)
	}
	want := len(evs)
	for _, e := range evs {
		want += len(e.Categories)
	}
	if len(db.sqls) != want {
		t.Fatalf("%d statements, want %d", len(db.sqls), want)
	}
	for i, s := range db.sqls {
		if !strings.Contains(s, "ON CONFLICT (id) DO UPDATE") {
			t.Errorf("statement %d is not an upsert", i)
		}
		if strings.Contains(s, "ticket_categories") && strings.Contains(s[strings.Index(s, "DO UPDATE"):], "available") {
			t.Error("the category upsert rewrites available on conflict")
		}
	}
	if db.args[0][0] != "evt_jkt_istora_nightfall" || db.args[1][0] != "cat_istora_vip" || db.args[1][1] != "evt_jkt_istora_nightfall" || db.args[1][8] != 1 {
		t.Fatalf("args = %v / %v", db.args[0], db.args[1])
	}
}

func TestPostgresSeedReportsWhichRowFailed(t *testing.T) {
	err := seed.Postgres(context.Background(), &fakeDB{fail: "INTO ticket_categories"}, seed.JakartaEvents())
	if err == nil || !strings.Contains(err.Error(), "cat_istora_vip") {
		t.Fatalf("err = %v", err)
	}
	err = seed.Postgres(context.Background(), &fakeDB{fail: "INTO events"}, seed.JakartaEvents())
	if err == nil || !strings.Contains(err.Error(), "evt_jkt_istora_nightfall") {
		t.Fatalf("err = %v", err)
	}
	bad := seed.JakartaEvents()[:1]
	bad[0].Date = "tomorrow"
	if err := seed.Postgres(context.Background(), &fakeDB{}, bad); err == nil {
		t.Fatal("a bad date was accepted")
	}
}
