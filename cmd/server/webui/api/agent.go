package api

import (
	"encoding/json"
	"net/http"

	"github.com/lich0821/ccNexus/internal/service"
)

func (h *Handler) handleAgentRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req service.AgentRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	WriteSuccess(w, h.agent.Run(req))
}

func (h *Handler) handleAgentCheckConfigs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req service.AgentProviderInspectRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	WriteSuccess(w, h.agent.CheckAgentConfigs(req))
}

func (h *Handler) handleAgentRepairConfigs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req service.AgentConfigRepairRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	WriteSuccess(w, h.agent.RepairAgentConfigs(req))
}
