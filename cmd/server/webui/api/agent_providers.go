package api

import (
	"encoding/json"
	"net/http"

	"github.com/lich0821/ccNexus/internal/service"
)

func (h *Handler) handleAgentProviderStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	WriteSuccess(w, h.agentProvider.Status())
}

func (h *Handler) handleAgentProviderApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req service.AgentProviderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	WriteSuccess(w, h.agentProvider.Apply(req))
}

func (h *Handler) handleAgentProviderRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req service.AgentProviderRestoreRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	WriteSuccess(w, h.agentProvider.Restore(req))
}
