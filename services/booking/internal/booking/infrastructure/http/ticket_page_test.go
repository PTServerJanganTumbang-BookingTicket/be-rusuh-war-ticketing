package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goredis "github.com/redis/go-redis/v9"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/rediskeys"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/token"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
)

type stubBookings struct{}

func (stubBookings) ClaimForSelection(context.Context, string, string) (domain.Booking, bool, error) {
	return domain.Booking{ID: "RB-1", EventID: "evt_a", Status: bookingstate.Selecting,
		BookingExpiresAt: time.Date(2026, 11, 10, 19, 5, 0, 0, time.UTC)}, true, nil
}

type stubCatalog struct{}

func (stubCatalog) Page(context.Context, string, string) (domain.Event, []domain.Category, *domain.Account, bool, error) {
	return domain.Event{ID: "evt_a", Name: "Konser A", Date: "2026-12-25", Venue: "GBK", ImageURL: "https://cdn/a.jpg"},
		[]domain.Category{{ID: "cat_vip", Name: "VIP", Price: 1500000, MaxPerOrder: 3}}, nil, true, nil
}

type stubStock struct{}

func (stubStock) Levels(context.Context, string, []string) (map[string]int64, []string, error) {
	return map[string]int64{"cat_vip": 12}, nil, nil
}

// noSessions is never asked: this test uses a guest grant, which ignores Authorization.
type noSessions struct{}

func (noSessions) Verify(string) (token.Claims, error) { return token.Claims{}, token.ErrInvalid }

type noMetrics struct{}

func (noMetrics) Incr(string)              {}
func (noMetrics) IncrEvent(string, string) {}

// The handler behind the real middleware: what a client sees for a valid guest grant.
func TestTheTicketPageAnswersInTheContractEnvelope(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := goredis.NewClient(&goredis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	pepper := []byte("p")
	mr.HSet(rediskeys.Grant("gt_1"), rediskeys.FieldEventID, "evt_a", rediskeys.FieldBookingID, "RB-1",
		rediskeys.FieldQueueToken, "qt_1", rediskeys.FieldDeviceHash, grantauth.DeviceHash(pepper, "device-0001"))

	uc := usecase.NewTicketPage(usecase.TicketPageDeps{Bookings: stubBookings{}, Catalog: stubCatalog{}, Stock: stubStock{},
		Metrics: noMetrics{}, Clock: func() time.Time { return time.Date(2026, 11, 10, 19, 2, 0, 0, time.UTC) }}, 3)
	mux := http.NewServeMux()
	mux.Handle("GET /events/{event_id}/tickets", grantauth.Middleware(rdb, grantauth.Config{DevicePepper: pepper, Tokens: noSessions{}})(NewTicketPage(uc)))

	req := httptest.NewRequest(http.MethodGet, "/events/evt_a/tickets", nil)
	req.Header.Set(grantauth.HeaderGrantToken, "gt_1")
	req.Header.Set(grantauth.HeaderDeviceID, "device-0001")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Meta struct {
			Code       int    `json:"code"`
			Message    string `json:"message"`
			ServerTime string `json:"server_time"`
		} `json:"meta"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Meta.Code != 200 || body.Meta.Message != "Success" || body.Meta.ServerTime == "" {
		t.Fatalf("meta %+v", body.Meta)
	}
	for _, k := range []string{"event", "session", "categories", "rules", "account"} {
		if _, ok := body.Data[k]; !ok {
			t.Fatalf("data has no %q: %s", k, rec.Body.String())
		}
	}
	if string(body.Data["account"]) != "null" {
		t.Fatalf("account = %s, want null for a guest grant (contract §4.1)", body.Data["account"])
	}
}

func TestTheTicketPageHandlerRefusesARequestThatBypassedTheMiddleware(t *testing.T) {
	uc := usecase.NewTicketPage(usecase.TicketPageDeps{Bookings: stubBookings{}, Catalog: stubCatalog{}, Stock: stubStock{}, Metrics: noMetrics{}, Clock: time.Now}, 3)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/events/evt_a/tickets", nil)
	req.SetPathValue("event_id", "evt_a")
	NewTicketPage(uc).ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401 when no grant is in the context", rec.Code)
	}
}
