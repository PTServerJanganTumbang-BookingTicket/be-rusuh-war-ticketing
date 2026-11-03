package http

import (
	"encoding/json"
	"net/http"

	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apinizer"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/pkg/apperror"
	"github.com/PTServerJanganTumbang-BookingTicket/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
)

// GetSavedNIKs handles GET /accounts/me/saved-niks (FR-32a..c).
func (c *UserController) GetSavedNIKs(w http.ResponseWriter, r *http.Request) {
	accountID, ok := c.requireAuth(w, r)
	if !ok {
		return
	}
	resp, err := c.usecase.GetSavedNIKs(r.Context(), accountID)
	if err != nil {
		writeError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusOK, "Success", resp)
}

// AddSavedNIK handles POST /accounts/me/saved-niks (FR-32a..b).
func (c *UserController) AddSavedNIK(w http.ResponseWriter, r *http.Request) {
	accountID, ok := c.requireAuth(w, r)
	if !ok {
		return
	}
	if !c.requireSameOrigin(w, r) {
		return
	}
	var req dto.AddSavedNIKRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, apperror.ErrBadRequest("invalid request body").WithCode(apperror.CodeValidationError))
		return
	}
	if err := c.validate.Struct(req); err != nil {
		writeError(w, apperror.ErrBadRequest("nik and label are required").WithCode(apperror.CodeValidationError))
		return
	}
	item, err := c.usecase.AddSavedNIK(r.Context(), accountID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusCreated, "NIK saved successfully", item)
}

// DeleteSavedNIK handles DELETE /accounts/me/saved-niks/{id} (FR-32d).
func (c *UserController) DeleteSavedNIK(w http.ResponseWriter, r *http.Request) {
	accountID, ok := c.requireAuth(w, r)
	if !ok {
		return
	}
	if !c.requireSameOrigin(w, r) {
		return
	}
	nikID := r.PathValue("id")
	if nikID == "" {
		writeError(w, apperror.ErrBadRequest("NIK id path parameter is required").WithCode(apperror.CodeValidationError))
		return
	}
	if err := c.usecase.DeleteSavedNIK(r.Context(), accountID, nikID); err != nil {
		writeError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
