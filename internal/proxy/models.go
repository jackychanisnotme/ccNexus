package proxy

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/providercompat"
)

// ModelInfo represents a single model information
type ModelInfo struct {
	ID         string `json:"id"`
	Object     string `json:"object"`
	Created    int64  `json:"created"`
	OwnedBy    string `json:"owned_by"`
	EndpointID string `json:"endpoint_id"` // Source endpoint identifier
}

// ModelsCache represents cached models data with TTL
type ModelsCache struct {
	data      []ModelInfo
	updatedAt time.Time
	ttl       time.Duration
	mu        sync.RWMutex
}

// NewModelsCache creates a new models cache
func NewModelsCache(ttlMinutes int) *ModelsCache {
	if ttlMinutes <= 0 {
		ttlMinutes = 30 // Default 30 minutes
	}
	return &ModelsCache{
		data:      []ModelInfo{},
		updatedAt: time.Time{},
		ttl:       time.Duration(ttlMinutes) * time.Minute,
	}
}

// Get returns cached data if valid
func (c *ModelsCache) Get() ([]ModelInfo, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if time.Since(c.updatedAt) > c.ttl {
		return nil, false
	}
	return c.data, true
}

// Set updates cached data
func (c *ModelsCache) Set(data []ModelInfo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data = data
	c.updatedAt = time.Now()
}

// Clear clears the cache
func (c *ModelsCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.data = []ModelInfo{}
	c.updatedAt = time.Time{}
}

// fetchModelsFromEndpoint fetches models from a specific endpoint
func (p *Proxy) fetchModelsFromEndpoint(ep config.Endpoint) ([]ModelInfo, error) {
	var modelsURL string
	var req *http.Request
	var err error

	switch providercompat.NormalizeTransformer(ep.Transformer) {
	case "openai", "deepseek", "kimi", "poe", "openai2":
		// OpenAI compatible endpoints
		var candidates []string
		if ep.AuthMode == config.AuthModeCodexTokenPool {
			candidates = []string{providercompat.JoinBaseURLAndPath(ep.APIUrl, "/models")}
		} else {
			c, err := providercompat.BuildOpenAIModelURLCandidates(ep.APIUrl, ep.Transformer)
			if err != nil {
				return nil, err
			}
			candidates = c
		}
		var lastErr error
		for _, modelsURL = range candidates {
			req, err = http.NewRequest("GET", modelsURL, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create request: %w", err)
			}
			// Add authorization header
			if ep.AuthMode == config.AuthModeAPIKey && ep.APIKey != "" {
				req.Header.Set("Authorization", "Bearer "+ep.APIKey)
			}
			req.Header.Set("User-Agent", "AINexus/1.0")

			client := p.httpClientForEndpoint(ep, modelsURL, 10*time.Second)
			models, fetchErr := p.fetchOpenAIModelsWithRequest(client, req, ep.Name)
			if fetchErr == nil {
				return models, nil
			}
			lastErr = fetchErr
			if !isModelsCandidateFallbackError(fetchErr) {
				return nil, fetchErr
			}
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no models URL candidates")

	case "gemini":
		// Google Gemini endpoints
		baseURL := strings.TrimSuffix(ep.APIUrl, "/")
		if strings.Contains(baseURL, "/v1") {
			modelsURL = baseURL + "/models"
		} else {
			modelsURL = baseURL + "/v1beta/models"
		}
		// Add API key as query parameter
		if ep.AuthMode == config.AuthModeAPIKey && ep.APIKey != "" {
			modelsURL = modelsURL + "?key=" + ep.APIKey
		}
		req, err = http.NewRequest("GET", modelsURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

	default:
		// For transformers without /v1/models support (claude, codex)
		return nil, fmt.Errorf("transformer %s does not support /v1/models", ep.Transformer)
	}

	// Set User-Agent
	req.Header.Set("User-Agent", "AINexus/1.0")

	// Execute request
	client := p.httpClientForEndpoint(ep, modelsURL, 10*time.Second)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if providercompat.IsGeminiTransformer(ep.Transformer) {
		return decodeGeminiModels(resp, ep.Name)
	}
	return decodeOpenAIModels(resp, ep.Name)
}

func (p *Proxy) fetchOpenAIModelsWithRequest(client *http.Client, req *http.Request, endpointName string) ([]ModelInfo, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	return decodeOpenAIModels(resp, endpointName)
}

func decodeOpenAIModels(resp *http.Response, endpointName string) ([]ModelInfo, error) {
	var result struct {
		Data []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	if result.Data == nil {
		return nil, fmt.Errorf("models response schema missing data")
	}

	models := make([]ModelInfo, len(result.Data))
	for i, m := range result.Data {
		models[i] = ModelInfo{
			ID:         m.ID,
			Object:     m.Object,
			Created:    m.Created,
			OwnedBy:    m.OwnedBy,
			EndpointID: endpointName,
		}
	}

	return models, nil
}

func decodeGeminiModels(resp *http.Response, endpointName string) ([]ModelInfo, error) {
	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode Gemini models response: %w", err)
	}
	if result.Models == nil {
		return nil, fmt.Errorf("Gemini models response schema missing models")
	}
	models := make([]ModelInfo, 0, len(result.Models))
	for _, m := range result.Models {
		id := strings.TrimSpace(m.Name)
		id = strings.TrimPrefix(id, "models/")
		if id == "" {
			continue
		}
		models = append(models, ModelInfo{
			ID:         id,
			Object:     "model",
			Created:    time.Now().Unix(),
			OwnedBy:    "google",
			EndpointID: endpointName,
		})
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("Gemini models response contained no usable models")
	}
	return models, nil
}

func (p *Proxy) httpClientForEndpoint(endpoint config.Endpoint, targetURL string, timeout time.Duration) *http.Client {
	client := &http.Client{Timeout: timeout}
	if p != nil && p.httpClient != nil {
		client.Transport = p.httpClient.Transport
	}

	var globalProxy *config.ProxyConfig
	var codexProxy *config.ProxyConfig
	if p != nil && p.config != nil {
		globalProxy = p.config.GetProxy()
		codexProxy = p.config.GetCodexProxy()
	}

	proxyURL := config.ResolveEndpointProxyURL(&endpoint, targetURL, globalProxy, codexProxy)
	if proxyURL == "" {
		return client
	}

	transport, err := CreateProxyTransport(proxyURL)
	if err != nil {
		logger.Warn("Failed to create endpoint proxy transport for models: %v", err)
		return client
	}
	client.Transport = transport
	return client
}

func isModelsCandidateFallbackError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "status code: 404") ||
		strings.Contains(msg, "status code: 405") ||
		strings.Contains(msg, "failed to decode response")
}

// getDefaultModels returns default models for endpoints that don't support /v1/models
func (p *Proxy) getDefaultModels(ep config.Endpoint) []ModelInfo {
	var modelID string
	var ownedBy string

	switch providercompat.NormalizeTransformer(ep.Transformer) {
	case "claude":
		// Claude endpoints
		if ep.Model != "" {
			modelID = ep.Model
		} else {
			modelID = "claude-sonnet-4-20250514" // Default Claude model
		}
		ownedBy = "anthropic"

	case "openai2":
		// Codex endpoints
		if ep.Model != "" {
			modelID = ep.Model
		} else if ep.AuthMode == config.AuthModeCodexTokenPool {
			modelID = "gpt-5-codex" // Default Codex model
		} else {
			modelID = "gpt-4o" // Default OpenAI model
		}
		ownedBy = "openai"

	case "deepseek", "kimi", "poe", "openai":
		if ep.Model != "" {
			modelID = ep.Model
		} else {
			modelID = providercompat.DefaultModel(ep.Transformer)
		}
		ownedBy = providercompat.Owner(ep.Transformer)

	default:
		// Fallback for any other transformer
		if ep.Model != "" {
			modelID = ep.Model
		} else {
			modelID = "unknown-model"
		}
		ownedBy = strings.ToLower(ep.Transformer)
	}

	return []ModelInfo{
		{
			ID:         modelID,
			Object:     "model",
			Created:    time.Now().Unix(),
			OwnedBy:    ownedBy,
			EndpointID: ep.Name,
		},
	}
}

// handleModels handles GET /v1/models requests
func (p *Proxy) handleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Check for refresh parameter
	refresh := r.URL.Query().Get("refresh") == "true"
	runtimeSnapshot := p.runtimeSnapshot()
	refreshEnabled := runtimeSnapshot.config.ModelsCacheRefreshEnabled

	if refresh && !refreshEnabled {
		http.Error(w, "Refresh is disabled in configuration", http.StatusForbidden)
		return
	}

	allModels := p.loadModelsForResponse(runtimeSnapshot, refresh)

	// Write response
	p.writeModelsResponse(w, allModels)
}

func (p *Proxy) loadModelsForResponse(runtimeSnapshot *proxyRuntimeSnapshot, refresh bool) []ModelInfo {
	if !refresh {
		if cached, ok := p.modelsCache.Get(); ok {
			return cached
		}
	}

	// Fetch from endpoints
	endpoints := runtimeSnapshot.endpoints
	allModels := []ModelInfo{}
	allFailed := true

	for _, ep := range endpoints {
		if !ep.Enabled {
			continue
		}

		var models []ModelInfo
		var err error

		// Try to fetch from endpoint's /v1/models API
		models, err = p.fetchModelsFromEndpoint(ep)
		if err != nil {
			// If fetch fails, use default models for this endpoint
			logger.Debug("Failed to fetch models from %s: %v", ep.Name, err)
			models = p.getDefaultModels(ep)
		} else {
			allFailed = false
		}

		allModels = append(allModels, models...)
	}

	// If all endpoints failed, still return the aggregated default models
	if allFailed {
		logger.Debug("All endpoints failed to fetch models, returning default models")
	}

	// Cache the result
	p.modelsCache.Set(allModels)

	return allModels
}

// writeModelsResponse writes the models list response
func (p *Proxy) writeModelsResponse(w http.ResponseWriter, models []ModelInfo) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	response := struct {
		Object string      `json:"object"`
		Data   []ModelInfo `json:"data"`
	}{
		Object: "list",
		Data:   models,
	}

	if err := json.NewEncoder(w).Encode(response); err != nil {
		logger.Debug("Failed to encode models response: %v", err)
	}
}

// refreshModelsCache refreshes the models cache in background
func (p *Proxy) refreshModelsCache() {
	logger.Debug("Refreshing models cache in background")

	runtimeSnapshot := p.runtimeSnapshot()
	endpoints := runtimeSnapshot.endpoints
	allModels := []ModelInfo{}

	for _, ep := range endpoints {
		if !ep.Enabled {
			continue
		}

		var models []ModelInfo
		var err error

		models, err = p.fetchModelsFromEndpoint(ep)
		if err != nil {
			logger.Debug("Background refresh: failed to fetch models from %s: %v", ep.Name, err)
			models = p.getDefaultModels(ep)
		}

		allModels = append(allModels, models...)
	}

	p.modelsCache.Set(allModels)
	logger.Debug("Models cache refreshed, total models: %d", len(allModels))
}
