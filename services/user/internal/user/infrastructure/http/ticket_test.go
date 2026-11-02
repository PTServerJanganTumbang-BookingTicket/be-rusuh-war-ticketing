package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	mock_inbound "gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/_mock/user/port/inbound"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
)

// ---- GetMyTickets ----

func TestGetMyTickets_success(t *testing.T) {
	c, uc := newTestController(t)
	expectAuth(uc)
	uc.EXPECT().GetMyTickets(gomock.Any(), testAccID, gomock.Any()).Return(dto.MyTicketsResponse{
		Pagination: dto.PaginationMeta{TotalData: 1, TotalPage: 1, Limit: 10, Offset: 0},
		Tickets:    []dto.MyTicketItem{{TicketID: "TKT-001", Category: "VIP"}},
	}, nil)

	rec := httptest.NewRecorder()
	req := authHeader(httptest.NewRequest(http.MethodGet, "/users/me/tickets", nil))
	c.GetMyTickets(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "TKT-001") {
		t.Errorf("body missing ticket id: %s", rec.Body.String())
	}
}

func TestGetMyTickets_unauthorized(t *testing.T) {
	c, _ := newTestController(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/users/me/tickets", nil)
	c.GetMyTickets(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rec.Code)
	}
}

func TestGetMyTickets_usecaseError(t *testing.T) {
	ctrl := gomock.NewController(t)
	uc := mock_inbound.NewMockIUserUsecase(ctrl)
	c := NewUser(uc).(*UserController)

	uc.EXPECT().ValidateAccessToken(gomock.Any(), validToken).Return(testAccID, nil)
	uc.EXPECT().GetMyTickets(gomock.Any(), testAccID, gomock.Any()).Return(dto.MyTicketsResponse{}, apperror.ErrInternal("db error"))

	rec := httptest.NewRecorder()
	req := authHeader(httptest.NewRequest(http.MethodGet, "/users/me/tickets", nil))
	c.GetMyTickets(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}

// ---- GetETicket ----

func TestGetETicket_success(t *testing.T) {
	c, uc := newTestController(t)
	expectAuth(uc)
	uc.EXPECT().GetETicket(gomock.Any(), testAccID, "TKT-001").Return(dto.ETicketResponse{
		TicketID: "TKT-001",
		Category: "VIP",
		Instructions: "Tunjukkan QR code dan identitas pemegang saat masuk venue.",
	}, nil)

	rec := httptest.NewRecorder()
	req := authHeader(httptest.NewRequest(http.MethodGet, "/tickets/TKT-001/eticket", nil))
	req.SetPathValue("ticket_id", "TKT-001")
	c.GetETicket(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}

func TestGetETicket_notFound(t *testing.T) {
	c, uc := newTestController(t)
	expectAuth(uc)
	uc.EXPECT().GetETicket(gomock.Any(), testAccID, "TKT-missing").Return(dto.ETicketResponse{}, apperror.ErrNotFound("not found"))

	rec := httptest.NewRecorder()
	req := authHeader(httptest.NewRequest(http.MethodGet, "/tickets/TKT-missing/eticket", nil))
	req.SetPathValue("ticket_id", "TKT-missing")
	c.GetETicket(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}

func TestGetETicket_missingPathParam(t *testing.T) {
	c, uc := newTestController(t)
	expectAuth(uc)

	rec := httptest.NewRecorder()
	// ticket_id path value not set
	req := authHeader(httptest.NewRequest(http.MethodGet, "/tickets//eticket", nil))
	c.GetETicket(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// ---- parseQueryInt ----

func TestParseQueryInt_validPositive(t *testing.T) {
	assert := func(got, want int) {
		t.Helper()
		if got != want {
			t.Errorf("got %d, want %d", got, want)
		}
	}
	assert(parseQueryInt("10", 5), 10)
	assert(parseQueryInt("", 5), 5)        // empty → fallback
	assert(parseQueryInt("-1", 5), 5)      // negative → fallback
	assert(parseQueryInt("abc", 5), 5)     // non-numeric → fallback
}
