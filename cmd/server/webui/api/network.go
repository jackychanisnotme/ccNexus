package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/storage"
)

func (h *Handler) handleNetwork(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		WriteSuccess(w, h.proxy.GetNetworkStatus())
	case http.MethodPut:
		var req struct {
			ListenMode string `json:"listenMode"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		if !isValidListenMode(req.ListenMode) {
			WriteError(w, http.StatusBadRequest, "Invalid listen mode")
			return
		}

		h.config.UpdateListenMode(req.ListenMode)
		adapter := storage.NewConfigStorageAdapter(h.storage)
		if err := h.config.SaveToStorage(adapter); err != nil {
			logger.Error("Failed to save network config: %v", err)
			WriteError(w, http.StatusInternalServerError, "Failed to save network configuration")
			return
		}

		status := h.proxy.GetNetworkStatus()
		status.RestartRequired = true
		WriteSuccess(w, status)
	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func isValidListenMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case config.ListenModeLocal, config.ListenModeLAN:
		return true
	default:
		return false
	}
}
