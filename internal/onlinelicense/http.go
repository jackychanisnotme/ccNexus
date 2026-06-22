package onlinelicense

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
)

type AdminConfig struct {
	Username string
	Password string
}

type HTTPHandler struct {
	service *Service
	admin   AdminConfig
}

func NewHTTPHandler(service *Service, admin AdminConfig) http.Handler {
	return &HTTPHandler{service: service, admin: admin}
}

func AdminMiddleware(admin AdminConfig, next http.Handler) http.Handler {
	handler := &HTTPHandler{admin: admin}
	return handler.withAdmin(next)
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimRight(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}
	switch {
	case path == "/api/license/activate":
		h.handleActivate(w, r)
	case path == "/api/license/refresh":
		h.handleRefresh(w, r)
	case path == "/api/admin/cards":
		h.withAdmin(http.HandlerFunc(h.handleCards)).ServeHTTP(w, r)
	case path == "/api/admin/cards/generate":
		h.withAdmin(http.HandlerFunc(h.handleGenerateCards)).ServeHTTP(w, r)
	case strings.HasPrefix(path, "/api/admin/cards/") && strings.HasSuffix(path, "/disable"):
		h.withAdmin(http.HandlerFunc(h.handleDisableCard)).ServeHTTP(w, r)
	case path == "/api/admin/activations":
		h.withAdmin(http.HandlerFunc(h.handleActivations)).ServeHTTP(w, r)
	case strings.HasPrefix(path, "/api/admin/activations/") && strings.HasSuffix(path, "/disable"):
		h.withAdmin(http.HandlerFunc(h.handleDisableActivation)).ServeHTTP(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (h *HTTPHandler) handleActivate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req ActivationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.IPAddress = clientIP(r)
	result, err := h.service.Activate(req)
	if err != nil {
		writeJSONError(w, httpStatusForError(err), err.Error())
		return
	}
	writeJSONSuccess(w, result)
}

func (h *HTTPHandler) handleRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req RefreshRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.IPAddress = clientIP(r)
	result, err := h.service.Refresh(req)
	if err != nil {
		writeJSONError(w, httpStatusForError(err), err.Error())
		return
	}
	writeJSONSuccess(w, result)
}

func (h *HTTPHandler) handleCards(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	cards, err := h.service.ListCards()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONSuccess(w, cards)
}

func (h *HTTPHandler) handleGenerateCards(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req GenerateCardsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.service.GenerateCards(req)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSONSuccess(w, result)
}

func (h *HTTPHandler) handleDisableCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := pathID(r.URL.Path, "/api/admin/cards/", "/disable")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.service.DisableCard(id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONSuccess(w, map[string]bool{"disabled": true})
}

func (h *HTTPHandler) handleActivations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	activations, err := h.service.ListActivations()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONSuccess(w, activations)
}

func (h *HTTPHandler) handleDisableActivation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := pathID(r.URL.Path, "/api/admin/activations/", "/disable")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.service.DisableActivation(id); err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONSuccess(w, map[string]bool{"disabled": true})
}

func (h *HTTPHandler) withAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || !constantTimeEqual(username, h.admin.Username) || !constantTimeEqual(password, h.admin.Password) {
			w.Header().Set("WWW-Authenticate", `Basic realm="ccNexus License Admin"`)
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func constantTimeEqual(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func writeJSONSuccess(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"data":    data,
	})
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   message,
	})
}

func httpStatusForError(err error) int {
	switch {
	case errors.Is(err, ErrInvalidCard), errors.Is(err, ErrCardDisabled), errors.Is(err, ErrDeviceLimit), errors.Is(err, ErrActivationBlocked), errors.Is(err, ErrInvalidTicket), errors.Is(err, ErrTicketExpired):
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

func pathID(path, prefix, suffix string) (int64, error) {
	idText := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	id, err := strconv.ParseInt(strings.Trim(idText, "/"), 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid id")
	}
	return id, nil
}

func clientIP(r *http.Request) string {
	if forwarded := strings.TrimSpace(r.Header.Get("X-Forwarded-For")); forwarded != "" {
		parts := strings.Split(forwarded, ",")
		return strings.TrimSpace(parts[0])
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
