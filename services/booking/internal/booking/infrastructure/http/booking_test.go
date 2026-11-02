package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	mock_inbound "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/_mock/booking/port/inbound"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/booking/internal/booking/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
)

func validBody() string {
	return `{
		"event_id": "evt_rusuh_2026",
		"tickets": [{"category_id": "cat_vip", "nik": "3171000000000001", "holder_email": "a@example.com"}],
		"contact_email": "contact@example.com",
		"terms_version": "v1"
	}`
}

func TestCreateBooking_success(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIBookingUsecase(ctrl)
	uc.EXPECT().
		CreateBooking(gomock.Any(), gomock.Any()).
		Return(dto.BookingResponse{BookingID: "RB-001", Status: "PENDING_PAYMENT"}, nil)

	c := NewBooking(uc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bookings", strings.NewReader(validBody()))

	c.CreateBooking(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "RB-001") {
		t.Errorf("body missing booking id: %s", rec.Body.String())
	}
}

func TestCreateBooking_invalidJSON(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIBookingUsecase(ctrl)
	// usecase must NOT be called on a decode failure

	c := NewBooking(uc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bookings", strings.NewReader("{not json"))

	c.CreateBooking(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCreateBooking_validationError(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIBookingUsecase(ctrl)
	// missing required fields -> validator fails -> usecase not called

	c := NewBooking(uc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bookings", strings.NewReader(`{"event_id":"e1"}`))

	c.CreateBooking(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestCreateBooking_usecaseError(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIBookingUsecase(ctrl)
	uc.EXPECT().
		CreateBooking(gomock.Any(), gomock.Any()).
		Return(dto.BookingResponse{}, apperror.ErrConflict("already booked"))

	c := NewBooking(uc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/bookings", strings.NewReader(validBody()))

	c.CreateBooking(rec, req)

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409", rec.Code)
	}
}

func TestGetBooking_success(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIBookingUsecase(ctrl)
	uc.EXPECT().
		GetBooking(gomock.Any(), "RB-001").
		Return(dto.BookingResponse{BookingID: "RB-001"}, nil)

	c := NewBooking(uc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bookings/RB-001", nil)
	req.SetPathValue("booking_id", "RB-001")

	c.GetBooking(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

func TestGetBooking_notFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIBookingUsecase(ctrl)
	uc.EXPECT().
		GetBooking(gomock.Any(), "RB-missing").
		Return(dto.BookingResponse{}, apperror.ErrNotFound("booking not found"))

	c := NewBooking(uc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bookings/RB-missing", nil)
	req.SetPathValue("booking_id", "RB-missing")

	c.GetBooking(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestGetBooking_internalError(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIBookingUsecase(ctrl)
	// a non-AppError must fall back to 500
	uc.EXPECT().
		GetBooking(gomock.Any(), "RB-boom").
		Return(dto.BookingResponse{}, errors.New("db down"))

	c := NewBooking(uc)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/bookings/RB-boom", nil)
	req.SetPathValue("booking_id", "RB-boom")

	c.GetBooking(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
