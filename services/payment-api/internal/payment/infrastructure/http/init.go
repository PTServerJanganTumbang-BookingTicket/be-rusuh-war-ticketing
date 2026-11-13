package http

import (
	"errors"
	"net/http"
	"net/netip"

	"github.com/go-playground/validator/v10"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/payment-api/internal/payment/port/inbound"
)

// Payment is the HTTP handler interface for the payment domain.
type Payment interface {
	GetMethods(w http.ResponseWriter, r *http.Request)
	Initiate(w http.ResponseWriter, r *http.Request)
	Retry(w http.ResponseWriter, r *http.Request)
	GetStatus(w http.ResponseWriter, r *http.Request)
	Webhook(w http.ResponseWriter, r *http.Request)
}

// PaymentController translates HTTP to use case calls and back.
type PaymentController struct {
	usecase            portin.IPaymentUsecase
	validate           *validator.Validate
	webhookIPAllowlist []netip.Prefix
}

// NewPayment creates a PaymentController behind the Payment interface.
// webhookIPs is the allowlist of CIDR blocks for POST /payments/webhook;
// an empty slice disables the check (acceptable for local dev only — the
// config validator rejects an empty allowlist outside local).
func NewPayment(uc portin.IPaymentUsecase, webhookIPs []netip.Prefix) Payment {
	return &PaymentController{
		usecase:            uc,
		validate:           validator.New(),
		webhookIPAllowlist: webhookIPs,
	}
}

// webhookIPAllowed reports whether the given IP is within any of the
// configured allowlist CIDR blocks.
func (c *PaymentController) webhookIPAllowed(ip string) bool {
	if len(c.webhookIPAllowlist) == 0 {
		return true // empty allowlist disables the check (local dev)
	}
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false // unparseable IP is never allowed
	}
	// Unmap IPv4-mapped IPv6 addresses (::ffff:192.0.2.1 → 192.0.2.1) so that
	// a v4 allowlist matches regardless of how the gateway reports the IP.
	addr = addr.Unmap()
	for _, prefix := range c.webhookIPAllowlist {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// WriteError maps an error onto the response envelope, carrying the Appendix A
// code so callers switch on a machine value rather than parsing prose.
func WriteError(w http.ResponseWriter, err error) {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		apinizer.WriteErrorEnvelope(w, appErr.HTTPStatus, appErr.ErrorCode, appErr.Message, nil)
		return
	}
	apinizer.WriteErrorEnvelope(w, http.StatusInternalServerError, "", "internal server error", nil)
}
