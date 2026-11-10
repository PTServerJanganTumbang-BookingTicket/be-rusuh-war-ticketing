package http

import (
	"errors"
	"net/http"

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
	usecase  portin.IPaymentUsecase
	validate *validator.Validate
}

// NewPayment creates a PaymentController behind the Payment interface.
func NewPayment(uc portin.IPaymentUsecase) Payment {
	return &PaymentController{usecase: uc, validate: validator.New()}
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
