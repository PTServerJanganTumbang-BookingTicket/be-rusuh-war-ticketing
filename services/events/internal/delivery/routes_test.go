package delivery_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/pgtest"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/delivery"
)

// The whole service on real Postgres (make verify) and miniredis: both routes, end to end.
func TestRoutesServeTheHomePageAndTheDetailPage(t *testing.T) {
	pool := pgtest.New(t)
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	for _, q := range []string{
		`INSERT INTO events (id, name, event_date, sale_starts_at) VALUES ('open', 'Open', '2026-12-25', '2026-11-10T02:00:00Z'),
		  ('closed', 'Closed', '2026-10-01', '2026-09-01T02:00:00Z'), ('soon', 'Soon', '2027-02-01', NULL)`,
		`INSERT INTO ticket_categories (id, event_id, name, price, capacity, available) VALUES ('vip', 'open', 'VIP', 1500000, 100, 12)`,
	} {
		if _, err := pool.Exec(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	open, _ := rediskeys.NewEvent("open")
	mr.HSet(open.QueueHash(), "state", "FULL_BOOKED", "issued", "5", "now_serving", "5")
	_ = mr.Set(open.Stock("vip"), "0")
	closed, _ := rediskeys.NewEvent("closed")
	mr.HSet(closed.QueueHash(), "state", "CLOSED")

	h := delivery.NewHandler(config.Config{Events: config.Events{QueueCapacity: 100, CacheMaxAgeSeconds: 30, DegradedMaxAge: 5, TurnstileSiteKey: "sk"}},
		delivery.Deps{Redis: rdb, DB: pool})

	get := func(path string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	code, body := get("/events")
	items := body["data"].([]any)
	if code != 200 || len(items) != 3 {
		t.Fatalf("list: %d %v", code, body)
	}
	got := []string{}
	for _, it := range items { // newest event date first: soon (2027), open (2026-12), closed (2026-10)
		m := it.(map[string]any)
		got = append(got, m["id"].(string)+":"+m["queue_state"].(string))
	}
	if got[0] != "soon:SCHEDULED" || got[1] != "open:FULL_BOOKED" || got[2] != "closed:CLOSED" {
		t.Fatalf("list = %v", got)
	}

	code, body = get("/events?limit=1&offset=1")
	if one := body["data"].([]any); code != 200 || len(one) != 1 || one[0].(map[string]any)["id"] != "open" ||
		body["meta"].(map[string]any)["pagination"].(map[string]any)["totalData"] != float64(3) {
		t.Fatalf("page 2: %d %v", code, body)
	}

	code, body = get("/events/open")
	d := body["data"].(map[string]any)
	cat := d["ticket_categories"].([]any)[0].(map[string]any)
	if code != 200 || d["queue_state"] != "FULL_BOOKED" || d["display"].(map[string]any)["enabled"] != true || cat["status"] != "SOLD_OUT" {
		t.Fatalf("detail: %d %v", code, d)
	}
	if code, _ = get("/events/nope"); code != 404 {
		t.Fatalf("unknown event: %d", code)
	}
	if code, _ = get("/sys/ping"); code != 200 {
		t.Fatalf("health: %d", code)
	}
}
