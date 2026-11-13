package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/bookingstate"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/application/usecase"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/domain"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/port/outbound"
)

// ---- stubs for the use case's ports

type stubOrders struct {
	status domain.BookingStatus
	placed bool
}

func (s *stubOrders) Status(context.Context, string, string) (domain.BookingStatus, bool, error) {
	return s.status, true, nil
}
func (*stubOrders) Categories(context.Context, string) ([]domain.Category, error) {
	return []domain.Category{{ID: "cat_vip", Name: "VIP", Price: 1500000, MaxPerOrder: 3}}, nil
}
func (*stubOrders) Identities(context.Context, string, []string) (domain.Identities, error) {
	return domain.Identities{}, nil
}
func (s *stubOrders) Place(ctx context.Context, _ domain.Placement, hold outbound.HoldFunc) (domain.PlaceResult, error) {
	if _, err := hold(ctx); err != nil {
		return domain.PlaceResult{}, err
	}
	s.placed = true
	return domain.PlaceResult{Outcome: domain.Placed}, nil
}

type stubHolds struct{}

func (stubHolds) Hold(context.Context, string, domain.HoldOrder) (domain.HoldAnswer, error) {
	return domain.HoldAnswer{Code: domain.HoldHeld, Index: -1}, nil
}
func (stubHolds) Release(context.Context, string, domain.HoldOrder) (int, error) { return 0, nil }

type stubNIKs struct{}

func (stubNIKs) Validate(_ context.Context, nik string) (domain.NIKCheck, error) {
	return domain.NIKCheck{NIKHmac: "hmac-" + nik, NIKMasked: "3171**********22", Valid: true, Status: "ACTIVE"}, nil
}

type stubReader struct{}

func (stubReader) Get(_ context.Context, _ grantauth.Grant, id string) (usecase.BookingView, error) {
	return usecase.BookingView{BookingID: id, Status: "PENDING_PAYMENT"}, nil
}

type nilMetrics struct{}

func (nilMetrics) Incr(string)              {}
func (nilMetrics) IncrEvent(string, string) {}

func createHandler(o *stubOrders) *CreateBooking {
	uc := usecase.NewCreateBooking(usecase.CreateBookingDeps{
		Orders: o, Holds: stubHolds{}, Stock: stubStock{}, Queue: nil, NIKs: stubNIKs{}, Reader: stubReader{}, Metrics: nilMetrics{},
		NewID: func() string { return "BI-1" },
	}, 3)
	return NewCreateBooking(uc)
}

func post(t *testing.T, h http.Handler, body string, withGrant bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/bookings", strings.NewReader(body))
	if withGrant {
		req = req.WithContext(grantauth.WithGrant(req.Context(), grantauth.Grant{Token: "gt_1", EventID: "evt_a", BookingID: "RB-1", QueueToken: "qt_1", DeviceHash: "h"}))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

const goodBody = `{"event_id":"evt_a","contact_email":"buyer@example.com","tickets":[{"category_id":"cat_vip","nik":"3171000000000022"}]}`

func TestACreatedBookingAnswers201WithTheBookingInTheEnvelope(t *testing.T) {
	o := &stubOrders{status: domain.BookingStatus{Status: bookingstate.Selecting}}
	rec := post(t, createHandler(o), goodBody, true)
	if rec.Code != http.StatusCreated || !o.placed {
		t.Fatalf("status %d placed=%v: %s", rec.Code, o.placed, rec.Body.String())
	}
	var body struct {
		Meta struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"meta"`
		Data struct {
			BookingID string `json:"booking_id"`
			Status    string `json:"status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Meta.Code != 201 || body.Data.BookingID != "RB-1" || body.Data.Status != "PENDING_PAYMENT" {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

// Contract §4.3: a retry answers 200 with the booking as it is, whatever the body says, even when it is not JSON.
func TestARetryOnAHeldBookingAnswers200EvenWithAGarbageBody(t *testing.T) {
	o := &stubOrders{status: domain.BookingStatus{Status: bookingstate.Held}}
	for _, body := range []string{goodBody, `not json at all`, ``, `{"tickets": "wrong type"}`} {
		rec := post(t, createHandler(o), body, true)
		if rec.Code != http.StatusOK || o.placed {
			t.Fatalf("body %q: status %d placed=%v, want 200 and no second hold: %s", body, rec.Code, o.placed, rec.Body.String())
		}
	}
}

func TestAMalformedBodyOnASelectingBookingIs400(t *testing.T) {
	o := &stubOrders{status: domain.BookingStatus{Status: bookingstate.Selecting}}
	for _, body := range []string{`not json`, ``, `{"tickets": "wrong type"}`, `[]`, goodBody + `garbage`, goodBody + goodBody} {
		rec := post(t, createHandler(o), body, true)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"error_code":"VALIDATION_ERROR"`) {
			t.Fatalf("body %q: status %d %s", body, rec.Code, rec.Body.String())
		}
	}
}

// holder_email and any other unknown field from an old client is ignored, not refused (contract §4.3).
func TestUnknownFieldsFromAnOldClientAreIgnored(t *testing.T) {
	o := &stubOrders{status: domain.BookingStatus{Status: bookingstate.Selecting}}
	body := `{"event_id":"evt_a","contact_email":"buyer@example.com","terms_version":"v1",
	  "tickets":[{"category_id":"cat_vip","nik":"3171000000000022","holder_email":"x@example.com"}]}`
	if rec := post(t, createHandler(o), body, true); rec.Code != http.StatusCreated {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAnOversizedBodyIsRefusedWithoutBeingBuffered(t *testing.T) {
	o := &stubOrders{status: domain.BookingStatus{Status: bookingstate.Selecting}}
	huge := `{"contact_email":"` + strings.Repeat("a", 200<<10) + `"}`
	if rec := post(t, createHandler(o), huge, true); rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400 for a body over the limit", rec.Code)
	}
}

func TestTheCreateHandlerRefusesARequestThatBypassedTheMiddleware(t *testing.T) {
	rec := post(t, createHandler(&stubOrders{}), goodBody, false)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "GRANT_TOKEN_MISSING") {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
}
