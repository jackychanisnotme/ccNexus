package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lich0821/ccNexus/internal/claudeoauth"
	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/storage"
)

type importCredentialItem struct {
	IDToken      string `json:"id_token"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
	LastRefresh  string `json:"last_refresh"`
	Email        string `json:"email"`
	Type         string `json:"type"`
	Expired      string `json:"expired"`
	Remark       string `json:"remark"`
	Enabled      *bool  `json:"enabled"`
}

type importCredentialsRequest struct {
	Items     []importCredentialItem `json:"items"`
	Overwrite bool                   `json:"overwrite"`
	Remark    string                 `json:"remark"`
}

func (h *Handler) handleEndpointCredentials(w http.ResponseWriter, r *http.Request, endpointName string, parts []string) {
	endpoint, err := h.getEndpointByName(endpointName)
	if err != nil {
		logger.Error("Failed to get endpoint: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get endpoint")
		return
	}
	if endpoint == nil {
		WriteError(w, http.StatusNotFound, "Endpoint not found")
		return
	}

	if len(parts) == 0 || parts[0] == "" {
		switch r.Method {
		case http.MethodGet:
			h.listEndpointCredentials(w, r, endpointName)
		case http.MethodPost:
			h.importEndpointCredentials(w, r, endpoint)
		default:
			WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
		return
	}

	switch parts[0] {
	case "auth":
		h.handleEndpointCredentialAuth(w, r, endpoint, parts[1:])
		return
	case "claude-oauth":
		h.handleClaudeOAuthCredentials(w, r, endpoint, parts[1:])
		return
	case "import":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		h.importEndpointCredentials(w, r, endpoint)
		return
	case "stats":
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		h.getEndpointCredentialStats(w, r, endpointName)
		return
	}

	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, http.StatusBadRequest, "Invalid credential id")
		return
	}

	switch r.Method {
	case http.MethodPatch:
		h.updateEndpointCredential(w, r, endpointName, id)
	case http.MethodDelete:
		h.deleteEndpointCredential(w, r, endpointName, id)
	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (h *Handler) handleEndpointCredentialAuth(w http.ResponseWriter, r *http.Request, endpoint *storage.Endpoint, parts []string) {
	if endpoint == nil {
		WriteError(w, http.StatusNotFound, "Endpoint not found")
		return
	}
	if h.codexAuth == nil {
		WriteError(w, http.StatusInternalServerError, "Codex auth unavailable")
		return
	}
	if config.NormalizeAuthMode(endpoint.AuthMode) != config.AuthModeCodexTokenPool {
		WriteError(w, http.StatusBadRequest, "Codex Token Pool endpoint required")
		return
	}
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		WriteError(w, http.StatusBadRequest, "Auth action required")
		return
	}

	switch parts[0] {
	case "start":
		if len(parts) != 1 {
			WriteError(w, http.StatusBadRequest, "Invalid auth path")
			return
		}
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		result, err := h.codexAuth.Start(r.Context(), storageEndpointToConfig(endpoint))
		if err != nil {
			WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		WriteSuccess(w, result)
	default:
		if len(parts) != 1 {
			WriteError(w, http.StatusBadRequest, "Invalid auth path")
			return
		}
		loginID := strings.TrimSpace(parts[0])
		switch r.Method {
		case http.MethodGet:
			result, err := h.codexAuth.Status(loginID)
			if err != nil {
				WriteError(w, http.StatusNotFound, err.Error())
				return
			}
			WriteSuccess(w, result)
		case http.MethodDelete:
			if err := h.codexAuth.Cancel(loginID); err != nil {
				WriteError(w, http.StatusNotFound, err.Error())
				return
			}
			WriteSuccess(w, map[string]interface{}{"message": "Auth canceled"})
		default:
			WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		}
	}
}

func storageEndpointToConfig(endpoint *storage.Endpoint) config.Endpoint {
	if endpoint == nil {
		return config.Endpoint{}
	}
	return config.Endpoint{
		Name:        endpoint.Name,
		APIUrl:      endpoint.APIUrl,
		APIKey:      endpoint.APIKey,
		AuthMode:    endpoint.AuthMode,
		Enabled:     endpoint.Enabled,
		Transformer: endpoint.Transformer,
		Model:       endpoint.Model,
		Thinking:    endpoint.Thinking,
		ForceStream: endpoint.ForceStream,
		Remark:      endpoint.Remark,
		ProxyURL:    endpoint.ProxyURL,
	}
}

func defaultCredentialProviderType(endpoint *storage.Endpoint) string {
	if endpoint != nil && config.NormalizeAuthMode(endpoint.AuthMode) == config.AuthModeClaudeOAuthTokenPool {
		return storage.ProviderTypeClaudeOAuth
	}
	return storage.ProviderTypeCodex
}

func (h *Handler) handleClaudeOAuthCredentials(w http.ResponseWriter, r *http.Request, endpoint *storage.Endpoint, parts []string) {
	if endpoint == nil {
		WriteError(w, http.StatusNotFound, "Endpoint not found")
		return
	}
	if config.NormalizeAuthMode(endpoint.AuthMode) != config.AuthModeClaudeOAuthTokenPool {
		WriteError(w, http.StatusBadRequest, "Claude OAuth Token Pool endpoint required")
		return
	}
	if len(parts) != 1 || strings.TrimSpace(parts[0]) == "" {
		WriteError(w, http.StatusBadRequest, "Claude OAuth action required")
		return
	}

	switch parts[0] {
	case "discover":
		if r.Method != http.MethodGet {
			WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		candidates, err := claudeoauth.Discover(claudeoauth.DiscoverOptions{})
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteSuccess(w, map[string]interface{}{
			"credentials": claudeoauth.Preview(candidates),
		})
	case "import":
		if r.Method != http.MethodPost {
			WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
			return
		}
		h.importClaudeOAuthCredential(w, r, endpoint)
	default:
		WriteError(w, http.StatusBadRequest, "Invalid Claude OAuth action")
	}
}

func (h *Handler) importClaudeOAuthCredential(w http.ResponseWriter, r *http.Request, endpoint *storage.Endpoint) {
	var req struct {
		ID        string `json:"id"`
		Token     string `json:"token"`
		AccountID string `json:"accountId"`
		Email     string `json:"email"`
		Remark    string `json:"remark"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	token := strings.TrimSpace(req.Token)
	if token != "" {
		parsed, err := claudeoauth.ParseSetupToken(token)
		if err != nil {
			WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		token = parsed
	} else if strings.TrimSpace(req.ID) != "" {
		candidates, err := claudeoauth.Discover(claudeoauth.DiscoverOptions{})
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, candidate := range candidates {
			if candidate.ID == strings.TrimSpace(req.ID) {
				token = candidate.Token
				break
			}
		}
	}
	if strings.TrimSpace(token) == "" {
		WriteError(w, http.StatusBadRequest, "Claude OAuth token or discovery id is required")
		return
	}

	created, updated, skipped, err := h.saveClaudeOAuthCredential(endpoint.Name, token, strings.TrimSpace(req.AccountID), strings.TrimSpace(req.Email), strings.TrimSpace(req.Remark), req.Overwrite)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	WriteSuccess(w, map[string]interface{}{
		"created":   created,
		"updated":   updated,
		"skipped":   skipped,
		"failed":    0,
		"processed": 1,
		"errors":    []string{},
	})
}

func (h *Handler) saveClaudeOAuthCredential(endpointName string, token string, accountID string, email string, remark string, overwrite bool) (int, int, int, error) {
	existing, err := h.storage.GetEndpointCredentials(endpointName)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("failed to get existing credentials")
	}
	for _, cred := range existing {
		if cred.ProviderType != storage.ProviderTypeClaudeOAuth || cred.AccessToken != token {
			continue
		}
		if !overwrite {
			return 0, 0, 1, nil
		}
		cred.AccountID = accountID
		cred.Email = email
		cred.Remark = remark
		cred.Status = "active"
		cred.Enabled = true
		cred.FailureCount = 0
		cred.CooldownUntil = nil
		cred.LastError = ""
		if err := h.storage.UpdateEndpointCredential(&cred); err != nil {
			return 0, 0, 0, fmt.Errorf("failed to update credential")
		}
		return 0, 1, 0, nil
	}

	cred := storage.EndpointCredential{
		EndpointName: endpointName,
		ProviderType: storage.ProviderTypeClaudeOAuth,
		AccountID:    accountID,
		Email:        email,
		AccessToken:  token,
		Status:       "active",
		Enabled:      true,
		Remark:       remark,
	}
	if err := h.storage.SaveEndpointCredential(&cred); err != nil {
		return 0, 0, 0, fmt.Errorf("failed to save credential")
	}
	return 1, 0, 0, nil
}

func (h *Handler) listEndpointCredentials(w http.ResponseWriter, r *http.Request, endpointName string) {
	credentials, err := h.storage.GetEndpointCredentials(endpointName)
	if err != nil {
		logger.Error("Failed to get endpoint credentials: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get endpoint credentials")
		return
	}
	rateLimits, err := h.storage.GetCredentialRateLimitsByEndpoint(endpointName)
	if err != nil {
		logger.Warn("Failed to load rate limits: %v", err)
		rateLimits = nil
	}

	stats, err := h.storage.GetTokenPoolStats(endpointName)
	if err != nil {
		logger.Warn("Failed to get credential stats: %v", err)
	}

	for i := range credentials {
		credentials[i].AccessToken = maskToken(credentials[i].AccessToken)
		credentials[i].RefreshToken = maskToken(credentials[i].RefreshToken)
		credentials[i].IDToken = maskToken(credentials[i].IDToken)
		if rateLimits != nil {
			if entry, ok := rateLimits[credentials[i].ID]; ok {
				credentials[i].RateLimits = entry
			}
		}
	}

	WriteSuccess(w, map[string]interface{}{
		"credentials": credentials,
		"stats":       stats,
	})
}

func (h *Handler) importEndpointCredentials(w http.ResponseWriter, r *http.Request, endpoint *storage.Endpoint) {
	if endpoint == nil {
		WriteError(w, http.StatusNotFound, "Endpoint not found")
		return
	}
	endpointName := endpoint.Name
	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	req, items, err := parseImportCredentialsPayload(rawBody)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	existing, err := h.storage.GetEndpointCredentials(endpointName)
	if err != nil {
		logger.Error("Failed to get existing credentials: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get existing credentials")
		return
	}

	accountIndex := make(map[string]storage.EndpointCredential)
	emailIndex := make(map[string]storage.EndpointCredential)
	for _, cred := range existing {
		if cred.AccountID != "" {
			accountIndex[cred.AccountID] = cred
		}
		if cred.Email != "" {
			emailIndex[cred.Email] = cred
		}
	}

	created := 0
	updated := 0
	skipped := 0
	failed := 0
	errors := make([]string, 0)

	for i, item := range items {
		if strings.TrimSpace(item.AccessToken) == "" {
			failed++
			errors = append(errors, fmt.Sprintf("item[%d]: access_token is required", i))
			continue
		}

		expiresAt, err := parseOptionalRFC3339(item.Expired)
		if err != nil {
			failed++
			errors = append(errors, fmt.Sprintf("item[%d]: invalid expired: %v", i, err))
			continue
		}

		lastRefresh, err := parseOptionalRFC3339(item.LastRefresh)
		if err != nil {
			failed++
			errors = append(errors, fmt.Sprintf("item[%d]: invalid last_refresh: %v", i, err))
			continue
		}

		enabled := true
		if item.Enabled != nil {
			enabled = *item.Enabled
		}

		cred := storage.EndpointCredential{
			EndpointName: endpointName,
			ProviderType: strings.TrimSpace(item.Type),
			AccountID:    strings.TrimSpace(item.AccountID),
			Email:        strings.TrimSpace(item.Email),
			AccessToken:  strings.TrimSpace(item.AccessToken),
			RefreshToken: strings.TrimSpace(item.RefreshToken),
			IDToken:      strings.TrimSpace(item.IDToken),
			LastRefresh:  lastRefresh,
			ExpiresAt:    expiresAt,
			Status:       "active",
			Enabled:      enabled,
			Remark:       strings.TrimSpace(item.Remark),
		}
		if cred.ProviderType == "" {
			cred.ProviderType = defaultCredentialProviderType(endpoint)
		}
		if cred.Remark == "" {
			cred.Remark = strings.TrimSpace(req.Remark)
		}

		existingCred := findExistingCredential(accountIndex, emailIndex, &cred)
		if existingCred == nil {
			if err := h.storage.SaveEndpointCredential(&cred); err != nil {
				failed++
				errors = append(errors, fmt.Sprintf("item[%d]: save failed: %v", i, err))
				continue
			}
			created++
		} else {
			if !req.Overwrite {
				skipped++
				continue
			}

			cred.ID = existingCred.ID
			if item.Enabled == nil {
				cred.Enabled = existingCred.Enabled
			}
			if cred.Remark == "" {
				cred.Remark = existingCred.Remark
			}
			if cred.LastRefresh == nil {
				cred.LastRefresh = existingCred.LastRefresh
			}
			if cred.ExpiresAt == nil {
				cred.ExpiresAt = existingCred.ExpiresAt
			}
			if cred.RefreshToken == "" {
				cred.RefreshToken = existingCred.RefreshToken
			}
			if cred.IDToken == "" {
				cred.IDToken = existingCred.IDToken
			}

			if err := h.storage.UpdateEndpointCredential(&cred); err != nil {
				failed++
				errors = append(errors, fmt.Sprintf("item[%d]: update failed: %v", i, err))
				continue
			}
			updated++
		}

		if cred.AccountID != "" {
			accountIndex[cred.AccountID] = cred
		}
		if cred.Email != "" {
			emailIndex[cred.Email] = cred
		}
	}

	WriteSuccess(w, map[string]interface{}{
		"created":   created,
		"updated":   updated,
		"skipped":   skipped,
		"failed":    failed,
		"processed": len(items),
		"errors":    errors,
	})
}

func (h *Handler) updateEndpointCredential(w http.ResponseWriter, r *http.Request, endpointName string, id int64) {
	var req struct {
		AccessToken  *string `json:"accessToken"`
		RefreshToken *string `json:"refreshToken"`
		IDToken      *string `json:"idToken"`
		Remark       *string `json:"remark"`
		Enabled      *bool   `json:"enabled"`
		ExpiresAt    *string `json:"expiresAt"`
		LastRefresh  *string `json:"lastRefresh"`
		Status       *string `json:"status"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	cred, err := h.storage.GetCredentialByID(id)
	if err != nil {
		logger.Error("Failed to get credential: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get credential")
		return
	}
	if cred == nil || cred.EndpointName != endpointName {
		WriteError(w, http.StatusNotFound, "Credential not found")
		return
	}

	if req.AccessToken != nil {
		token := strings.TrimSpace(*req.AccessToken)
		if token == "" {
			WriteError(w, http.StatusBadRequest, "accessToken cannot be empty")
			return
		}
		cred.AccessToken = token
	}
	if req.RefreshToken != nil {
		cred.RefreshToken = strings.TrimSpace(*req.RefreshToken)
	}
	if req.IDToken != nil {
		cred.IDToken = strings.TrimSpace(*req.IDToken)
	}
	if req.Remark != nil {
		cred.Remark = strings.TrimSpace(*req.Remark)
	}
	if req.Enabled != nil {
		cred.Enabled = *req.Enabled
	}
	if req.ExpiresAt != nil {
		expiresAt, err := parseOptionalRFC3339(*req.ExpiresAt)
		if err != nil {
			WriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid expiresAt: %v", err))
			return
		}
		cred.ExpiresAt = expiresAt
	}
	if req.LastRefresh != nil {
		lastRefresh, err := parseOptionalRFC3339(*req.LastRefresh)
		if err != nil {
			WriteError(w, http.StatusBadRequest, fmt.Sprintf("invalid lastRefresh: %v", err))
			return
		}
		cred.LastRefresh = lastRefresh
	}
	if req.Status != nil {
		status := strings.TrimSpace(*req.Status)
		if status != "" {
			if !isAllowedCredentialStatus(status) {
				WriteError(w, http.StatusBadRequest, "invalid status")
				return
			}
			cred.Status = status
			if status == "active" {
				// Manual reactivation must clear cooldown/failure state, otherwise
				// deriveCredentialStatus keeps reporting cooldown until it expires.
				cred.CooldownUntil = nil
				cred.FailureCount = 0
				cred.LastError = ""
			}
		}
	}

	if err := h.storage.UpdateEndpointCredential(cred); err != nil {
		logger.Error("Failed to update credential: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to update credential")
		return
	}

	updated, err := h.storage.GetCredentialByID(id)
	if err != nil {
		logger.Error("Failed to reload credential: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to reload credential")
		return
	}
	if updated == nil {
		WriteError(w, http.StatusNotFound, "Credential not found")
		return
	}
	updated.AccessToken = maskToken(updated.AccessToken)
	updated.RefreshToken = maskToken(updated.RefreshToken)
	updated.IDToken = maskToken(updated.IDToken)
	WriteSuccess(w, updated)
}

func (h *Handler) deleteEndpointCredential(w http.ResponseWriter, r *http.Request, endpointName string, id int64) {
	if err := h.storage.DeleteEndpointCredential(endpointName, id); err != nil {
		logger.Error("Failed to delete credential: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to delete credential")
		return
	}

	WriteSuccess(w, map[string]interface{}{
		"message": "Credential deleted successfully",
	})
}

func (h *Handler) getEndpointCredentialStats(w http.ResponseWriter, r *http.Request, endpointName string) {
	stats, err := h.storage.GetTokenPoolStats(endpointName)
	if err != nil {
		logger.Error("Failed to get token pool stats: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get token pool stats")
		return
	}

	WriteSuccess(w, stats)
}

func (h *Handler) getEndpointByName(name string) (*storage.Endpoint, error) {
	endpoints, err := h.storage.GetEndpoints()
	if err != nil {
		return nil, err
	}
	for i := range endpoints {
		if endpoints[i].Name == name {
			return &endpoints[i], nil
		}
	}
	return nil, nil
}

func findExistingCredential(accountIndex map[string]storage.EndpointCredential, emailIndex map[string]storage.EndpointCredential, cred *storage.EndpointCredential) *storage.EndpointCredential {
	if cred.AccountID != "" {
		if existing, ok := accountIndex[cred.AccountID]; ok {
			return &existing
		}
	}
	if cred.Email != "" {
		if existing, ok := emailIndex[cred.Email]; ok {
			return &existing
		}
	}
	return nil
}

func parseImportCredentialsPayload(rawBody []byte) (*importCredentialsRequest, []importCredentialItem, error) {
	var req importCredentialsRequest
	if err := json.Unmarshal(rawBody, &req); err == nil && len(req.Items) > 0 {
		return &req, req.Items, nil
	}

	var items []importCredentialItem
	if err := json.Unmarshal(rawBody, &items); err == nil && len(items) > 0 {
		return &importCredentialsRequest{Items: items}, items, nil
	}

	var single importCredentialItem
	if err := json.Unmarshal(rawBody, &single); err == nil && strings.TrimSpace(single.AccessToken) != "" {
		return &importCredentialsRequest{Items: []importCredentialItem{single}}, []importCredentialItem{single}, nil
	}

	return nil, nil, fmt.Errorf("request body must be a credential object, array, or {items:[...]}")
}

func parseOptionalRFC3339(value string) (*time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339, trimmed)
	if err != nil {
		return nil, err
	}
	utc := t.UTC()
	return &utc, nil
}

func isAllowedCredentialStatus(status string) bool {
	switch status {
	case "active", "invalid", "cooldown":
		return true
	default:
		return false
	}
}

func maskToken(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	if len(token) <= 10 {
		return "****"
	}
	return token[:6] + "..." + token[len(token)-4:]
}
