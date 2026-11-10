package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
)

type stubDetails struct{}

func (stubDetails) Detail(context.Context, string, string) (domain.BookingDetail, bool, error) {
	return domain.BookingDetail{ID: "RB-1", EventID: "evt_a", Status: bookingstate.Held,
		PaymentExpiresAt: time.Date(2026, 11, 10, 19, 12, 0, 0, time.UTC),
		Event:            domain.Event{ID: "evt_a", Name: "Konser A", Date: "2026-12-25"},
		Items:            []domain.Item{{ID: "it_1", CategoryName: "VIP", UnitPrice: 1500000, NIKMasked: "3171********0001"}}}, true, nil
}

type stubLinks struct{}

func (stubLinks) Mint(string, time.Time) (string, error) { return "tok", nil }

func detailHandler() *BookingDetail {
	return NewBookingDetail(usecase.NewBookingDetail(usecase.BookingDetailDeps{Details: stubDetails{}, Links: stubLinks{}, Metrics: noMetrics{},
		Clock: func() time.Time { return time.Date(2026, 11, 10, 19, 2, 0, 0, time.UTC) }}))
}

func TestTheBookingDetailAnswersInTheContractEnvelope(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/bookings/RB-1", nil)
	req.SetPathValue("booking_id", "RB-1")
	g := grantauth.Grant{Token: "gt_1", EventID: "evt_a", BookingID: "RB-1", QueueToken: "qt_1", DeviceHash: "h"}
	req = req.WithContext(grantauth.WithGrant(req.Context(), g))
	rec := httptest.NewRecorder()
	detailHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Meta struct {
			Code int `json:"code"`
		} `json:"meta"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.Meta.Code != 200 {
		t.Fatalf("body %s (%v)", rec.Body.String(), err)
	}
	for _, k := range []string{"booking_id", "status", "event", "tickets", "summary", "payment_ttl"} {
		if _, ok := body.Data[k]; !ok {
			t.Fatalf("data has no %q: %s", k, rec.Body.String())
		}
	}
}

func TestTheBookingDetailHandlerRefusesARequestThatBypassedTheMiddleware(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/bookings/RB-1", nil)
	req.SetPathValue("booking_id", "RB-1")
	rec := httptest.NewRecorder()
	detailHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401 when no grant is in the context", rec.Code)
	}
}
