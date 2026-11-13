package admission_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/infrastructure/postgres"
	redisadapter "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/admission-ctl/internal/admission/infrastructure/redis"
)

// The HELD → EXPIRED sweep with the real Postgres store, the real Redis adapters (miniredis runs the real Lua) and the
// real use case. Skipped without DATABASE_URL, like the other Postgres tests.

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

type countMetrics struct {
	mu     sync.Mutex
	counts map[string]int
}

func (m *countMetrics) Inc(name string)                    { m.mu.Lock(); m.counts[name]++; m.mu.Unlock() }
func (m *countMetrics) Gauge(string, float64)              {}
func (m *countMetrics) IncEvent(name, _ string)            { m.Inc(name) }
func (m *countMetrics) GaugeEvent(string, string, float64) {}

func TestAnExpiredHoldGivesItsSeatsNIKsAndGrantBackAndLeavesAPaymentInFlightAlone(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.New(t)
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	ev, _ := rediskeys.NewEvent("E1")
	now := time.Date(2026, 11, 10, 19, 0, 0, 0, time.UTC)

	insert := func(id, status, grantToken string, due time.Time, seats ...[2]string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `INSERT INTO bookings (id, event_id, queue_token, status, grant_token, contact_email, booking_expires_at, held_at, payment_expires_at)
			VALUES ($1, 'E1', 'qt-'||$1, $2, $3, 'a@example.com', $4, $5, $6)`, id, status, grantToken, due.Add(-time.Hour), due.Add(-10*time.Minute), due); err != nil {
			t.Fatal(err)
		}
		for i, s := range seats {
			if _, err := pool.Exec(ctx, `INSERT INTO booking_items (id, booking_id, event_id, category_id, category_name, unit_price, nik_hmac, nik_masked)
				VALUES ($1, $2, 'E1', $3, $3, 1000, $4, 'm')`, id+"-"+string(rune('a'+i)), id, s[0], s[1]); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO nik_claims (event_id, nik_hmac, booking_id) VALUES ('E1', $1, $2)`, s[1], id); err != nil {
				t.Fatal(err)
			}
			_ = mr.Set(ev.NIKUsed(s[1]), "1")
		}
		mr.HSet(rediskeys.Grant(grantToken), "event_id", "E1", "booking_id", id)
	}
	insert("held", "HELD", "gt-held", now.Add(-time.Second), [2]string{"VIP", "n1"}, [2]string{"VIP", "n2"}, [2]string{"FEST", "n3"})
	insert("paying", "AWAITING_PAYMENT", "gt-paying", now.Add(-time.Hour), [2]string{"VIP", "n9"})
	_ = mr.Set(ev.Stock("VIP"), "2")  // 3 VIP seats were held: 2 VIP of "held" and 1 of "paying", so 5 - 3
	_ = mr.Set(ev.Stock("FEST"), "4") // 1 FEST seat held by "held"

	m := &countMetrics{counts: map[string]int{}}
	bookings := postgres.NewBookings(pool)
	tick := usecase.NewTick(usecase.DefaultConfig(), bookings, redisadapter.NewQueue(rdb), fixedClock{now}, m).
		WithHeldSweep(bookings, redisadapter.NewHolds(rdb))
	if n := tick.Sweep(ctx); n != 1 {
		t.Fatalf("swept %d, want 1 (the payment in flight is not ours)", n)
	}

	var status string
	_ = pool.QueryRow(ctx, `SELECT status FROM bookings WHERE id = 'held'`).Scan(&status)
	if status != "EXPIRED" {
		t.Fatalf("held is %s, want EXPIRED", status)
	}
	if v, _ := mr.Get(ev.Stock("VIP")); v != "4" {
		t.Fatalf("VIP stock = %s, want 2 + the 2 seats of the expired hold", v)
	}
	if v, _ := mr.Get(ev.Stock("FEST")); v != "5" {
		t.Fatalf("FEST stock = %s, want 4 + 1", v)
	}
	for _, n := range []string{"n1", "n2", "n3"} {
		if mr.Exists(ev.NIKUsed(n)) {
			t.Fatalf("NIK lock %s still held after the hold expired", n)
		}
	}
	if mr.Exists(rediskeys.Grant("gt-held")) {
		t.Fatal("the grant of an expired booking must be deleted")
	}
	// the payment in flight keeps everything
	_ = pool.QueryRow(ctx, `SELECT status FROM bookings WHERE id = 'paying'`).Scan(&status)
	if status != "AWAITING_PAYMENT" || !mr.Exists(ev.NIKUsed("n9")) || !mr.Exists(rediskeys.Grant("gt-paying")) {
		t.Fatalf("paying: status %s, its NIK lock and grant must stay", status)
	}
	var claims int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM nik_claims`).Scan(&claims)
	if claims != 1 || m.counts[usecase.MetricHeldExpired] != 1 || m.counts[usecase.MetricReleaseFailed] != 0 {
		t.Fatalf("claims left %d, metrics %v, want only the payment in flight's claim", claims, m.counts)
	}
}
