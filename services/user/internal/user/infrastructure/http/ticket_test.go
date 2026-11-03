package http

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
)

func TestGetMyTickets_EmitsPagination(t *testing.T) {
	// The gap this closes: the total was computed and then dropped, so clients
	// had no row count to page on (contract §6.1).
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().GetMyTickets(gomock.Any(), "USR-001", gomock.Any()).Return(dto.MyTicketsResponse{
		Total: 23, Limit: 10, Offset: 0,
		Tickets: []dto.MyTicketItem{{TicketID: "TKT-001", BookingID: "RB-00912"}},
	}, nil)

	rec := authed(t, mux, http.MethodGet, "/users/me/tickets", "")
	assert.Equal(t, http.StatusOK, rec.Code)

	meta := metaOf(t, rec)
	page, ok := meta["pagination"].(map[string]any)
	require.True(t, ok, "meta.pagination must be present on a list endpoint")
	assert.Equal(t, float64(23), page["totalData"])
	assert.Equal(t, float64(3), page["totalPage"])
	assert.Equal(t, float64(10), page["limit"])
	assert.Equal(t, float64(0), page["offset"])
	assert.Len(t, decode(t, rec)["data"], 1)
}

func TestGetMyTickets_PassesQueryParams(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().GetMyTickets(gomock.Any(), "USR-001",
		dto.GetMyTicketsRequest{Status: "upcoming", Limit: 25, Offset: 50}).
		Return(dto.MyTicketsResponse{Limit: 25, Offset: 50, Tickets: []dto.MyTicketItem{}}, nil)

	rec := authed(t, mux, http.MethodGet, "/users/me/tickets?status=upcoming&limit=25&offset=50", "")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetMyTickets_IgnoresUnparseableParams(t *testing.T) {
	// A bad limit is a client mistake, not a reason to 400 a read-only listing.
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().GetMyTickets(gomock.Any(), gomock.Any(), dto.GetMyTicketsRequest{Limit: 0, Offset: 0}).
		Return(dto.MyTicketsResponse{Limit: 10, Tickets: []dto.MyTicketItem{}}, nil)

	rec := authed(t, mux, http.MethodGet, "/users/me/tickets?limit=abc&offset=-5", "")
	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetMyTickets_Unauthenticated(t *testing.T) {
	mux, _ := newTestServer(t)
	req := httptestNewGet("/users/me/tickets")
	rec := serve(mux, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGetETicket_Success(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().GetETicket(gomock.Any(), "USR-001", "TKT-001").Return(dto.ETicketResponse{
		TicketID: "TKT-001",
		Holder:   dto.ETicketHolder{NIKMasked: "3171**********22"},
		QRValue:  "TKT-001.nonce-abc",
		QRCode:   "TKT-001.nonce-abc",
	}, nil)

	rec := authed(t, mux, http.MethodGet, "/tickets/TKT-001/eticket", "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NotContains(t, rec.Body.String(), "3171000000000022")

	data := decode(t, rec)["data"].(map[string]any)
	assert.Equal(t, "TKT-001.nonce-abc", data["qr_value"])
}

func TestGetETicket_NotFound(t *testing.T) {
	mux, uc := newTestServer(t)
	expectAuth(uc, "USR-001")
	uc.EXPECT().GetETicket(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(dto.ETicketResponse{}, apperror.ErrNotFound("ticket not found"))

	rec := authed(t, mux, http.MethodGet, "/tickets/TKT-other/eticket", "")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestParseQueryInt(t *testing.T) {
	assert.Equal(t, 7, parseQueryInt("", 7))
	assert.Equal(t, 7, parseQueryInt("abc", 7))
	assert.Equal(t, 7, parseQueryInt("-1", 7))
	assert.Equal(t, 42, parseQueryInt("42", 7))
	assert.Equal(t, 0, parseQueryInt("0", 7))
}
