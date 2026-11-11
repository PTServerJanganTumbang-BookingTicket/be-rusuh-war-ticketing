package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/domain/dto"
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/inbound"
)

// maxExchangeBody bounds the exchange body; the real one is about 100 bytes.
const maxExchangeBody = 4 << 10

// Handoff is the HTTP side of the WebView payment handoff (ADR-007, §5.6, §5.7).
//
// Errors go through apinizer.WriteError, not this package's WriteError: the latter drops data, and these answers
// carry data.status, data.payment_expires_at and data.retry_after.
type Handoff struct{ uc portin.IHandoffUsecase }

// NewHandoff returns the handoff handlers.
func NewHandoff(uc portin.IHandoffUsecase) *Handoff { return &Handoff{uc: uc} }

// RegisterHandoffRoutes mounts §5.6 behind guard, which must be grantauth.Middleware (it refuses X-Payment-Session:
// only the app may mint a code), and §5.7 with no guard: the code in the body is the credential.
func RegisterHandoffRoutes(mux *http.ServeMux, h *Handoff, guard func(http.Handler) http.Handler) {
	mux.Handle("POST /payments/{booking_id}/handoff", guard(http.HandlerFunc(h.Create)))
	mux.HandleFunc("POST /payments/handoff/exchange", h.Exchange)
}

// Create handles POST /payments/{booking_id}/handoff (§5.6).
func (h *Handoff) Create(w http.ResponseWriter, r *http.Request) {
	g, ok := GrantFromRequest(r)
	if !ok {
		slog.ErrorContext(r.Context(), "handoff route reached without grant guard", slog.String("path", r.URL.Path))
		apinizer.WriteError(w, apperror.ErrInternal("grant guard is not configured"))
		return
	}
	res, err := h.uc.CreateHandoff(r.Context(), r.PathValue("booking_id"), g.Token)
	if err != nil {
		apinizer.WriteError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusCreated, "Handoff code created", res)
}

// Exchange handles POST /payments/handoff/exchange (§5.7).
func (h *Handoff) Exchange(w http.ResponseWriter, r *http.Request) {
	var req dto.ExchangeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxExchangeBody)).Decode(&req); err != nil {
		apinizer.WriteError(w, apperror.ErrBadRequest("booking_id dan handoff_code wajib").WithCode(apperror.CodeValidationError))
		return
	}
	res, err := h.uc.ExchangeHandoff(r.Context(), req)
	if err != nil {
		apinizer.WriteError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Payment session created", res)
}
