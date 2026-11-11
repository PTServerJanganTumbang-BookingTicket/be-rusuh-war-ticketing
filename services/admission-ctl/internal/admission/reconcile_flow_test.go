package admission_test

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/infrastructure/postgres"
	redisadapter "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/infrastructure/redis"
)

// Stock reconciliation with the real Postgres store, the real Redis adapters and the real use case. Skipped without
// DATABASE_URL, like the other Postgres tests.

type reconRig struct {
	pool *pgxpool.Pool
	mr   *miniredis.Miniredis
	ev   rediskeys.Event
	m    *countMetrics
	rec  *usecase.Reconciler
	ctx  context.Context
}

// newReconRig seeds event E1: VIP (capacity 10) and FEST (capacity 20), one HELD booking with 2 VIP seats and their 2
// NIK claims. So Redis should hold VIP 8, FEST 20, and nik:used for n1 and n2.
func newReconRig(t *testing.T) *reconRig {
	t.Helper()
	ctx := context.Background()
	pool := pgtest.New(t)
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ev, _ := rediskeys.NewEvent("E1")
	for _, q := range []string{
		`INSERT INTO events (id, name, event_date, venue, image_url) VALUES ('E1', 'A', '2026-12-25', 'v', 'u')`,
		`INSERT INTO ticket_categories (id, event_id, name, price, capacity, available, sort_order) VALUES
		   ('VIP', 'E1', 'VIP', 1500000, 10, 10, 1), ('FEST', 'E1', 'Festival', 850000, 20, 20, 2)`,
		`INSERT INTO bookings (id, event_id, queue_token, status, grant_token, contact_email, booking_expires_at, held_at, payment_expires_at)
		   VALUES ('B1', 'E1', 'qt1', 'HELD', 'gt1', 'a@example.com', now() + interval '1 hour', now(), now() + interval '10 minutes')`,
		`INSERT INTO booking_items (id, booking_id, event_id, category_id, category_name, unit_price, nik_hmac, nik_masked) VALUES
		   ('i1', 'B1', 'E1', 'VIP', 'VIP', 1500000, 'n1', 'm'), ('i2', 'B1', 'E1', 'VIP', 'VIP', 1500000, 'n2', 'm')`,
		`INSERT INTO nik_claims (event_id, nik_hmac, booking_id) VALUES ('E1', 'n1', 'B1'), ('E1', 'n2', 'B1')`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	_ = mr.Set(ev.Stock("VIP"), "8")
	_ = mr.Set(ev.Stock("FEST"), "20")
	for _, h := range []string{"n1", "n2"} {
		_ = mr.Set(ev.NIKUsed(h), "1")
		mr.SetTTL(ev.NIKUsed(h), 6*time.Hour)
	}
	m := &countMetrics{counts: map[string]int{}}
	bookings := postgres.NewBookings(pool)
	rec := usecase.NewReconciler(usecase.DefaultReconcileConfig(), bookings, redisadapter.NewRepair(rdb), m)
	return &reconRig{pool: pool, mr: mr, ev: ev, m: m, rec: rec, ctx: ctx}
}

func (g *reconRig) stock(cat string) string { v, _ := g.mr.Get(g.ev.Stock(cat)); return v }

func TestAHealthyEventIsLeftAloneRunAfterRun(t *testing.T) {
	g := newReconRig(t)
	for i := 0; i < 4; i++ {
		g.rec.Run(g.ctx, "E1")
	}
	if g.stock("VIP") != "8" || g.stock("FEST") != "20" || len(g.m.counts) != 0 {
		t.Fatalf("VIP %s FEST %s metrics %v, want nothing touched and nothing counted", g.stock("VIP"), g.stock("FEST"), g.m.counts)
	}
}

func TestEachLeakIsRepairedFromAuroraWithinTheRightNumberOfRuns(t *testing.T) {
	g := newReconRig(t)
	g.mr.Del(g.ev.Stock("FEST"))           // a lost stock key: the category answers STOCK_UNINITIALIZED
	_ = g.mr.Set(g.ev.Stock("VIP"), "5")   // 3 seats leaked: a failed compensation or release left them out of sale
	g.mr.Del(g.ev.NIKUsed("n1"))           // a lost NIK lock for a claim that exists
	_ = g.mr.Set(g.ev.NIKUsed("n99"), "1") // a lock with no claim: maybe a hold that has not committed yet

	g.rec.Run(g.ctx, "E1")
	if g.stock("FEST") != "20" {
		t.Fatalf("FEST = %s after one run, want the lost key restored at once to 20", g.stock("FEST"))
	}
	if g.stock("VIP") != "5" || g.mr.Exists(g.ev.NIKUsed("n1")) {
		t.Fatalf("VIP %s: a leak and a lost lock are seen once, and a single sighting proves nothing", g.stock("VIP"))
	}

	g.rec.Run(g.ctx, "E1")
	if g.stock("VIP") != "8" {
		t.Fatalf("VIP = %s after two runs, want the 3 leaked seats given back (8)", g.stock("VIP"))
	}
	if !g.mr.Exists(g.ev.NIKUsed("n1")) || g.mr.TTL(g.ev.NIKUsed("n1")) <= 0 {
		t.Fatal("the lost NIK lock must be back, with an expiry")
	}
	if !g.mr.Exists(g.ev.NIKUsed("n99")) {
		t.Fatal("a lock with no claim must never be deleted")
	}
	if g.m.counts[usecase.MetricStockKeyRestored] != 1 || g.m.counts[usecase.MetricStockAdjusted] != 1 || g.m.counts[usecase.MetricNikClaimRestored] != 1 {
		t.Fatalf("metrics %v", g.m.counts)
	}

	// and it stays repaired
	g.rec.Run(g.ctx, "E1")
	if g.stock("VIP") != "8" || g.stock("FEST") != "20" {
		t.Fatalf("VIP %s FEST %s after a third run", g.stock("VIP"), g.stock("FEST"))
	}
}

// A hold lowers Redis before Aurora commits, so a sale in flight looks like a leak for a moment. It must not be "corrected":
// that would sell a seat twice.
func TestASaleInFlightIsNotMistakenForALeak(t *testing.T) {
	g := newReconRig(t)
	_ = g.mr.Set(g.ev.Stock("VIP"), "7") // one seat taken in Redis whose booking has not committed yet
	g.rec.Run(g.ctx, "E1")
	_ = g.mr.Set(g.ev.Stock("VIP"), "8") // it resolved (rolled back and released) before the next run
	g.rec.Run(g.ctx, "E1")
	if g.stock("VIP") != "8" || g.m.counts[usecase.MetricStockAdjusted] != 0 {
		t.Fatalf("VIP %s metrics %v, want no correction for a shortfall that was not there on the second run", g.stock("VIP"), g.m.counts)
	}
}

func TestTooManySeatsInRedisAreTakenAwayAfterTwoRuns(t *testing.T) {
	g := newReconRig(t)
	_ = g.mr.Set(g.ev.Stock("VIP"), "11") // 3 too many: a double release
	g.rec.Run(g.ctx, "E1")
	if g.stock("VIP") != "11" {
		t.Fatalf("VIP = %s after one run, want it untouched", g.stock("VIP"))
	}
	g.rec.Run(g.ctx, "E1")
	if g.stock("VIP") != "8" {
		t.Fatalf("VIP = %s after two runs, want the excess taken away (8)", g.stock("VIP"))
	}
}

func TestACapacityChangedMidSaleIsAlarmedAndNeverCorrected(t *testing.T) {
	g := newReconRig(t)
	if _, err := g.pool.Exec(g.ctx, `UPDATE ticket_categories SET capacity = 500 WHERE id = 'VIP'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		g.rec.Run(g.ctx, "E1")
	}
	if g.stock("VIP") != "8" || g.m.counts[usecase.MetricStockDriftTooLarge] != 3 {
		t.Fatalf("VIP %s metrics %v, want an alarm every run and no correction", g.stock("VIP"), g.m.counts)
	}
}
