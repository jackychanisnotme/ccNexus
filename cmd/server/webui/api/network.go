package api

import (
	"encoding/json"
	"fmt"
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
			Port       *int   `json:"port,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		listenMode := strings.ToLower(strings.TrimSpace(req.ListenMode))
		if listenMode == "" {
			listenMode = h.config.GetListenMode()
		}
		if !isValidListenMode(listenMode) {
			WriteError(w, http.StatusBadRequest, "Invalid listen mode")
			return
		}
		port := h.config.GetPort()
		if req.Port != nil {
			if *req.Port <= 0 || *req.Port > 65535 {
				WriteError(w, http.StatusBadRequest, "Invalid port number")
				return
			}
			if h.config.IsPortLocked() && *req.Port != port {
				WriteError(w, http.StatusForbidden, "Port is locked by CLI flag and cannot be changed")
				return
			}
			port = *req.Port
		}

		if err := h.applyNetworkAccessConfig(listenMode, port); err != nil {
			logger.Error("Failed to save network config: %v", err)
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}

		status := h.proxy.GetNetworkStatus()
		status.RestartRequired = false
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

func (h *Handler) applyNetworkAccessConfig(listenMode string, port int) error {
	oldMode := h.config.GetListenMode()
	oldPort := h.config.GetPort()

	h.config.UpdateListenMode(listenMode)
	h.config.UpdatePort(port)

	if h.proxy != nil {
		if err := h.proxy.RebindListener(); err != nil {
			h.restoreNetworkAccessConfig(oldMode, oldPort)
			_ = h.proxy.RebindListener()
			return fmt.Errorf("failed to apply listener change: %w", err)
		}
	}

	adapter := storage.NewConfigStorageAdapter(h.storage)
	if err := h.config.SaveToStorage(adapter); err != nil {
		h.restoreNetworkAccessConfig(oldMode, oldPort)
		if h.proxy != nil {
			_ = h.proxy.RebindListener()
		}
		return fmt.Errorf("failed to save network configuration: %w", err)
	}

	return nil
}

func (h *Handler) restoreNetworkAccessConfig(listenMode string, port int) {
	h.config.UpdateListenMode(listenMode)
	h.config.UpdatePort(port)
}
