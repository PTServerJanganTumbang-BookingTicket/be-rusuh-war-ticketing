package http

import "net/http"

// RegisterRoutes mounts all user-domain HTTP routes on the given mux.
// Route patterns use Go 1.22+ method-prefixed syntax.
func RegisterRoutes(mux *http.ServeMux, h User) {
	// Auth
	mux.HandleFunc("POST /auth/magic-link", h.RequestMagicLink)
	mux.HandleFunc("POST /auth/magic-link/resend", h.ResendMagicLink)
	mux.HandleFunc("GET /auth/verify", h.VerifyMagicLink)
	mux.HandleFunc("GET /auth/verify-email", h.VerifyEmail)
	mux.HandleFunc("POST /auth/register", h.Register)
	mux.HandleFunc("POST /auth/refresh", h.RefreshSession)

	// Account / Saved NIKs
	mux.HandleFunc("GET /accounts/me/saved-niks", h.GetSavedNIKs)
	mux.HandleFunc("POST /accounts/me/saved-niks", h.AddSavedNIK)
	mux.HandleFunc("DELETE /accounts/me/saved-niks/{id}", h.DeleteSavedNIK)

	// Tickets
	mux.HandleFunc("GET /users/me/tickets", h.GetMyTickets)
	mux.HandleFunc("GET /tickets/{ticket_id}/eticket", h.GetETicket)
}
