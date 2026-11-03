package http

import (
	"encoding/json"
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/nik-verify/internal/nik/domain/dto"
)

// Validate handles POST /nik/validate (FR-15a).
//
// Always 200 for both a valid and an invalid NIK — the call succeeded either
// way. Non-2xx means no answer was obtainable, which is the only case callers
// are required to fail closed on.
func (c *NikController) Validate(w http.ResponseWriter, r *http.Request) {
	var req dto.ValidateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, apperror.ErrBadRequest("invalid request body").WithCode(apperror.CodeValidationError))
		return
	}
	if err := c.validate.Struct(req); err != nil {
		WriteError(w, apperror.ErrBadRequest("nik is required").WithCode(apperror.CodeValidationError))
		return
	}

	resp, err := c.usecase.Validate(r.Context(), req)
	if err != nil {
		WriteError(w, err)
		return
	}

	message := "NIK is valid"
	if !resp.Valid {
		message = "NIK is not valid"
	}
	apinizer.WriteEnvelope(w, http.StatusOK, message, resp)
}
