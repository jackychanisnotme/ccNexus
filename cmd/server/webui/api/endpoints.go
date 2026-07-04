package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/providercompat"
	"github.com/lich0821/ccNexus/internal/storage"
)

// handleEndpoints handles GET (list) and POST (create) for endpoints
func (h *Handler) handleEndpoints(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.listEndpoints(w, r)
	case http.MethodPost:
		h.createEndpoint(w, r)
	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleEndpointByName handles GET, PUT, DELETE, PATCH for specific endpoint
func (h *Handler) handleEndpointByName(w http.ResponseWriter, r *http.Request) {
	// Extract endpoint name from path
	path := strings.TrimPrefix(r.URL.Path, "/api/endpoints/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		WriteError(w, http.StatusBadRequest, "Endpoint name required")
		return
	}

	name := parts[0]

	// Handle /test and /toggle sub-paths
	if len(parts) > 1 {
		switch parts[1] {
		case "test":
			h.testEndpoint(w, r, name)
			return
		case "toggle":
			h.toggleEndpoint(w, r, name)
			return
		case "credentials":
			h.handleEndpointCredentials(w, r, name, parts[2:])
			return
		}
	}

	switch r.Method {
	case http.MethodGet:
		h.getEndpoint(w, r, name)
	case http.MethodPut:
		h.updateEndpoint(w, r, name)
	case http.MethodDelete:
		h.deleteEndpoint(w, r, name)
	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// listEndpoints returns all endpoints
func (h *Handler) listEndpoints(w http.ResponseWriter, r *http.Request) {
	endpoints, err := h.storage.GetEndpoints()
	if err != nil {
		logger.Error("Failed to get endpoints: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get endpoints")
		return
	}

	// Mask API keys
	for i := range endpoints {
		endpoints[i].APIKey = maskAPIKey(endpoints[i].APIKey)
	}

	tokenPools, err := h.storage.GetAllTokenPoolStats()
	if err != nil {
		logger.Warn("Failed to get token pool stats: %v", err)
		tokenPools = map[string]storage.TokenPoolStats{}
	}

	WriteSuccess(w, map[string]interface{}{
		"endpoints":  endpoints,
		"tokenPools": tokenPools,
	})
}

// getEndpoint returns a specific endpoint
func (h *Handler) getEndpoint(w http.ResponseWriter, r *http.Request, name string) {
	endpoints, err := h.storage.GetEndpoints()
	if err != nil {
		logger.Error("Failed to get endpoints: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get endpoints")
		return
	}

	for _, ep := range endpoints {
		if ep.Name == name {
			ep.APIKey = maskAPIKey(ep.APIKey)
			WriteSuccess(w, ep)
			return
		}
	}

	WriteError(w, http.StatusNotFound, "Endpoint not found")
}

// createEndpoint creates a new endpoint
func (h *Handler) createEndpoint(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name                  string `json:"name"`
		APIUrl                string `json:"apiUrl"`
		APIKey                string `json:"apiKey"`
		AuthMode              string `json:"authMode"`
		Enabled               bool   `json:"enabled"`
		Transformer           string `json:"transformer"`
		Model                 string `json:"model"`
		Thinking              string `json:"thinking"`
		ForceStream           *bool  `json:"forceStream"`
		MaxConcurrentRequests *int   `json:"maxConcurrentRequests"`
		Remark                string `json:"remark"`
		ProxyURL              string `json:"proxyUrl"`
		CloneFrom             string `json:"cloneFrom"` // Clone from existing endpoint name
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// If cloning, inherit omitted fields from source endpoint.
	if req.CloneFrom != "" {
		endpoints, err := h.storage.GetEndpoints()
		if err == nil {
			for _, ep := range endpoints {
				if ep.Name == req.CloneFrom {
					if req.APIKey == "" {
						req.APIKey = ep.APIKey
					}
					if req.Thinking == "" {
						req.Thinking = ep.Thinking
					}
					if req.ForceStream == nil {
						forceStream := ep.ForceStream
						req.ForceStream = &forceStream
					}
					if req.MaxConcurrentRequests == nil {
						maxConcurrentRequests := ep.MaxConcurrentRequests
						req.MaxConcurrentRequests = &maxConcurrentRequests
					}
					if strings.TrimSpace(req.Model) == "" {
						req.Model = ep.Model
					}
					if strings.TrimSpace(req.ProxyURL) == "" {
						req.ProxyURL = ep.ProxyURL
					}
					break
				}
			}
		}
	}

	authMode := config.NormalizeAuthMode(req.AuthMode)
	forceStream := false
	if req.ForceStream != nil {
		forceStream = *req.ForceStream
	}
	maxConcurrentRequests := 0
	if req.MaxConcurrentRequests != nil {
		maxConcurrentRequests = config.NormalizeEndpointMaxConcurrentRequests(*req.MaxConcurrentRequests)
	}
	normalizedEndpoint := config.Endpoint{
		APIUrl:                normalizeAPIUrl(req.APIUrl),
		APIKey:                req.APIKey,
		AuthMode:              authMode,
		Transformer:           req.Transformer,
		Model:                 req.Model,
		Thinking:              req.Thinking,
		ForceStream:           forceStream,
		Remark:                req.Remark,
		ProxyURL:              strings.TrimSpace(req.ProxyURL),
		MaxConcurrentRequests: maxConcurrentRequests,
	}
	if normalizedEndpoint.Transformer == "" {
		normalizedEndpoint.Transformer = "claude"
	}
	normalizedEndpoint.Transformer = providercompat.NormalizeTransformer(normalizedEndpoint.Transformer)
	config.ApplyEndpointAuthModeRules(&normalizedEndpoint)
	authMode = normalizedEndpoint.AuthMode
	req.APIUrl = normalizedEndpoint.APIUrl
	req.APIKey = normalizedEndpoint.APIKey
	req.Transformer = normalizedEndpoint.Transformer
	req.Thinking = normalizedEndpoint.Thinking
	req.ProxyURL = normalizedEndpoint.ProxyURL
	forceStream = normalizedEndpoint.ForceStream
	maxConcurrentRequests = normalizedEndpoint.MaxConcurrentRequests

	// Validate required fields
	if req.Name == "" || req.APIUrl == "" {
		WriteError(w, http.StatusBadRequest, "Name and apiUrl are required")
		return
	}
	if authMode == config.AuthModeAPIKey && req.APIKey == "" {
		WriteError(w, http.StatusBadRequest, "apiKey is required in api_key mode")
		return
	}
	if config.IsTokenPoolAuthMode(authMode) {
		req.APIKey = ""
	}

	// Get current endpoints to determine sort order
	endpoints, err := h.storage.GetEndpoints()
	if err != nil {
		logger.Error("Failed to get endpoints: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get endpoints")
		return
	}

	// Check if endpoint with same name exists
	for _, ep := range endpoints {
		if ep.Name == req.Name {
			WriteError(w, http.StatusConflict, "Endpoint with this name already exists")
			return
		}
	}

	// Create new endpoint
	endpoint := &storage.Endpoint{
		Name:                  req.Name,
		APIUrl:                normalizeAPIUrl(req.APIUrl),
		APIKey:                req.APIKey,
		AuthMode:              authMode,
		Enabled:               req.Enabled,
		Transformer:           req.Transformer,
		Model:                 req.Model,
		Thinking:              req.Thinking,
		ForceStream:           forceStream,
		MaxConcurrentRequests: maxConcurrentRequests,
		Remark:                req.Remark,
		ProxyURL:              strings.TrimSpace(req.ProxyURL),
		SortOrder:             len(endpoints),
		CreatedAt:             time.Now(),
		UpdatedAt:             time.Now(),
	}

	if err := validateEndpointCandidate(h.config, endpoints, storageEndpointToConfig(endpoint), ""); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := h.storage.SaveEndpoint(endpoint); err != nil {
		logger.Error("Failed to save endpoint: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to save endpoint")
		return
	}

	// Update proxy config
	if err := h.reloadConfig(); err != nil {
		logger.Error("Failed to reload config: %v", err)
	}

	endpoint.APIKey = maskAPIKey(endpoint.APIKey)
	WriteSuccess(w, endpoint)
}

// updateEndpoint updates an existing endpoint
func (h *Handler) updateEndpoint(w http.ResponseWriter, r *http.Request, name string) {
	var req struct {
		Name                  string `json:"name"`
		APIUrl                string `json:"apiUrl"`
		APIKey                string `json:"apiKey"`
		AuthMode              string `json:"authMode"`
		Enabled               bool   `json:"enabled"`
		Transformer           string `json:"transformer"`
		Model                 string `json:"model"`
		Thinking              string `json:"thinking"`
		ForceStream           *bool  `json:"forceStream"`
		MaxConcurrentRequests *int   `json:"maxConcurrentRequests"`
		Remark                string `json:"remark"`
		ProxyURL              string `json:"proxyUrl"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if req.Name != "" && strings.TrimSpace(req.Name) == "" {
		WriteError(w, http.StatusBadRequest, "Invalid endpoint name")
		return
	}

	// Get existing endpoint
	endpoints, err := h.storage.GetEndpoints()
	if err != nil {
		logger.Error("Failed to get endpoints: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get endpoints")
		return
	}

	var existing *storage.Endpoint
	for i := range endpoints {
		if endpoints[i].Name == name {
			existing = &endpoints[i]
			break
		}
	}

	if existing == nil {
		WriteError(w, http.StatusNotFound, "Endpoint not found")
		return
	}

	oldName := name
	currentEndpointName := h.proxy.GetCurrentEndpointName()

	// Update fields
	if newName := strings.TrimSpace(req.Name); newName != "" {
		existing.Name = newName
	}
	if req.APIUrl != "" {
		existing.APIUrl = normalizeAPIUrl(req.APIUrl)
	}
	if req.APIKey != "" {
		existing.APIKey = req.APIKey
	}
	existing.ProxyURL = strings.TrimSpace(req.ProxyURL)
	if req.AuthMode != "" {
		existing.AuthMode = config.NormalizeAuthMode(req.AuthMode)
	}
	if existing.AuthMode == "" {
		existing.AuthMode = config.AuthModeAPIKey
	}
	normalizedEndpoint := config.Endpoint{
		Name:                  existing.Name,
		APIUrl:                existing.APIUrl,
		APIKey:                existing.APIKey,
		AuthMode:              existing.AuthMode,
		Enabled:               existing.Enabled,
		Transformer:           existing.Transformer,
		Model:                 existing.Model,
		Thinking:              existing.Thinking,
		ForceStream:           existing.ForceStream,
		Remark:                existing.Remark,
		ProxyURL:              existing.ProxyURL,
		MaxConcurrentRequests: existing.MaxConcurrentRequests,
	}
	if req.ForceStream != nil {
		normalizedEndpoint.ForceStream = *req.ForceStream
	}
	if req.MaxConcurrentRequests != nil {
		normalizedEndpoint.MaxConcurrentRequests = *req.MaxConcurrentRequests
	}
	if normalizedEndpoint.Transformer == "" {
		normalizedEndpoint.Transformer = "claude"
	}
	if req.Transformer != "" {
		normalizedEndpoint.Transformer = providercompat.NormalizeTransformer(req.Transformer)
	}
	config.ApplyEndpointAuthModeRules(&normalizedEndpoint)
	existing.APIUrl = normalizedEndpoint.APIUrl
	existing.APIKey = normalizedEndpoint.APIKey
	existing.AuthMode = normalizedEndpoint.AuthMode
	existing.Transformer = normalizedEndpoint.Transformer
	existing.Thinking = normalizedEndpoint.Thinking
	existing.ForceStream = normalizedEndpoint.ForceStream
	existing.MaxConcurrentRequests = normalizedEndpoint.MaxConcurrentRequests
	if existing.AuthMode == config.AuthModeAPIKey && existing.APIKey == "" {
		WriteError(w, http.StatusBadRequest, "apiKey is required in api_key mode")
		return
	}
	existing.Enabled = req.Enabled
	existing.Transformer = normalizedEndpoint.Transformer
	if req.Model != "" {
		existing.Model = req.Model
	}
	if req.Thinking != "" {
		existing.Thinking = config.NormalizeThinkingEffort(req.Thinking)
	}
	if req.ForceStream != nil {
		existing.ForceStream = *req.ForceStream
	}
	existing.Remark = req.Remark
	existing.UpdatedAt = time.Now()

	if err := validateEndpointCandidate(h.config, endpoints, storageEndpointToConfig(existing), oldName); err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	if oldName != existing.Name {
		if err := h.storage.RenameEndpoint(oldName, existing); err != nil {
			logger.Error("Failed to rename endpoint: %v", err)
			status, message := classifyEndpointRenameError(err)
			WriteError(w, status, message)
			return
		}
		if currentEndpointName == oldName {
			currentEndpointName = existing.Name
		}
	} else {
		if err := h.storage.UpdateEndpoint(existing); err != nil {
			logger.Error("Failed to update endpoint: %v", err)
			WriteError(w, http.StatusInternalServerError, "Failed to update endpoint")
			return
		}
	}

	// Update proxy config
	if err := h.reloadConfigPreservingCurrentName(currentEndpointName); err != nil {
		logger.Error("Failed to reload config: %v", err)
	}

	existing.APIKey = maskAPIKey(existing.APIKey)
	WriteSuccess(w, existing)
}

func validateEndpointCandidate(base *config.Config, storedEndpoints []storage.Endpoint, candidate config.Endpoint, replaceName string) error {
	if base == nil {
		base = config.DefaultConfig()
	}
	candidate.Name = strings.TrimSpace(candidate.Name)
	candidate.APIUrl = normalizeAPIUrl(candidate.APIUrl)
	candidate.ProxyURL = strings.TrimSpace(candidate.ProxyURL)
	if candidate.Transformer == "" {
		candidate.Transformer = providercompat.TransformerClaude
	}
	candidate.Transformer = providercompat.NormalizeTransformer(candidate.Transformer)
	config.ApplyEndpointAuthModeRules(&candidate)

	endpoints := make([]config.Endpoint, 0, len(storedEndpoints)+1)
	replaced := false
	for _, ep := range storedEndpoints {
		if replaceName != "" && ep.Name == replaceName {
			endpoints = append(endpoints, candidate)
			replaced = true
			continue
		}
		endpoints = append(endpoints, storageEndpointToConfig(&ep))
	}
	if !replaced {
		endpoints = append(endpoints, candidate)
	}

	cfg := config.DefaultConfig()
	cfg.ReplaceWith(base)
	cfg.UpdateEndpoints(endpoints)
	return cfg.Validate()
}

func classifyEndpointRenameError(err error) (int, string) {
	switch {
	case errors.Is(err, storage.ErrEndpointNameConflict):
		return http.StatusConflict, "Endpoint with this name already exists"
	case errors.Is(err, storage.ErrEndpointNotFound):
		return http.StatusNotFound, "Endpoint not found"
	case errors.Is(err, storage.ErrInvalidEndpointName):
		return http.StatusBadRequest, "Invalid endpoint name"
	default:
		return http.StatusInternalServerError, "Failed to rename endpoint"
	}
}

// deleteEndpoint deletes an endpoint
func (h *Handler) deleteEndpoint(w http.ResponseWriter, r *http.Request, name string) {
	if err := h.storage.DeleteEndpoint(name); err != nil {
		logger.Error("Failed to delete endpoint: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to delete endpoint")
		return
	}

	// Update proxy config
	if err := h.reloadConfig(); err != nil {
		logger.Error("Failed to reload config: %v", err)
	}

	WriteSuccess(w, map[string]interface{}{
		"message": "Endpoint deleted successfully",
	})
}

// toggleEndpoint enables or disables an endpoint
func (h *Handler) toggleEndpoint(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPatch && r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Get existing endpoint
	endpoints, err := h.storage.GetEndpoints()
	if err != nil {
		logger.Error("Failed to get endpoints: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get endpoints")
		return
	}

	var existing *storage.Endpoint
	for i := range endpoints {
		if endpoints[i].Name == name {
			existing = &endpoints[i]
			break
		}
	}

	if existing == nil {
		WriteError(w, http.StatusNotFound, "Endpoint not found")
		return
	}

	existing.Enabled = req.Enabled
	existing.UpdatedAt = time.Now()

	if err := h.storage.UpdateEndpoint(existing); err != nil {
		logger.Error("Failed to update endpoint: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to update endpoint")
		return
	}

	// Update proxy config
	if err := h.reloadConfig(); err != nil {
		logger.Error("Failed to reload config: %v", err)
	}

	WriteSuccess(w, map[string]interface{}{
		"enabled": existing.Enabled,
	})
}

// handleCurrentEndpoint returns the current active endpoint
func (h *Handler) handleCurrentEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	currentEndpoint := h.proxy.GetCurrentEndpointName()
	if currentEndpoint == "" {
		WriteError(w, http.StatusNotFound, "No endpoints configured")
		return
	}

	WriteSuccess(w, map[string]interface{}{
		"name": currentEndpoint,
	})
}

// handleSwitchEndpoint switches to a specific endpoint
func (h *Handler) handleSwitchEndpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Name string `json:"name"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Verify endpoint exists
	endpoints := h.config.GetEndpoints()
	found := false
	for _, ep := range endpoints {
		if ep.Name == req.Name && ep.Enabled {
			found = true
			break
		}
	}

	if !found {
		WriteError(w, http.StatusNotFound, "Endpoint not found or not enabled")
		return
	}

	if err := h.proxy.SetCurrentEndpoint(req.Name); err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}

	WriteSuccess(w, map[string]interface{}{
		"message": "Endpoint switched successfully",
		"name":    req.Name,
	})
}

// handleReorderEndpoints reorders endpoints
func (h *Handler) handleReorderEndpoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		Names []string `json:"names"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	// Get all endpoints
	endpoints, err := h.storage.GetEndpoints()
	if err != nil {
		logger.Error("Failed to get endpoints: %v", err)
		WriteError(w, http.StatusInternalServerError, "Failed to get endpoints")
		return
	}

	// Create a map for quick lookup
	endpointMap := make(map[string]*storage.Endpoint)
	for i := range endpoints {
		endpointMap[endpoints[i].Name] = &endpoints[i]
	}

	// Update sort order
	for i, name := range req.Names {
		if ep, ok := endpointMap[name]; ok {
			ep.SortOrder = i
			ep.UpdatedAt = time.Now()
			if err := h.storage.UpdateEndpoint(ep); err != nil {
				logger.Error("Failed to update endpoint sort order: %v", err)
			}
		}
	}

	// Update proxy config
	if err := h.reloadConfig(); err != nil {
		logger.Error("Failed to reload config: %v", err)
	}

	WriteSuccess(w, map[string]interface{}{
		"message": "Endpoints reordered successfully",
	})
}

// reloadConfig reloads the configuration from storage and updates the proxy
func (h *Handler) reloadConfig() error {
	return h.reloadConfigPreservingCurrentName(h.proxy.GetCurrentEndpointName())
}

func (h *Handler) reloadConfigPreservingCurrentName(name string) error {
	adapter := storage.NewConfigStorageAdapter(h.storage)
	cfg, err := config.LoadFromStorage(adapter)
	if err != nil {
		return err
	}

	h.config.ReplaceWith(cfg)
	if err := h.proxy.UpdateConfigPreservingCurrentName(h.config, name); err != nil {
		return err
	}
	h.resetCodexAuthManager()
	return nil
}

// maskAPIKey masks an API key, showing only the last 4 characters
func maskAPIKey(key string) string {
	if key == "" {
		return ""
	}
	if len(key) <= 4 {
		return "****"
	}
	return "****" + key[len(key)-4:]
}

// normalizeAPIUrl ensures the API URL has the correct format
func normalizeAPIUrl(apiUrl string) string {
	return strings.TrimSuffix(apiUrl, "/")
}
