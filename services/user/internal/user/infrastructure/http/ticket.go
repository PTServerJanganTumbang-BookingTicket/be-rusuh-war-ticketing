package http

import (
	"net/http"
	"strconv"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
)

// GetMyTickets handles GET /users/me/tickets (contract §6.1).
//
// Responds through WriteList so meta.pagination travels with the rows: clients
// page on it and have no other source for the total.
func (c *UserController) GetMyTickets(w http.ResponseWriter, r *http.Request) {
	accountID, ok := c.requireAuth(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	req := dto.GetMyTicketsRequest{
		Status: q.Get("status"),
		Limit:  parseQueryInt(q.Get("limit"), 0),
		Offset: parseQueryInt(q.Get("offset"), 0),
	}

	resp, err := c.usecase.GetMyTickets(r.Context(), accountID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	apinizer.WriteList(w, http.StatusOK, "Success", resp.Tickets,
		apinizer.NewPagination(resp.Total, resp.Limit, resp.Offset))
}

// GetETicket handles GET /tickets/{ticket_id}/eticket (FR-27, contract §6.2).
func (c *UserController) GetETicket(w http.ResponseWriter, r *http.Request) {
	accountID, ok := c.requireAuth(w, r)
	if !ok {
		return
	}
	ticketID := r.PathValue("ticket_id")
	if ticketID == "" {
		writeError(w, apperror.ErrBadRequest("ticket_id path parameter is required").WithCode(apperror.CodeValidationError))
		return
	}
	resp, err := c.usecase.GetETicket(r.Context(), accountID, ticketID)
	if err != nil {
		writeError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Success", resp)
}

// parseQueryInt reads a non-negative integer, falling back on anything else.
// A negative or unparseable limit is a client mistake, not a reason to 400 a
// read-only listing.
func parseQueryInt(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	v, err := strconv.Atoi(s)
	if err != nil || v < 0 {
		return fallback
	}
	return v
}
