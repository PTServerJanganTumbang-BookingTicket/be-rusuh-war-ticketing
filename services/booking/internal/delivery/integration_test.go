package delivery_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/config"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/delivery"
)

// newTestServer stands up the fully-wired service over a real TCP listener
// (real usecase + in-memory repository + HTTP handler/router).
func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(delivery.New(config.Config{}))
	t.Cleanup(srv.Close)
	return srv
}

type envelope struct {
	Meta struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"meta"`
	Data struct {
		BookingID string `json:"booking_id"`
		Status    string `json:"status"`
		Tickets   []struct {
			Category  string `json:"category"`
			Price     int64  `json:"price"`
			NIKMasked string `json:"nik_masked"`
		} `json:"tickets"`
		Summary struct {
			Total int64 `json:"total"`
		} `json:"summary"`
		PaymentTTL struct {
			TTLSeconds int `json:"ttl_seconds"`
		} `json:"payment_ttl"`
	} `json:"data"`
}

func doJSON(t *testing.T, srv *httptest.Server, method, path, body string) (*http.Response, envelope) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = bytes.NewReader([]byte(body))
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	raw, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	var env envelope
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &env)
	}
	return resp, env
}

const validBody = `{
	"event_id": "evt_rusuh_2026",
	"tickets": [{"category_id": "cat_vip", "nik": "3171000000000001", "holder_email": "a@example.com"}],
	"contact_email": "contact@example.com",
	"terms_version": "v1"
}`

func TestIntegration_CreateThenGetBooking(t *testing.T) {
	srv := newTestServer(t)

	createResp, created := doJSON(t, srv, http.MethodPost, "/bookings", validBody)
	assert.Equal(t, http.StatusCreated, createResp.StatusCode)
	assert.Equal(t, http.StatusCreated, created.Meta.Code)
	require.NotEmpty(t, created.Data.BookingID)
	assert.Equal(t, "PENDING_PAYMENT", created.Data.Status)
	require.Len(t, created.Data.Tickets, 1)
	assert.Equal(t, "VIP", created.Data.Tickets[0].Category)
	assert.Equal(t, int64(1500000), created.Data.Summary.Total)
	assert.Equal(t, "3171********xx01", created.Data.Tickets[0].NIKMasked)
	assert.LessOrEqual(t, created.Data.PaymentTTL.TTLSeconds, 600)

	// The booking persisted in the in-memory repo is retrievable by id.
	getResp, got := doJSON(t, srv, http.MethodGet, "/bookings/"+created.Data.BookingID, "")
	assert.Equal(t, http.StatusOK, getResp.StatusCode)
	assert.Equal(t, created.Data.BookingID, got.Data.BookingID)
	assert.Equal(t, "3171********xx01", got.Data.Tickets[0].NIKMasked)
}

func TestIntegration_GetUnknownBookingIsNotFound(t *testing.T) {
	srv := newTestServer(t)

	resp, env := doJSON(t, srv, http.MethodGet, "/bookings/RB-does-not-exist", "")
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	assert.Equal(t, http.StatusNotFound, env.Meta.Code)
}

func TestIntegration_CreateInvalidBodyIsBadRequest(t *testing.T) {
	srv := newTestServer(t)

	resp, env := doJSON(t, srv, http.MethodPost, "/bookings", `{"event_id":""}`)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, env.Meta.Code)
}

func TestIntegration_CreateUnknownCategoryIsBadRequest(t *testing.T) {
	srv := newTestServer(t)

	body := `{
		"event_id": "evt_rusuh_2026",
		"tickets": [{"category_id": "cat_unknown", "nik": "3171000000000001", "holder_email": "a@example.com"}],
		"contact_email": "contact@example.com",
		"terms_version": "v1"
	}`
	resp, env := doJSON(t, srv, http.MethodPost, "/bookings", body)
	assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	assert.Equal(t, http.StatusBadRequest, env.Meta.Code)
}

func TestIntegration_HealthCheck(t *testing.T) {
	srv := newTestServer(t)

	resp, _ := doJSON(t, srv, http.MethodGet, "/sys/ping", "")
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
