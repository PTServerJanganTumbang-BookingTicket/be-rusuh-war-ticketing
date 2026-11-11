package delivery_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/delivery"
)

// POST /bookings end to end: the real handler, use case, Postgres store and Lua scripts (miniredis runs the real
// script). Only the NIK check is the local fake (a NIK ending 0000 is rejected, 9999 is an outage). Skipped without
// DATABASE_URL, like the other Postgres tests.

type e2e struct {
	t    *testing.T
	h    http.Handler
	pool *pgxpool.Pool
	mr   *miniredis.Miniredis
	ev   rediskeys.Event
}

const e2ePepper = "e2e-pepper"
const e2eDevice = "e2e-device-0001"

func newE2E(t *testing.T) *e2e {
	t.Helper()
	pool := pgtest.New(t)
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	cfg := config.Config{
		App:       config.App{Env: "local"},
		Grant:     config.Grant{DevicePepper: []byte(e2ePepper), JWTSecret: "e2e-jwt"},
		Booking:   config.Booking{MaxTicketsPerTransaction: 3, NIKLockTTL: 6 * time.Hour},
		NIKVerify: config.NIKVerify{Timeout: time.Second},
	}
	h, err := delivery.NewHandler(cfg, delivery.Deps{Redis: rdb, DB: pool})
	if err != nil {
		t.Fatal(err)
	}
	ev, _ := rediskeys.NewEvent("evt_a")
	e := &e2e{t: t, h: h, pool: pool, mr: mr, ev: ev}
	for _, q := range []string{
		`INSERT INTO events (id, name, event_date, venue, image_url) VALUES ('evt_a', 'Konser A', '2026-12-25', 'GBK', 'https://cdn/a.jpg')`,
		`INSERT INTO ticket_categories (id, event_id, name, description, price, capacity, available, max_per_order, sort_order) VALUES
		   ('cat_vip', 'evt_a', 'VIP', 'Dekat', 1500000, 10, 10, 3, 1),
		   ('cat_fest', 'evt_a', 'Festival', '', 850000, 10, 10, 3, 2)`,
	} {
		if _, err := pool.Exec(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	e.stock(5, 5)
	mr.HSet(ev.QueueHash(), "state", "OPEN")
	return e
}

func (e *e2e) stock(vip, fest int) {
	_ = e.mr.Set(e.ev.Stock("cat_vip"), itoa(vip))
	_ = e.mr.Set(e.ev.Stock("cat_fest"), itoa(fest))
}

func itoa(n int) string { return strconv.Itoa(n) }

// booking seeds a SELECTING booking and the grant hash of a guest for it.
func (e *e2e) booking(id string) {
	e.t.Helper()
	_, err := e.pool.Exec(e.t.Context(), `INSERT INTO bookings (id, event_id, queue_token, status, grant_token, granted_at, booking_expires_at)
		VALUES ($1, 'evt_a', 'qt_'||$1, 'SELECTING', 'gt_'||$1, now(), now() + interval '4 minutes')`, id)
	if err != nil {
		e.t.Fatal(err)
	}
	e.mr.HSet(rediskeys.Grant("gt_"+id), rediskeys.FieldEventID, "evt_a", rediskeys.FieldBookingID, id,
		rediskeys.FieldQueueToken, "qt_"+id, rediskeys.FieldDeviceHash, grantauth.DeviceHash([]byte(e2ePepper), e2eDevice))
}

func (e *e2e) post(id, body string) (int, map[string]any) {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/bookings", strings.NewReader(body))
	req.Header.Set("X-Grant-Token", "gt_"+id)
	req.Header.Set("X-Device-Id", e2eDevice)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		e.t.Fatalf("not JSON: %s", rec.Body.String())
	}
	return rec.Code, out
}

func order(seats ...string) string {
	return `{"event_id":"evt_a","contact_email":"buyer@example.com","tickets":[` + strings.Join(seats, ",") + `]}`
}

func seat(cat, nik string) string { return `{"category_id":"` + cat + `","nik":"` + nik + `"}` }

func errCode(body map[string]any) string {
	m, _ := body["meta"].(map[string]any)
	s, _ := m["error_code"].(string)
	return s
}

func data(body map[string]any) map[string]any {
	d, _ := body["data"].(map[string]any)
	return d
}

func (e *e2e) statusOf(id string) string {
	var s string
	_ = e.pool.QueryRow(e.t.Context(), `SELECT status FROM bookings WHERE id=$1`, id).Scan(&s)
	return s
}

func (e *e2e) stockOf(cat string) string { v, _ := e.mr.Get(e.ev.Stock(cat)); return v }

func TestAnOrderIsHeldEndToEndAndARetryAnswersWithTheSameBooking(t *testing.T) {
	e := newE2E(t)
	e.booking("RB-1")
	body := order(seat("cat_vip", "3171 0000 0000 0022"), seat("cat_fest", "3171000000000033"), seat("cat_vip", "3171000000000044"))
	code, out := e.post("RB-1", body)
	if code != http.StatusCreated {
		t.Fatalf("status %d: %v", code, out)
	}
	d := data(out)
	tickets, _ := d["tickets"].([]any)
	summary, _ := d["summary"].(map[string]any)
	if d["booking_id"] != "RB-1" || d["status"] != "PENDING_PAYMENT" || len(tickets) != 3 || summary["total"] != float64(1500000+850000+1500000) {
		t.Fatalf("201 body = %v, want the GET /bookings/{id} shape with 3 seats", d)
	}
	if first, _ := tickets[0].(map[string]any); first["category"] != "VIP" || first["nik_masked"] == "" {
		t.Fatalf("first seat = %v, want the seats in the order they were sent", tickets[0])
	}
	ttl, _ := d["payment_ttl"].(map[string]any)
	if exp, err := time.Parse(time.RFC3339, ttl["expires_at"].(string)); err != nil || time.Until(exp) < 9*time.Minute || time.Until(exp) > 10*time.Minute+5*time.Second {
		t.Fatalf("payment_ttl = %v, want about 10 minutes from now", ttl)
	}
	if e.statusOf("RB-1") != "HELD" || e.stockOf("cat_vip") != "3" || e.stockOf("cat_fest") != "4" {
		t.Fatalf("status=%s vip=%s fest=%s, want HELD with 2 VIP and 1 Festival taken", e.statusOf("RB-1"), e.stockOf("cat_vip"), e.stockOf("cat_fest"))
	}

	// a double click, even with another body, answers 200 with the same booking and holds nothing more
	code, again := e.post("RB-1", `not json`)
	if code != http.StatusOK || data(again)["booking_id"] != "RB-1" || e.stockOf("cat_vip") != "3" {
		t.Fatalf("retry = %d %v vip=%s, want 200, the same booking and no second hold", code, again, e.stockOf("cat_vip"))
	}
}

func TestNIKsCheckedByNikVerifyAreRefusedWithTheirSeatAndNothingIsHeld(t *testing.T) {
	e := newE2E(t)
	e.booking("RB-1")
	code, out := e.post("RB-1", order(seat("cat_vip", "3171000000000022"), seat("cat_vip", "3171000000000000")))
	if code != http.StatusBadRequest || errCode(out) != "NIK_NOT_FOUND" || data(out)["index"] != float64(1) {
		t.Fatalf("rejected NIK = %d %v, want 400 NIK_NOT_FOUND at seat 1", code, out)
	}
	code, out = e.post("RB-1", order(seat("cat_vip", "3171000000009999")))
	if code != http.StatusServiceUnavailable || errCode(out) != "DUKCAPIL_UNAVAILABLE" {
		t.Fatalf("outage = %d %v, want 503 DUKCAPIL_UNAVAILABLE", code, out)
	}
	if e.statusOf("RB-1") != "SELECTING" || e.stockOf("cat_vip") != "5" {
		t.Fatalf("status=%s vip=%s, want SELECTING and nothing held", e.statusOf("RB-1"), e.stockOf("cat_vip"))
	}
}

func TestTheSameNIKInTwoBookingsIsRefusedTheSecondTimeAndTakesNoStock(t *testing.T) {
	e := newE2E(t)
	e.booking("RB-1")
	e.booking("RB-2")
	if code, out := e.post("RB-1", order(seat("cat_vip", "3171000000000022"))); code != http.StatusCreated {
		t.Fatalf("first: %d %v", code, out)
	}
	code, out := e.post("RB-2", order(seat("cat_fest", "3171 0000 0000 0022")))
	if code != http.StatusConflict || errCode(out) != "NIK_ALREADY_USED" || data(out)["index"] != float64(0) {
		t.Fatalf("second = %d %v, want 409 NIK_ALREADY_USED at seat 0", code, out)
	}
	if e.statusOf("RB-2") != "SELECTING" || e.stockOf("cat_fest") != "5" {
		t.Fatalf("status=%s fest=%s, want the second booking still SELECTING and no stock taken", e.statusOf("RB-2"), e.stockOf("cat_fest"))
	}
}

func TestPartlyAndTotallySoldOutAreToldApart(t *testing.T) {
	e := newE2E(t)
	e.booking("RB-1")
	e.stock(1, 5) // VIP has one seat, Festival has five
	code, out := e.post("RB-1", order(seat("cat_vip", "3171000000000022"), seat("cat_vip", "3171000000000033")))
	d := data(out)
	if code != http.StatusConflict || errCode(out) != "INSUFFICIENT_STOCK" || d["category_id"] != "cat_vip" || d["available"] != float64(1) || d["requested"] != float64(2) {
		t.Fatalf("partly sold out = %d %v", code, out)
	}
	if e.stockOf("cat_vip") != "1" || e.statusOf("RB-1") != "SELECTING" {
		t.Fatal("a refused hold must change nothing")
	}

	e.stock(0, 0)
	code, out = e.post("RB-1", order(seat("cat_vip", "3171000000000022")))
	d = data(out)
	// the queue hash still says OPEN (one tick behind the stock): the answer is FULL_BOOKED, never OPEN
	if code != http.StatusConflict || errCode(out) != "TICKETS_UNAVAILABLE" || d["queue_state"] != "FULL_BOOKED" || d["full_booked_at"] != nil {
		t.Fatalf("totally sold out = %d %v", code, out)
	}
	e.mr.HSet(e.ev.QueueHash(), "state", "CLOSED", "full_booked_at", "1794000000")
	if _, out = e.post("RB-1", order(seat("cat_vip", "3171000000000022"))); data(out)["queue_state"] != "CLOSED" || data(out)["full_booked_at"] == nil {
		t.Fatalf("closed = %v, want CLOSED with the time", out)
	}
}

func TestAnOrderWithAMissingStockKeyIsUninitializedNotSoldOut(t *testing.T) {
	e := newE2E(t)
	e.booking("RB-1")
	e.mr.Del(e.ev.Stock("cat_fest"))
	code, out := e.post("RB-1", order(seat("cat_vip", "3171000000000022")))
	if code != http.StatusCreated {
		// vip alone has stock: held. The missing key only matters to an order that includes it.
		t.Fatalf("vip only: %d %v", code, out)
	}
	e.booking("RB-2")
	code, out = e.post("RB-2", order(seat("cat_fest", "3171000000000033")))
	if code != http.StatusServiceUnavailable || errCode(out) != "STOCK_UNINITIALIZED" {
		t.Fatalf("missing key = %d %v, want 503 STOCK_UNINITIALIZED", code, out)
	}
}
