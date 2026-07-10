package onlinelicense

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	adminSessionCookieName  = "ccnexus_admin_session"
	defaultAdminSessionTTL  = 12 * time.Hour
	defaultRequestBodyLimit = 128 << 10
	remoteRequestBodyLimit  = 2 << 20
)

type AdminConfig struct {
	Username   string
	Password   string
	SessionTTL time.Duration
}

type HTTPHandler struct {
	service  *Service
	admin    AdminConfig
	sessions *adminSessionStore
	limiter  *rateLimiter
}

type adminSession struct {
	Username  string
	AccountID int64
	ExpiresAt time.Time
}

type adminContextKey struct{}

type adminSessionStore struct {
	mu       sync.Mutex
	sessions map[string]adminSession
}

type rateLimiter struct {
	mu          sync.Mutex
	buckets     map[string]rateBucket
	lastCleanup time.Time
}

type rateBucket struct {
	Count   int
	ResetAt time.Time
}

func NewHTTPHandler(service *Service, admin AdminConfig) *HTTPHandler {
	if admin.SessionTTL <= 0 {
		admin.SessionTTL = defaultAdminSessionTTL
	}
	if service != nil && strings.TrimSpace(admin.Username) != "" && strings.TrimSpace(admin.Password) != "" {
		_, _ = service.EnsureBootstrapAdmin(admin.Username, admin.Password)
	}
	return &HTTPHandler{
		service: service,
		admin:   admin,
		sessions: &adminSessionStore{
			sessions: map[string]adminSession{},
		},
		limiter: &rateLimiter{
			buckets: map[string]rateBucket{},
		},
	}
}

func AdminMiddleware(admin AdminConfig, next http.Handler) http.Handler {
	handler := NewHTTPHandler(nil, admin)
	return handler.AdminPageMiddleware(next)
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimRight(r.URL.Path, "/")
	if path == "" {
		path = "/"
	}
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, requestBodyLimit(path))
	}
	switch {
	case path == "/api/license/activate":
		if !h.allowRate(w, r, "license", 120, time.Minute) {
			return
		}
		h.handleActivate(w, r)
	case path == "/api/license/refresh":
		if !h.allowRate(w, r, "license", 120, time.Minute) {
			return
		}
		h.handleRefresh(w, r)
	case path == "/api/license/remote/poll":
		if !h.allowRate(w, r, "license_remote", 2400, time.Minute) {
			return
		}
		h.handleRemotePoll(w, r)
	case path == "/api/license/remote/result":
		if !h.allowRate(w, r, "license_remote", 2400, time.Minute) {
			return
		}
		h.handleRemoteResult(w, r)
	case path == "/api/license/telemetry/endpoint-errors":
		if !h.allowRate(w, r, "license_remote", 2400, time.Minute) {
			return
		}
		h.handleEndpointErrorTelemetrySubmit(w, r)
	case path == "/api/admin/login":
		h.handleLogin(w, r)
	case path == "/api/admin/logout":
		h.serveAdminMutation(w, r, h.handleLogout)
	case path == "/api/admin/me":
		h.serveAdmin(w, r, h.handleMe)
	case path == "/api/admin/accounts":
		h.serveAdminMutation(w, r, h.handleAccounts)
	case strings.HasPrefix(path, "/api/admin/accounts/"):
		h.serveAdminMutation(w, r, h.handleAccount)
	case path == "/api/admin/cards":
		h.serveAdmin(w, r, h.handleCards)
	case path == "/api/admin/cards/generate":
		h.serveAdminMutation(w, r, h.handleGenerateCards)
	case strings.HasPrefix(path, "/api/admin/cards/") && strings.HasSuffix(path, "/disable"):
		h.serveAdminMutation(w, r, h.handleDisableCard)
	case strings.HasPrefix(path, "/api/admin/cards/"):
		h.serveAdminMutation(w, r, h.handleDeleteCard)
	case path == "/api/admin/activations":
		h.serveAdmin(w, r, h.handleActivations)
	case strings.HasPrefix(path, "/api/admin/activations/") && strings.HasSuffix(path, "/disable"):
		h.serveAdminMutation(w, r, h.handleDisableActivation)
	case path == "/api/admin/telemetry/endpoint-errors":
		h.serveAdmin(w, r, h.handleEndpointErrorTelemetry)
	case path == "/api/admin/telemetry/endpoint-errors/summary":
		h.serveAdmin(w, r, h.handleEndpointErrorTelemetry)
	case strings.HasPrefix(path, "/api/admin/devices/") && strings.Contains(path, "/remote/commands/"):
		h.serveAdmin(w, r, h.handleRemoteCommandStatus)
	case strings.HasPrefix(path, "/api/admin/devices/") && strings.HasSuffix(path, "/remote/commands"):
		h.serveAdminMutation(w, r, h.handleQueueRemoteCommand)
	case strings.HasPrefix(path, "/api/admin/devices/") && strings.HasSuffix(path, "/remote/secrets/reveal"):
		h.serveAdminMutation(w, r, h.handleQueueRemoteSecretReveal)
	case strings.HasPrefix(path, "/api/admin/devices/") && strings.HasSuffix(path, "/remote"):
		h.serveAdmin(w, r, h.handleRemoteDevice)
	case path == "/api/admin/devices/expiry":
		h.serveAdminMutation(w, r, h.handleSetDeviceExpiry)
	case path == "/api/admin/devices/remark":
		h.serveAdminMutation(w, r, h.handleSetDeviceRemark)
	case path == "/api/admin/devices":
		h.serveAdmin(w, r, h.handleDevices)
	case path == "/api/admin/history":
		h.serveAdmin(w, r, h.handleHistory)
	default:
		http.NotFound(w, r)
	}
}

func (h *HTTPHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !h.allowRate(w, r, "admin_login", 5, time.Minute) {
		return
	}
	if !validAdminRequestOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "cross-site request rejected")
		return
	}
	var req AdminLoginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	username := strings.TrimSpace(req.Username)
	if !constantTimeEqual(username, h.admin.Username) || !constantTimeEqual(req.Password, h.admin.Password) {
		if h.service == nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
	}
	var account *AdminAccount
	var err error
	if h.service != nil {
		account, err = h.service.AuthenticateAdmin(username, req.Password)
		if err != nil {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
	} else {
		account = &AdminAccount{Username: username, Status: AdminAccountStatusActive}
	}
	now := h.currentTime()
	token, expiresAt, err := h.sessions.create(account, now, h.admin.SessionTTL)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "failed to create session")
		return
	}
	http.SetCookie(w, h.sessionCookie(r, token, expiresAt))
	h.recordAudit("admin_login", "admin", 0, clientIP(r))
	writeJSONSuccess(w, AdminSessionInfo{
		Username:    account.Username,
		Account:     publicAdminAccountFor(account, *account),
		Permissions: account.Permissions,
		ExpiresAt:   expiresAt,
	})
}

func (h *HTTPHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if cookie, err := r.Cookie(adminSessionCookieName); err == nil {
		h.sessions.delete(cookie.Value)
	}
	http.SetCookie(w, h.expiredSessionCookie(r))
	h.recordAudit("admin_logout", "admin", 0, clientIP(r))
	writeJSONSuccess(w, map[string]bool{"loggedOut": true})
}

func (h *HTTPHandler) handleMe(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	account := adminFromContext(r)
	writeJSONSuccess(w, map[string]interface{}{
		"account":     publicAdminAccountFor(account, *account),
		"permissions": account.Permissions,
	})
}

func (h *HTTPHandler) handleAccounts(w http.ResponseWriter, r *http.Request) {
	account := adminFromContext(r)
	switch r.Method {
	case http.MethodGet:
		accounts, err := h.service.ListAdminAccountsFor(account)
		if err != nil {
			h.writeServiceError(w, err)
			return
		}
		writeJSONSuccess(w, publicAdminAccountsFor(account, accounts))
	case http.MethodPost:
		var req CreateAdminAccountRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		created, err := h.service.CreateAdminAccount(account, req)
		if err != nil {
			h.writeServiceError(w, err)
			return
		}
		writeJSONSuccess(w, publicAdminAccountFor(account, *created))
	default:
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (h *HTTPHandler) handleAccount(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := pathID(r.URL.Path, "/api/admin/accounts/", "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	req, err := decodeUpdateAdminAccountRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	updated, err := h.service.UpdateAdminAccount(adminFromContext(r), id, req)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, publicAdminAccountFor(adminFromContext(r), *updated))
}

func publicAdminAccountsFor(actor *AdminAccount, accounts []AdminAccount) []AdminAccount {
	result := make([]AdminAccount, 0, len(accounts))
	for _, account := range accounts {
		result = append(result, publicAdminAccountFor(actor, account))
	}
	return result
}

func publicAdminAccountFor(actor *AdminAccount, account AdminAccount) AdminAccount {
	if actor == nil || actor.Level == AdminLevelRoot {
		return account
	}
	if account.ID == actor.ID {
		account.Relationship = AdminRelationshipSelf
	} else {
		account.Relationship = AdminRelationshipDownline
	}
	account.Level = 0
	account.ParentID = 0
	return account
}

func decodeUpdateAdminAccountRequest(r *http.Request) (UpdateAdminAccountRequest, error) {
	var req UpdateAdminAccountRequest
	var raw map[string]json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return req, err
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return req, err
	}
	if err := json.Unmarshal(encoded, &req); err != nil {
		return req, err
	}
	_, req.hasLevel = raw["level"]
	_, req.hasParentID = raw["parentId"]
	_, req.hasStatus = raw["status"]
	_, req.hasPermissions = raw["permissions"]
	return req, nil
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

func (h *HTTPHandler) handleRemotePoll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req RemotePollRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.service.PollRemoteCommands(req)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, result)
}

func (h *HTTPHandler) handleRemoteResult(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req RemoteResultRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := h.service.SubmitRemoteResult(req); err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, map[string]bool{"updated": true})
}

func (h *HTTPHandler) handleEndpointErrorTelemetrySubmit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req EndpointErrorTelemetryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	result, err := h.service.SubmitEndpointErrorTelemetry(req)
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
	cards, err := h.service.ListCardsFor(adminFromContext(r))
	if err != nil {
		h.writeServiceError(w, err)
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
	result, err := h.service.GenerateCardsFor(adminFromContext(r), req)
	if err != nil {
		h.writeServiceError(w, err)
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
	if err := h.service.DisableCardFor(adminFromContext(r), id); err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, map[string]bool{"disabled": true})
}

func (h *HTTPHandler) handleDeleteCard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id, err := pathID(r.URL.Path, "/api/admin/cards/", "")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := h.service.DeleteCardFor(adminFromContext(r), id); err != nil {
		if errors.Is(err, ErrInvalidCard) {
			writeJSONError(w, http.StatusNotFound, err.Error())
			return
		}
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, map[string]bool{"deleted": true})
}

func (h *HTTPHandler) handleActivations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	activations, err := h.service.ListActivationsFor(adminFromContext(r))
	if err != nil {
		h.writeServiceError(w, err)
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
	if err := h.service.DisableActivationFor(adminFromContext(r), id); err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, map[string]bool{"disabled": true})
}

func (h *HTTPHandler) handleDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	devices, err := h.service.ListDevicesFor(adminFromContext(r))
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, devices)
}

func (h *HTTPHandler) handleSetDeviceExpiry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req SetDeviceExpiryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.DeviceID) == "" || req.ExpiresAt.IsZero() {
		writeJSONError(w, http.StatusBadRequest, "device id and expiry are required")
		return
	}
	if err := h.service.SetDeviceExpiryFor(adminFromContext(r), req.DeviceID, req.ExpiresAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "device not found")
			return
		}
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, map[string]bool{"updated": true})
}

func (h *HTTPHandler) handleSetDeviceRemark(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req SetDeviceRemarkRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if strings.TrimSpace(req.DeviceID) == "" {
		writeJSONError(w, http.StatusBadRequest, "device id is required")
		return
	}
	if err := h.service.SetDeviceRemarkFor(adminFromContext(r), req.DeviceID, req.Remark); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeJSONError(w, http.StatusNotFound, "device not found")
			return
		}
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, map[string]bool{"updated": true})
}

func (h *HTTPHandler) handleRemoteDevice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	deviceID, err := remoteDeviceIDFromPath(r.URL.Path, "/api/admin/devices/", "/remote")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	detail, err := h.service.RemoteDeviceDetailFor(adminFromContext(r), deviceID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, detail)
}

func (h *HTTPHandler) handleQueueRemoteCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	deviceID, err := remoteDeviceIDFromPath(r.URL.Path, "/api/admin/devices/", "/remote/commands")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req RemoteCommandRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	command, err := h.service.QueueRemoteCommandFor(adminFromContext(r), deviceID, req)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, command)
}

func (h *HTTPHandler) handleQueueRemoteSecretReveal(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	deviceID, err := remoteDeviceIDFromPath(r.URL.Path, "/api/admin/devices/", "/remote/secrets/reveal")
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	var req RemoteSecretRevealRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	command, err := h.service.QueueRemoteSecretRevealFor(adminFromContext(r), deviceID, req)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, command)
}

func (h *HTTPHandler) handleRemoteCommandStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	deviceID, commandID, err := remoteCommandPath(r.URL.Path)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	command, err := h.service.RemoteCommandFor(adminFromContext(r), deviceID, commandID)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	writeJSONSuccess(w, command)
}

func (h *HTTPHandler) handleEndpointErrorTelemetry(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	query, err := endpointErrorTelemetryQueryFromRequest(r)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	result, err := h.service.EndpointErrorTelemetryFor(adminFromContext(r), query)
	if err != nil {
		h.writeServiceError(w, err)
		return
	}
	if strings.HasSuffix(strings.TrimRight(r.URL.Path, "/"), "/summary") {
		writeJSONSuccess(w, map[string]interface{}{
			"deviceId": query.DeviceID,
			"from":     query.From,
			"to":       query.To,
			"summary":  result.Summary,
		})
		return
	}
	writeJSONSuccess(w, result)
}

func (h *HTTPHandler) handleHistory(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !hasPermission(adminFromContext(r), PermissionHistoryView) {
		writeJSONError(w, http.StatusForbidden, "forbidden")
		return
	}
	history, err := h.service.ListAudit()
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONSuccess(w, history)
}

func remoteDeviceIDFromPath(path, prefix, suffix string) (string, error) {
	value := strings.TrimSuffix(strings.TrimPrefix(path, prefix), suffix)
	value = strings.Trim(value, "/")
	if value == "" || value == path {
		return "", fmt.Errorf("device id is required")
	}
	return value, nil
}

func remoteCommandPath(path string) (string, int64, error) {
	const marker = "/remote/commands/"
	if !strings.HasPrefix(path, "/api/admin/devices/") || !strings.Contains(path, marker) {
		return "", 0, fmt.Errorf("invalid remote command path")
	}
	parts := strings.SplitN(strings.TrimPrefix(path, "/api/admin/devices/"), marker, 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" {
		return "", 0, fmt.Errorf("device id is required")
	}
	id, err := strconv.ParseInt(strings.Trim(parts[1], "/"), 10, 64)
	if err != nil || id <= 0 {
		return "", 0, fmt.Errorf("invalid command id")
	}
	return strings.Trim(parts[0], "/"), id, nil
}

func endpointErrorTelemetryQueryFromRequest(r *http.Request) (EndpointErrorTelemetryQuery, error) {
	values := r.URL.Query()
	query := EndpointErrorTelemetryQuery{
		DeviceID:     strings.TrimSpace(values.Get("deviceId")),
		EndpointName: strings.TrimSpace(values.Get("endpoint")),
		Reason:       strings.TrimSpace(values.Get("reason")),
	}
	if raw := strings.TrimSpace(values.Get("statusCode")); raw != "" {
		statusCode, err := strconv.Atoi(raw)
		if err != nil || statusCode < 0 {
			return query, fmt.Errorf("invalid statusCode")
		}
		query.StatusCode = statusCode
		query.StatusCodeSet = true
	}
	if raw := strings.TrimSpace(values.Get("limit")); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			return query, fmt.Errorf("invalid limit")
		}
		query.Limit = limit
	}
	if raw := strings.TrimSpace(values.Get("from")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return query, fmt.Errorf("invalid from")
		}
		query.From = parsed.UTC()
	}
	if raw := strings.TrimSpace(values.Get("to")); raw != "" {
		parsed, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return query, fmt.Errorf("invalid to")
		}
		query.To = parsed.UTC()
	}
	return query, nil
}

func (h *HTTPHandler) serveAdmin(w http.ResponseWriter, r *http.Request, handler http.HandlerFunc) {
	h.withAdmin(handler).ServeHTTP(w, r)
}

func (h *HTTPHandler) serveAdminMutation(w http.ResponseWriter, r *http.Request, handler http.HandlerFunc) {
	h.withAdmin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validAdminRequestOrigin(r) {
			writeJSONError(w, http.StatusForbidden, "cross-site request rejected")
			return
		}
		if !h.allowRate(w, r, "admin_mutation", 60, time.Minute) {
			return
		}
		handler(w, r)
	})).ServeHTTP(w, r)
}

func (h *HTTPHandler) withAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		account, ok := h.adminFromRequest(r)
		if !ok {
			writeJSONError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminContextKey{}, account)))
	})
}

func (h *HTTPHandler) AdminPageMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := h.adminFromRequest(r); !ok {
			http.Redirect(w, r, "/admin/login", http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (h *HTTPHandler) hasValidSession(r *http.Request) bool {
	_, ok := h.adminFromRequest(r)
	return ok
}

func (h *HTTPHandler) adminFromRequest(r *http.Request) (*AdminAccount, bool) {
	cookie, err := r.Cookie(adminSessionCookieName)
	if err != nil {
		return nil, false
	}
	session, ok := h.sessions.get(cookie.Value, h.currentTime())
	if !ok {
		return nil, false
	}
	if h.service == nil {
		return &AdminAccount{ID: session.AccountID, Username: session.Username, Status: AdminAccountStatusActive, Permissions: allAdminPermissions()}, true
	}
	account, err := h.service.GetAdminAccount(session.AccountID)
	if err != nil || account.Status != AdminAccountStatusActive {
		h.sessions.delete(cookie.Value)
		return nil, false
	}
	return account, true
}

func adminFromContext(r *http.Request) *AdminAccount {
	account, _ := r.Context().Value(adminContextKey{}).(*AdminAccount)
	return account
}

func (h *HTTPHandler) allowRate(w http.ResponseWriter, r *http.Request, scope string, limit int, window time.Duration) bool {
	key := scope + ":" + clientIP(r)
	if h.limiter.allow(key, limit, window, h.currentTime()) {
		return true
	}
	writeJSONError(w, http.StatusTooManyRequests, "rate limit exceeded")
	return false
}

func (h *HTTPHandler) currentTime() time.Time {
	if h != nil && h.service != nil {
		return h.service.currentTime()
	}
	return time.Now().UTC()
}

func (h *HTTPHandler) sessionCookie(r *http.Request, token string, expiresAt time.Time) *http.Cookie {
	return &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    token,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(h.admin.SessionTTL.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   requestIsSecure(r),
	}
}

func (h *HTTPHandler) expiredSessionCookie(r *http.Request) *http.Cookie {
	return &http.Cookie{
		Name:     adminSessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   requestIsSecure(r),
	}
}

func (h *HTTPHandler) recordAudit(action, targetType string, targetID int64, detail string) {
	if h == nil || h.service == nil {
		return
	}
	_ = h.service.RecordAudit(action, targetType, targetID, detail)
}

func (h *HTTPHandler) writeServiceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrForbidden):
		writeJSONError(w, http.StatusForbidden, "forbidden")
	case errors.Is(err, ErrRemoteResultExpired):
		writeJSONError(w, http.StatusGone, "remote command result expired")
	case errors.Is(err, sql.ErrNoRows):
		writeJSONError(w, http.StatusNotFound, "not found")
	default:
		writeJSONError(w, http.StatusBadRequest, err.Error())
	}
}

func (s *adminSessionStore) create(account *AdminAccount, now time.Time, ttl time.Duration) (string, time.Time, error) {
	token, err := randomSessionToken()
	if err != nil {
		return "", time.Time{}, err
	}
	expiresAt := now.Add(ttl).UTC()
	s.mu.Lock()
	s.sessions[token] = adminSession{Username: account.Username, AccountID: account.ID, ExpiresAt: expiresAt}
	s.mu.Unlock()
	return token, expiresAt, nil
}

func (s *adminSessionStore) get(token string, now time.Time) (adminSession, bool) {
	if strings.TrimSpace(token) == "" {
		return adminSession{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[token]
	if !ok {
		return adminSession{}, false
	}
	if now.After(session.ExpiresAt) {
		delete(s.sessions, token)
		return adminSession{}, false
	}
	return session, true
}

func (s *adminSessionStore) delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (l *rateLimiter) allow(key string, limit int, window time.Duration, now time.Time) bool {
	if limit <= 0 || window <= 0 {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lastCleanup.IsZero() || now.Sub(l.lastCleanup) >= time.Minute {
		for bucketKey, candidate := range l.buckets {
			if !now.Before(candidate.ResetAt) {
				delete(l.buckets, bucketKey)
			}
		}
		l.lastCleanup = now
	}
	bucket := l.buckets[key]
	if bucket.ResetAt.IsZero() || !now.Before(bucket.ResetAt) {
		bucket = rateBucket{ResetAt: now.Add(window)}
	}
	bucket.Count++
	l.buckets[key] = bucket
	return bucket.Count <= limit
}

func randomSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
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
	if trustedLoopbackProxy(r) {
		if realIP := net.ParseIP(strings.TrimSpace(r.Header.Get("X-Real-IP"))); realIP != nil {
			return realIP.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}

func requestBodyLimit(path string) int64 {
	switch path {
	case "/api/license/remote/result", "/api/license/telemetry/endpoint-errors":
		return remoteRequestBodyLimit
	default:
		return defaultRequestBodyLimit
	}
}

func trustedLoopbackProxy(r *http.Request) bool {
	if r == nil {
		return false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func requestIsSecure(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}
	return trustedLoopbackProxy(r) && strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https")
}

func validAdminRequestOrigin(r *http.Request) bool {
	if r == nil {
		return false
	}
	if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" {
		return sameRequestOrigin(r, origin)
	}
	if referer := strings.TrimSpace(r.Header.Get("Referer")); referer != "" {
		return sameRequestOrigin(r, referer)
	}
	return true
}

func sameRequestOrigin(r *http.Request, value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return false
	}
	expectedScheme := "http"
	if requestIsSecure(r) {
		expectedScheme = "https"
	}
	return strings.EqualFold(parsed.Scheme, expectedScheme) && strings.EqualFold(parsed.Host, r.Host)
}
