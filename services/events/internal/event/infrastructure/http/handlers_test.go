package http_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/domain"
	eventhttp "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/events/internal/event/infrastructure/http"
)

type events struct {
	list []domain.Event
	cats []domain.Category
	err  error
}

func (e events) List(_ context.Context, limit, offset int) ([]domain.Event, int, error) {
	return e.list, len(e.list), e.err
}
func (e events) Get(_ context.Context, id string) (domain.Event, []domain.Category, bool, error) {
	if e.err != nil || len(e.list) == 0 || e.list[0].ID != id {
		return domain.Event{}, nil, false, e.err
	}
	return e.list[0], e.cats, true, nil
}

type live struct{ err error }

func (l live) Queues(_ context.Context, ids []string) (map[string]domain.Queue, error) {
	out := map[string]domain.Queue{}
	for _, id := range ids {
		out[id] = domain.Queue{State: "OPEN"}
	}
	return out, l.err
}
func (l live) Stock(context.Context, string, []string) (map[string]int64, error) { return nil, l.err }

type metrics struct{}

func (metrics) Incr(string) {}

var caching = eventhttp.Caching{MaxAge: 30, DegradedMaxAge: 5}

func server(e events, l live) http.Handler {
	d := usecase.Deps{Events: e, Live: l, Metrics: metrics{}, Clock: time.Now}
	cfg := usecase.Config{QueueCapacity: 100, CaptchaSiteKey: "sk"}
	mux := http.NewServeMux()
	mux.Handle("GET /events", eventhttp.NewListEvents(usecase.NewList(d, cfg), caching))
	mux.Handle("GET /events/{event_id}", eventhttp.NewGetEvent(usecase.NewGet(d, cfg), caching))
	return mux
}

func do(h http.Handler, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

var one = events{
	list: []domain.Event{{ID: "e1", Name: "Konser A", Category: "LIVE MUSIC", Venue: "GBK", City: "Jakarta",
		Date: time.Date(2026, 12, 25, 0, 0, 0, 0, time.UTC), Time: "19:00", ImageURL: "https://x/y.jpg", Description: "d"}},
	cats: []domain.Category{{ID: "vip", Name: "VIP", Description: "close", Price: 1500000, Available: 12}},
}

func TestListQueryValidation(t *testing.T) {
	for _, q := range []string{"limit=abc", "limit=0", "limit=51", "limit=-1", "offset=-1", "offset=x"} {
		rec := do(server(one, live{}), "/events?"+q)
		var body struct {
			Meta struct {
				ErrorCode string `json:"error_code"`
			}
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		if rec.Code != 400 || body.Meta.ErrorCode != "VALIDATION_ERROR" {
			t.Errorf("%s: %d %s", q, rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Cache-Control"); got != "public, max-age=30" {
			t.Errorf("%s: Cache-Control = %q (a bad query may be cached)", q, got)
		}
	}
}

func TestListAnswersTheContractShapeWithPagination(t *testing.T) {
	rec := do(server(one, live{}), "/events?limit=10&offset=0&filter=available") // an old client's filter is ignored
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=30" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	var body struct {
		Meta struct {
			Pagination struct{ TotalData, TotalPage, Limit, Offset int } `json:"pagination"`
			ServerTime string                                            `json:"server_time"`
		}
		Data []map[string]any
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Meta.Pagination.TotalData != 1 || body.Meta.Pagination.Limit != 10 || body.Meta.ServerTime == "" || len(body.Data) != 1 {
		t.Fatalf("body = %s", rec.Body.String())
	}
	item := body.Data[0]
	if item["queue_state"] != "OPEN" || item["date"] != "2026-12-25" || item["sale_starts_at"] != nil ||
		item["display"].(map[string]any)["enabled"] != true || item["location"].(map[string]any)["city"] != "Jakarta" {
		t.Fatalf("item = %v", item)
	}
	for _, removed := range []string{"status", "queue_depth", "queue_capacity", "min_price", "max_price"} {
		if _, has := item[removed]; has {
			t.Errorf("field %q is no longer in the response", removed)
		}
	}
}

func TestListWhenRedisIsDownIsCachedBrieflyAndWhenAuroraIsDownNotAtAll(t *testing.T) {
	rec := do(server(one, live{err: errors.New("down")}), "/events")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=5" {
		t.Fatalf("redis down: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	rec = do(server(events{err: errors.New("down")}, live{}), "/events")
	if rec.Code != 503 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("aurora down: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestDetailAnswersTheContractShape(t *testing.T) {
	rec := do(server(one, live{}), "/events/e1")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=30" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	var body struct{ Data map[string]any }
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	d := body.Data
	cats := d["ticket_categories"].([]any)
	if d["time"] != "19:00" || d["queue_state"] != "OPEN" || len(cats) != 1 ||
		cats[0].(map[string]any)["status"] != "AVAILABLE" || d["captcha"].(map[string]any)["site_key"] != "sk" {
		t.Fatalf("data = %v", d)
	}
}

func TestDetailNotFoundIsCachedAndAuroraDownIsNot(t *testing.T) {
	rec := do(server(one, live{}), "/events/nope")
	if rec.Code != 404 || rec.Header().Get("Cache-Control") != "public, max-age=30" {
		t.Fatalf("404: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	rec = do(server(events{err: errors.New("down")}, live{}), "/events/e1")
	if rec.Code != 503 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("503: %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}

func TestDetailWhenRedisIsDownIsCachedBriefly(t *testing.T) {
	rec := do(server(one, live{err: errors.New("down")}), "/events/e1")
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=5" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
}
