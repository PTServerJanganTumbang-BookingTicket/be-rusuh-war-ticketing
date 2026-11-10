// Package http holds booking's HTTP handlers.
package http

import (
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/booking/internal/booking/application/usecase"
)

// TicketPage handles GET /events/{event_id}/tickets. It must sit behind pkg/grantauth.Middleware.
type TicketPage struct{ uc *usecase.TicketPage }

// NewTicketPage builds the handler.
func NewTicketPage(uc *usecase.TicketPage) *TicketPage { return &TicketPage{uc: uc} }

// ServeHTTP renders the page. Without a grant in the context (route wired without the middleware) it refuses.
func (h *TicketPage) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g, ok := grantauth.FromContext(r.Context())
	if !ok {
		apinizer.WriteError(w, grantauth.ErrMissing())
		return
	}
	view, err := h.uc.Get(r.Context(), g, r.PathValue("event_id"))
	if err != nil {
		apinizer.WriteError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Success", view)
}
