package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
)

// grantToken pulls the verified grant off the context.
//
// Absent means the route was mounted without the guard, which is a wiring bug
// rather than a client error — so it answers 500 rather than inventing a 401
// that would send the client off chasing its own headers.
func (c *PaymentController) grantToken(w http.ResponseWriter, r *http.Request) (string, bool) {
	g, ok := GrantFromRequest(r)
	if !ok {
		slog.ErrorContext(r.Context(), "payment route reached without grant guard",
			slog.String("path", r.URL.Path))
		WriteError(w, apperror.ErrInternal("grant guard is not configured"))
		return "", false
	}
	return g.Token, true
}

// GetMethods handles GET /payments/{booking_id}/methods (§5.1).
func (c *PaymentController) GetMethods(w http.ResponseWriter, r *http.Request) {
	token, ok := c.grantToken(w, r)
	if !ok {
		return
	}
	res, err := c.usecase.GetMethods(r.Context(), r.PathValue("booking_id"), token)
	if err != nil {
		WriteError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Success", res)
}

// Initiate handles POST /payments/{booking_id} (§5.2).
func (c *PaymentController) Initiate(w http.ResponseWriter, r *http.Request) {
	token, ok := c.grantToken(w, r)
	if !ok {
		return
	}
	var req dto.InitiateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, apperror.ErrBadRequest("malformed request body").WithCode(apperror.CodeValidationError))
		return
	}
	if err := c.validate.Struct(req); err != nil {
		WriteError(w, apperror.ErrBadRequest("method is required").WithCode(apperror.CodeValidationError))
		return
	}
	res, err := c.usecase.Initiate(r.Context(), r.PathValue("booking_id"), token, req)
	if err != nil {
		WriteError(w, err)
		return
	}
	// 200, not 201: a replayed initiate returns the SAME payment and creating
	// nothing, and the client cannot tell the two apart — so neither should
	// the status code (§5.2).
	apinizer.WriteEnvelope(w, http.StatusOK, "Payment initiated", res)
}

// Retry handles POST /payments/{booking_id}/retry (§5.3).
func (c *PaymentController) Retry(w http.ResponseWriter, r *http.Request) {
	token, ok := c.grantToken(w, r)
	if !ok {
		return
	}
	res, err := c.usecase.Retry(r.Context(), r.PathValue("booking_id"), token)
	if err != nil {
		WriteError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Previous payment cancelled", res)
}

// GetStatus handles GET /payments/{booking_id}/status (§5.4).
func (c *PaymentController) GetStatus(w http.ResponseWriter, r *http.Request) {
	token, ok := c.grantToken(w, r)
	if !ok {
		return
	}
	res, err := c.usecase.GetStatus(r.Context(), r.PathValue("booking_id"), token)
	if err != nil {
		WriteError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, statusMessage(res.Status), res)
}

// statusMessage mirrors the meta.message the contract shows per status.
func statusMessage(status string) string {
	switch status {
	case dto.StatusPaid:
		return "Payment successful"
	case dto.StatusExpired:
		return "Payment expired"
	case dto.StatusFailed:
		return "Payment failed"
	default:
		return "Payment pending"
	}
}
