package api

import (
	"encoding/json"
	"net/http"
	"time"
)

func (h *Handler) handleLicenseStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if h.license == nil {
		WriteError(w, http.StatusServiceUnavailable, "License service unavailable")
		return
	}
	status, err := h.license.Status(time.Now().UTC())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteSuccess(w, status)
}

func (h *Handler) handleLicenseActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if h.license == nil {
		WriteError(w, http.StatusServiceUnavailable, "License service unavailable")
		return
	}
	var req struct {
		CardKey string `json:"cardKey"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	result, err := h.license.Activate(req.CardKey, time.Now().UTC())
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}
	WriteSuccess(w, result)
}
