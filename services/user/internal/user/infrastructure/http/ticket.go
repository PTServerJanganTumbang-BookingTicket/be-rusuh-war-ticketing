package http

import (
	"net/http"
	"strconv"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apinizer"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
)

// GetMyTickets handles GET /users/me/tickets (Section 6.1).
func (c *UserController) GetMyTickets(w http.ResponseWriter, r *http.Request) {
	accountID, ok := c.requireAuth(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	req := dto.GetMyTicketsRequest{
		Status: q.Get("status"),
		Limit:  parseQueryInt(q.Get("limit"), 10),
		Offset: parseQueryInt(q.Get("offset"), 0),
	}
	resp, err := c.usecase.GetMyTickets(r.Context(), accountID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Success", resp.Tickets)
}

// GetETicket handles GET /tickets/{ticket_id}/eticket (FR-27, Section 6.2).
func (c *UserController) GetETicket(w http.ResponseWriter, r *http.Request) {
	accountID, ok := c.requireAuth(w, r)
	if !ok {
		return
	}
	ticketID := r.PathValue("ticket_id")
	if ticketID == "" {
		writeError(w, apperror.ErrBadRequest("ticket_id path parameter is required"))
		return
	}
	resp, err := c.usecase.GetETicket(r.Context(), accountID, ticketID)
	if err != nil {
		writeError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Success", resp)
}

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
