package http

import (
	"encoding/json"
	"net/http"

	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apinizer"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/pkg/apperror"
	"gitlab-cicd-ee.telkomsel.co.id/my-telkomsel/be-rusuh-war-ticketing/services/user/internal/user/domain/dto"
)

// GetSavedNIKs handles GET /accounts/me/saved-niks (FR-15a).
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

// AddSavedNIK handles POST /accounts/me/saved-niks (FR-15a).
func (c *UserController) AddSavedNIK(w http.ResponseWriter, r *http.Request) {
	accountID, ok := c.requireAuth(w, r)
	if !ok {
		return
	}
	var req dto.AddSavedNIKRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, apperror.ErrBadRequest("invalid request body"))
		return
	}
	if err := c.validate.Struct(req); err != nil {
		writeError(w, apperror.ErrBadRequest(err.Error()))
		return
	}
	item, err := c.usecase.AddSavedNIK(r.Context(), accountID, req)
	if err != nil {
		writeError(w, err)
		return
	}
	apinizer.WriteEnvelope(w, http.StatusCreated, "NIK saved successfully", item)
}

// DeleteSavedNIK handles DELETE /accounts/me/saved-niks/{id}.
func (c *UserController) DeleteSavedNIK(w http.ResponseWriter, r *http.Request) {
	accountID, ok := c.requireAuth(w, r)
	if !ok {
		return
	}
	nikID := r.PathValue("id")
	if nikID == "" {
		writeError(w, apperror.ErrBadRequest("NIK id path parameter is required"))
		return
	}
	if err := c.usecase.DeleteSavedNIK(r.Context(), accountID, nikID); err != nil {
		writeError(w, err)
		return
	}
	// 204 No Content — no body.
	w.WriteHeader(http.StatusNoContent)
}
