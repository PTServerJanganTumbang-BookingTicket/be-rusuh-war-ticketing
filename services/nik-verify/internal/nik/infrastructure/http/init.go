package http

import (
	"errors"
	"net/http"

	"github.com/go-playground/validator/v10"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	portin "github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/port/inbound"
)

// Nik is the HTTP handler interface for the NIK domain.
type Nik interface {
	Validate(w http.ResponseWriter, r *http.Request)
}

// NikController translates HTTP to use case calls and back.
type NikController struct {
	usecase  portin.INikUsecase
	validate *validator.Validate
}

// NewNik creates a NikController behind the Nik interface.
func NewNik(uc portin.INikUsecase) Nik {
	return &NikController{usecase: uc, validate: validator.New()}
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
