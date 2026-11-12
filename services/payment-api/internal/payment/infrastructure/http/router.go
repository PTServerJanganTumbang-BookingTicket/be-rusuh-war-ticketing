package http

import (
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/grantauth"
)

// RegisterRoutes mounts the payment routes.
//
// The four customer-facing routes are wrapped in guard, which in production is
// grantauth.MiddlewareWithSession: the WebView payment page reaches them with
// X-Payment-Session instead of the grant headers (ADR-007). The webhook
// is NOT. That asymmetry is the design: the webhook is called by Midtrans,
// which holds no grant, and is protected instead by an API Gateway resource
// policy (IP allowlist) plus the signature_key inside the body
// (api-contracts §5.5).
func RegisterRoutes(mux *http.ServeMux, h Payment, guard func(http.Handler) http.Handler) {
	customer := func(pattern string, fn http.HandlerFunc) {
		mux.Handle(pattern, guard(fn))
	}
	customer("GET /payments/{booking_id}/methods", h.GetMethods)
	customer("POST /payments/{booking_id}", h.Initiate)
	customer("POST /payments/{booking_id}/retry", h.Retry)
	customer("GET /payments/{booking_id}/status", h.GetStatus)

	// No guard, no rate limit, no CAPTCHA.
	mux.HandleFunc("POST /payments/webhook", h.Webhook)
}

// GrantFromRequest is a thin re-export so handlers do not import pkg/grantauth
// directly for the single value they need.
func GrantFromRequest(r *http.Request) (grantauth.Grant, bool) {
	return grantauth.FromContext(r.Context())
}
