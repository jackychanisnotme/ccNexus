package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/storage"
)

// SSEEvent represents a Server-Sent Event
type SSEEvent struct {
	Event string
	Data  string
}

// Usage represents token usage information from API response
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// APIResponse represents the structure of API responses to extract usage
type APIResponse struct {
	Usage Usage `json:"usage"`
}

// Proxy represents the proxy server
type Proxy struct {
	config                   *config.Config
	configEndpointsSnapshot  []config.Endpoint
	storage                  *storage.SQLiteStorage
	stats                    *Stats
	currentIndex             int
	mu                       sync.RWMutex
	server                   *http.Server
	httpClient               *http.Client                  // Reusable HTTP client with connection pool
	activeRequests           map[string]int                // tracks active request count by endpoint name
	activeRequestsMu         sync.RWMutex                  // protects activeRequests map
	endpointCtx              map[string]context.Context    // context per endpoint for cancellation
	endpointCancel           map[string]context.CancelFunc // cancel functions per endpoint
	ctxMu                    sync.RWMutex                  // protects context maps
	onEndpointSuccess        func(endpointName string)     // callback when endpoint request succeeds
	onCurrentEndpointChanged func(EndpointCurrentEvent)
	onEndpointRuntimeChanged func(EndpointRuntimeEvent)
	modelsCache              *ModelsCache                // Cache for /v1/models endpoint
	resolver                 *EndpointResolver           // 端点解析器，用于解析客户端指定的端点
	retrySleep               func(time.Duration)         // injectable sleep hook for retry backoff tests
	endpointCooldowns        map[string]endpointCooldown // temporary request-plan skips for deterministic endpoint failures
	cooldownMu               sync.RWMutex                // protects endpointCooldowns
	circuitBreakerMu         sync.Mutex
	endpointCircuitBreakers  map[string]*endpointCircuitBreakerState
	streamHeaderTimeout      time.Duration // injectable response-header timeout for upstream streaming requests
	streamHeartbeatInterval  time.Duration // injectable downstream SSE heartbeat interval
}

// New creates a new Proxy instance
func New(cfg *config.Config, statsStorage StatsStorage, sqliteStorage *storage.SQLiteStorage, deviceID string) *Proxy {
	stats := NewStats(statsStorage, deviceID)

	// Create a reusable HTTP client with connection pool
	// Enhanced configuration for large SSE streaming and HTTP/2 support
	httpClient := &http.Client{
		Timeout: 300 * time.Second,
		Transport: &http.Transport{
			ForceAttemptHTTP2:      true,
			MaxIdleConns:           100,
			MaxIdleConnsPerHost:    10,
			IdleConnTimeout:        90 * time.Second,
			TLSHandshakeTimeout:    10 * time.Second,
			ExpectContinueTimeout:  1 * time.Second,
			ResponseHeaderTimeout:  90 * time.Second,
			WriteBufferSize:        128 * 1024, // 128KB write buffer for large SSE streams
			ReadBufferSize:         128 * 1024, // 128KB read buffer for large SSE streams
			MaxResponseHeaderBytes: 64 * 1024,  // 64KB max response headers
		},
	}

	return &Proxy{
		config:                  cfg,
		configEndpointsSnapshot: cloneEndpoints(cfg.GetEndpoints()),
		storage:                 sqliteStorage,
		stats:                   stats,
		currentIndex:            0,
		httpClient:              httpClient,
		activeRequests:          make(map[string]int),
		endpointCtx:             make(map[string]context.Context),
		endpointCancel:          make(map[string]context.CancelFunc),
		modelsCache:             NewModelsCache(cfg.ModelsCacheTTL),
		resolver:                NewEndpointResolverWithFunc(cfg.GetEndpoints),
		retrySleep:              time.Sleep,
		endpointCooldowns:       make(map[string]endpointCooldown),
		endpointCircuitBreakers: make(map[string]*endpointCircuitBreakerState),
	}
}

// SetOnEndpointSuccess sets the callback for successful endpoint requests
func (p *Proxy) SetOnEndpointSuccess(callback func(endpointName string)) {
	p.onEndpointSuccess = callback
}

// SetOnCurrentEndpointChanged sets the callback for default endpoint changes.
func (p *Proxy) SetOnCurrentEndpointChanged(callback func(EndpointCurrentEvent)) {
	p.onCurrentEndpointChanged = callback
}

// SetOnEndpointRuntimeChanged sets the callback for live/persisted endpoint status changes.
func (p *Proxy) SetOnEndpointRuntimeChanged(callback func(EndpointRuntimeEvent)) {
	p.onEndpointRuntimeChanged = callback
}

// Start starts the proxy server
func (p *Proxy) Start() error {
	return p.StartWithMux(nil)
}

// StartWithMux starts the proxy server with an optional custom mux
func (p *Proxy) StartWithMux(customMux *http.ServeMux) error {
	port := p.config.GetPort()

	var mux *http.ServeMux
	if customMux != nil {
		mux = customMux
	} else {
		mux = http.NewServeMux()
	}

	p.registerRoutes(mux)

	p.server = &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: mux,
	}

	logger.Info("ccNexus starting on port %d", port)
	logger.Info("Configured %d endpoints", len(p.config.GetEndpoints()))

	return p.server.ListenAndServe()
}

func (p *Proxy) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", p.handleProxy)
	mux.HandleFunc("/v1/messages/count_tokens", p.handleCountTokens)
	mux.HandleFunc("/v1/models", p.handleModels)
	mux.HandleFunc("/models", p.handleModels)
	mux.HandleFunc("/api/v1/models", p.handleModels)
	mux.HandleFunc("/api/tags", p.handleOllamaTags)
	mux.HandleFunc("/version", p.handleVersion)
	mux.HandleFunc("/props", p.handleProps)
	mux.HandleFunc("/v1/props", p.handleProps)
	mux.HandleFunc("/health", p.handleHealth)
	mux.HandleFunc("/stats", p.handleStats)
}

// Stop stops the proxy server
func (p *Proxy) Stop() error {
	if p.server != nil {
		return p.server.Close()
	}
	return nil
}

// getEnabledEndpoints returns only the enabled endpoints
func (p *Proxy) getEnabledEndpoints() []config.Endpoint {
	allEndpoints := p.config.GetEndpoints()
	enabled := make([]config.Endpoint, 0)
	for _, ep := range allEndpoints {
		if ep.Enabled {
			enabled = append(enabled, ep)
		}
	}
	return enabled
}

// getCurrentEndpoint returns the current endpoint (thread-safe)
func (p *Proxy) getCurrentEndpoint() config.Endpoint {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return p.getCurrentEndpointLocked()
}

func (p *Proxy) getCurrentEndpointLocked() config.Endpoint {
	endpoints := p.getEnabledEndpoints()
	if len(endpoints) == 0 {
		// Return empty endpoint if no enabled endpoints
		return config.Endpoint{}
	}
	// Make sure currentIndex is within bounds
	index := p.currentIndex % len(endpoints)
	return endpoints[index]
}

// markRequestActive marks an endpoint as having active requests
func (p *Proxy) markRequestActive(endpointName string) int {
	p.activeRequestsMu.Lock()
	p.activeRequests[endpointName]++
	count := p.activeRequests[endpointName]
	p.activeRequestsMu.Unlock()

	status := p.recordEndpointAttempt(endpointName)
	p.emitEndpointRuntimeEvent(endpointName, "start", status)
	return count
}

// markRequestInactive marks an endpoint as having no active requests
func (p *Proxy) markRequestInactive(endpointName string) int {
	p.activeRequestsMu.Lock()
	count := p.activeRequests[endpointName]
	if count <= 1 {
		delete(p.activeRequests, endpointName)
		count = 0
	} else {
		count--
		p.activeRequests[endpointName] = count
	}
	p.activeRequestsMu.Unlock()

	p.emitEndpointRuntimeEvent(endpointName, "end", nil)
	return count
}

// hasActiveRequests checks if an endpoint has active requests
func (p *Proxy) hasActiveRequests(endpointName string) bool {
	p.activeRequestsMu.RLock()
	defer p.activeRequestsMu.RUnlock()
	return p.activeRequests[endpointName] > 0
}

// isCurrentEndpoint checks if the given endpoint is still the current one
func (p *Proxy) isCurrentEndpoint(endpointName string) bool {
	current := p.getCurrentEndpoint()
	return current.Name == endpointName
}

// getEndpointContext returns a context for the given endpoint, creating one if needed
func (p *Proxy) getEndpointContext(endpointName string) context.Context {
	p.ctxMu.Lock()
	defer p.ctxMu.Unlock()

	if ctx, ok := p.endpointCtx[endpointName]; ok {
		return ctx
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.endpointCtx[endpointName] = ctx
	p.endpointCancel[endpointName] = cancel
	return ctx
}

// cancelEndpointRequests cancels all requests for the given endpoint
func (p *Proxy) cancelEndpointRequests(endpointName string) {
	p.ctxMu.Lock()
	defer p.ctxMu.Unlock()

	if cancel, ok := p.endpointCancel[endpointName]; ok {
		cancel()
		delete(p.endpointCtx, endpointName)
		delete(p.endpointCancel, endpointName)
	}
}

// rotateEndpoint switches to the next endpoint (thread-safe)
// waitForActive: if true, waits briefly for active requests to complete before switching
func (p *Proxy) rotateEndpoint() config.Endpoint {
	// First, check if we need to wait for active requests
	oldEndpoint := p.getCurrentEndpoint()
	if p.hasActiveRequests(oldEndpoint.Name) {
		logger.Debug("[SWITCH] Waiting for active requests on %s to complete...", oldEndpoint.Name)

		// Wait outside of the main lock to avoid blocking other operations
		for i := 0; i < 10; i++ { // Check 10 times, 50ms each = 500ms max
			time.Sleep(50 * time.Millisecond)
			if !p.hasActiveRequests(oldEndpoint.Name) {
				break
			}
		}
	}

	// Now acquire lock and perform the rotation
	p.mu.Lock()
	defer p.mu.Unlock()

	endpoints := p.getEnabledEndpoints()
	if len(endpoints) == 0 {
		return config.Endpoint{}
	}

	oldIndex := p.currentIndex % len(endpoints)
	oldEndpoint = endpoints[oldIndex]

	// Calculate next index
	p.currentIndex = (oldIndex + 1) % len(endpoints)

	newEndpoint := endpoints[p.currentIndex]
	if len(endpoints) > 1 && oldEndpoint.Name != newEndpoint.Name {
		logger.Debug("[SWITCH] %s → %s (#%d)", oldEndpoint.Name, newEndpoint.Name, p.currentIndex+1)
	}

	go p.emitCurrentEndpointChanged(oldEndpoint.Name, newEndpoint.Name, "rotation")
	return newEndpoint
}

func (p *Proxy) switchCurrentEndpointAfterFailure(failedEndpoint config.Endpoint, reason string, obs requestObservability, attemptNumber int) config.Endpoint {
	failedName := strings.TrimSpace(failedEndpoint.Name)
	if failedName == "" {
		return config.Endpoint{}
	}

	p.mu.Lock()
	endpoints := p.getEnabledEndpoints()
	if len(endpoints) <= 1 {
		p.mu.Unlock()
		return config.Endpoint{}
	}

	currentIndex := p.currentIndex % len(endpoints)
	currentEndpoint := endpoints[currentIndex]
	if currentEndpoint.Name != failedName {
		p.mu.Unlock()
		return config.Endpoint{}
	}

	requestEndpoints := p.getRequestPlanEndpoints(endpoints, obs)
	requestPlan := newRequestEndpointPlanForCurrentWithSkip(requestEndpoints, endpoints, failedName, true)
	nextEndpoint := requestPlan.Current()
	nextIndex := indexEndpointByName(endpoints, nextEndpoint.Name)
	if nextIndex < 0 || nextEndpoint.Name == "" || nextEndpoint.Name == currentEndpoint.Name {
		p.mu.Unlock()
		return config.Endpoint{}
	}

	p.currentIndex = nextIndex
	p.mu.Unlock()

	cleanReason := sanitizeLogField(reason)
	logger.Debug("[AUTO SWITCH] %s → %s (#%d) %s switch_reason=%s",
		currentEndpoint.Name,
		nextEndpoint.Name,
		nextIndex+1,
		requestLogFields(obs, currentEndpoint.Name, attemptNumber, 0, cleanReason),
		cleanReason,
	)
	p.emitCurrentEndpointChanged(currentEndpoint.Name, nextEndpoint.Name, "failure")
	return nextEndpoint
}

// GetCurrentEndpointName returns the current endpoint name (thread-safe)
func (p *Proxy) GetCurrentEndpointName() string {
	endpoint := p.getCurrentEndpoint()
	return endpoint.Name
}

// SetCurrentEndpoint manually switches to a specific endpoint by name
// Returns error if endpoint not found or not enabled
// Thread-safe. Existing in-flight requests continue on the endpoint they already selected.
func (p *Proxy) SetCurrentEndpoint(targetName string) error {
	p.mu.Lock()

	endpoints := p.getEnabledEndpoints()
	if len(endpoints) == 0 {
		p.mu.Unlock()
		return fmt.Errorf("no enabled endpoints")
	}

	// Find the endpoint by name
	for i, ep := range endpoints {
		if ep.Name == targetName {
			oldEndpoint := endpoints[p.currentIndex%len(endpoints)]
			p.currentIndex = i
			logger.Info("[MANUAL SWITCH] %s → %s", oldEndpoint.Name, ep.Name)
			p.mu.Unlock()
			p.clearEndpointCooldown(ep.Name)
			p.emitCurrentEndpointChanged(oldEndpoint.Name, ep.Name, "manual_switch")
			return nil
		}
	}

	p.mu.Unlock()
	return fmt.Errorf("endpoint '%s' not found or not enabled", targetName)
}

// ClientFormat represents the API format used by the client
type ClientFormat string

const (
	ClientFormatClaude          ClientFormat = "claude"           // Claude Code: /v1/messages
	ClientFormatOpenAIChat      ClientFormat = "openai_chat"      // Codex (chat): /v1/chat/completions
	ClientFormatOpenAIResponses ClientFormat = "openai_responses" // Codex (responses): /v1/responses
)

// detectClientFormat identifies the client format based on request path
func detectClientFormat(path string) ClientFormat {
	switch {
	case strings.HasPrefix(path, "/v1/chat/completions") || strings.HasPrefix(path, "/chat/completions"):
		return ClientFormatOpenAIChat
	case strings.HasPrefix(path, "/v1/responses") || strings.HasPrefix(path, "/responses"):
		return ClientFormatOpenAIResponses
	default:
		return ClientFormatClaude
	}
}

// handleProxy handles the main proxy logic
func (p *Proxy) handleProxy(w http.ResponseWriter, r *http.Request) {
	obs := newRequestObservability(r)
	applyRequestObservabilityHeaders(w, obs, "", 0)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("Failed to read request body: %v", err)
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	requestStart := time.Now()
	reqBytes := len(bodyBytes)

	// Detect client format
	clientFormat := detectClientFormat(r.URL.Path)

	logger.DebugLog("=== Proxy Request ===")
	logger.DebugLog("Method: %s, Path: %s, ClientFormat: %s", r.Method, r.URL.Path, clientFormat)
	logger.DebugLog("Request Body: %s", string(bodyBytes))

	if err := validateClientJSONRequestBody(bodyBytes); err != nil {
		logger.Warn(
			"Invalid request body: %v method=%s path=%s content_type=%q content_length=%d %s",
			err,
			sanitizeLogField(r.Method),
			sanitizeLogField(r.URL.Path),
			r.Header.Get("Content-Type"),
			len(bodyBytes),
			requestLogFields(obs, "", 0, http.StatusBadRequest, "invalid_request_body"),
		)
		writeInvalidRequestError(w, err)
		return
	}

	if err := validateClientRequestForFormat(bodyBytes, clientFormat); err != nil {
		logger.Warn(
			"Invalid request shape: %v method=%s path=%s content_type=%q content_length=%d %s",
			err,
			sanitizeLogField(r.Method),
			sanitizeLogField(r.URL.Path),
			r.Header.Get("Content-Type"),
			len(bodyBytes),
			requestLogFields(obs, "", 0, http.StatusBadRequest, "invalid_request_shape"),
		)
		writeInvalidRequestError(w, err)
		return
	}

	var streamReq struct {
		Model    string      `json:"model"`
		Thinking interface{} `json:"thinking"`
		Stream   bool        `json:"stream"`
	}
	json.Unmarshal(bodyBytes, &streamReq)

	// 在解析时记录原始模型名称，用于后续处理
	// originalModelName := strings.TrimSpace(streamReq.Model)

	endpoints := p.getEnabledEndpoints()
	if len(endpoints) == 0 {
		logger.Error("No enabled endpoints available")
		http.Error(w, "No enabled endpoints configured", http.StatusServiceUnavailable)
		return
	}

	// 尝试解析客户端指定的端点
	specifiedEndpoint, requestedModelSuffix, resolveErr := p.resolver.ResolveEndpoint(r, bodyBytes)
	if resolveErr != nil {
		// 端点指定错误，返回错误响应
		logger.Warn("端点解析失败: %v %s", resolveErr, requestLogFields(obs, "", 0, http.StatusBadRequest, "endpoint_resolve_failed"))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		errorResp := map[string]interface{}{
			"error": map[string]interface{}{
				"type":    "invalid_request_error",
				"message": resolveErr.Error(),
			},
		}
		if jsonBytes, err := json.Marshal(errorResp); err == nil {
			w.Write(jsonBytes)
		}
		return
	}

	// 如果指定了端点，使用该端点；否则使用轮询机制
	var useSpecificEndpoint bool
	if specifiedEndpoint != nil {
		useSpecificEndpoint = true
		logger.Debug("[Resolver] 使用指定端点: %s", specifiedEndpoint.Name)
	}

	var streamSession *downstreamStreamSession
	if streamReq.Stream {
		streamSession = newDownstreamStreamSession(w, p.streamHeartbeatIntervalOrDefault(), clientFormat)
		if err := streamSession.Start(); err != nil {
			logger.Error("Failed to start downstream stream: %v %s", err, requestLogFields(obs, "", 0, http.StatusInternalServerError, "downstream_stream_start_failed"))
			http.Error(w, "Streaming not supported", http.StatusInternalServerError)
			return
		}
		defer streamSession.Close()
	}

	requestEndpoints := endpoints
	currentEndpointName := p.GetCurrentEndpointName()
	planCurrentEndpointName := currentEndpointName
	if !useSpecificEndpoint {
		requestEndpoints = p.getRequestPlanEndpoints(endpoints, obs)
		requestEndpoints, planCurrentEndpointName = prioritizeRequestEndpointsForClientFormat(requestEndpoints, clientFormat, currentEndpointName, obs)
	}
	skipCurrentEndpoint := !useSpecificEndpoint && planCurrentEndpointName == currentEndpointName && p.isEndpointDeprioritized(currentEndpointName)
	requestPlan := newRequestEndpointPlanForCurrentWithSkip(requestEndpoints, endpoints, planCurrentEndpointName, skipCurrentEndpoint)
	maxRetries := p.computeMaxRetries(requestEndpoints)
	if useSpecificEndpoint {
		maxRetries = endpointSlowFailoverAttempts
	}
	forceStreamRetryEndpoints := make(map[string]bool)
	endpointAttempts := 0
	advanceForFailure := func(current config.Endpoint, reason string, attemptNumber int, headers http.Header) {
		p.markEndpointCooldownForReason(current.Name, reason, headers, obs, attemptNumber)
		if !useSpecificEndpoint {
			p.advanceRequestEndpoint(requestPlan, current, obs, attemptNumber, reason)
		}
		endpointAttempts = 0
	}
	refreshedCredentialAttempts := make(map[int64]bool)

	for retry := 0; retry < maxRetries; retry++ {
		var endpoint config.Endpoint
		if useSpecificEndpoint {
			// 使用指定的端点，不进行轮询
			endpoint = *specifiedEndpoint
		} else {
			// 使用请求级端点计划；失败 fallback 不修改全局 currentIndex
			endpoint = requestPlan.Current()
		}
		if !streamReq.Stream && forceStreamRetryEndpoints[endpoint.Name] {
			endpoint.ForceStream = true
		}

		if endpoint.Name == "" {
			http.Error(w, "No enabled endpoints available", http.StatusServiceUnavailable)
			return
		}

		endpointAttempts++
		attemptNumber := retry + 1
		applyRequestObservabilityHeaders(w, obs, endpoint.Name, attemptNumber)
		p.markRequestActive(endpoint.Name)

		authMode := config.NormalizeAuthMode(endpoint.AuthMode)
		apiKey := strings.TrimSpace(endpoint.APIKey)
		credentialID := int64(0)
		var selectedCredential *storage.EndpointCredential
		if config.IsTokenPoolAuthMode(authMode) {
			credential, err := p.selectCredential(endpoint.Name)
			if err != nil {
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, 0, "credential_select_failed", "Failed to select token pool credential: %v", err)
				p.recordEndpointError(endpoint.Name, "credential_select_failed")
				p.markRequestInactive(endpoint.Name)
				if endpointAttempts >= endpointFastFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name) {
					advanceForFailure(endpoint, "credential_select_failed", attemptNumber, nil)
				}
				continue
			}
			if credential == nil || strings.TrimSpace(credential.AccessToken) == "" {
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, 0, "no_usable_token", "No usable token in token pool")
				p.recordEndpointError(endpoint.Name, "no_usable_token")
				p.markRequestInactive(endpoint.Name)
				if endpointAttempts >= endpointFastFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name) {
					advanceForFailure(endpoint, "no_usable_token", attemptNumber, nil)
				}
				continue
			}
			selectedCredential = credential
			if shouldTryCredentialRefresh(credential, time.Now().UTC()) {
				refreshed, refreshErr := p.refreshCredential(endpoint, credential)
				if refreshErr != nil {
					logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, 0, "credential_refresh_failed", "Preflight credential refresh failed (id=%d): %v", credential.ID, refreshErr)
				} else {
					selectedCredential = refreshed
					refreshedCredentialAttempts[refreshed.ID] = true
				}
			}
			apiKey = strings.TrimSpace(credential.AccessToken)
			if selectedCredential != nil {
				apiKey = strings.TrimSpace(selectedCredential.AccessToken)
				credentialID = selectedCredential.ID
			}
		} else if apiKey == "" {
			logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, 0, "empty_api_key", "API key mode but apiKey is empty")
			p.recordEndpointError(endpoint.Name, "empty_api_key")
			p.markRequestInactive(endpoint.Name)
			if endpointAttempts >= endpointFastFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name) {
				advanceForFailure(endpoint, "empty_api_key", attemptNumber, nil)
			}
			continue
		}

		trans, err := prepareTransformerForClient(clientFormat, endpoint)
		if err != nil {
			logRequestAttemptError(obs, endpoint.Name, attemptNumber, 0, "prepare_transformer_failed", "%v", err)
			p.recordEndpointError(endpoint.Name, "prepare_transformer_failed")
			p.markRequestInactive(endpoint.Name)
			if endpointAttempts >= endpointFastFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name) {
				advanceForFailure(endpoint, "prepare_transformer_failed", attemptNumber, nil)
			}
			continue
		}

		transformerName := trans.Name()

		transformedBody, err := trans.TransformRequest(bodyBytes)
		if err != nil {
			logRequestAttemptError(obs, endpoint.Name, attemptNumber, 0, "transform_request_failed", "Failed to transform request: %v", err)
			p.recordEndpointError(endpoint.Name, "transform_request_failed")
			p.markRequestInactive(endpoint.Name)
			if endpointAttempts >= endpointFastFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name) {
				advanceForFailure(endpoint, "transform_request_failed", attemptNumber, nil)
			}
			continue
		}

		logger.DebugLog("[%s] Transformer: %s", endpoint.Name, transformerName)
		logger.DebugLog("[%s] Transformed Request: %s", endpoint.Name, string(transformedBody))

		cleanedBody, err := cleanIncompleteToolCalls(transformedBody)
		if err != nil {
			logger.Warn("[%s] Failed to clean tool calls: %v", endpoint.Name, err)
			cleanedBody = transformedBody
		}
		transformedBody = cleanedBody
		if !streamReq.Stream && endpoint.ForceStream {
			transformedBody = forceStreamInPayload(transformedBody)
			logger.DebugLog("[%s] ForceStream enabled: forcing upstream stream=true for non-stream client", endpoint.Name)
		}
		if clientFormat != ClientFormatClaude {
			transformedBody = injectEndpointThinkingInPayload(transformedBody, transformerName, endpoint.Thinking)
		}
		transformedBody = enforceEndpointModelInPayload(transformedBody, endpoint, transformerName)
		logger.DebugLog("[%s] Final upstream request: %s", endpoint.Name, string(transformedBody))

		clientModelName := strings.TrimSpace(streamReq.Model)
		upstreamModelName := extractModelFromPayload(transformedBody)
		modelName := upstreamModelName
		if modelName == "" {
			modelName = clientModelName
		}
		if modelName == "" {
			modelName = endpoint.Model
		}
		if requestedModelSuffix != "" {
			logger.Debug("[%s] Ignoring model suffix from endpoint selector due endpoint model priority: %s", endpoint.Name, requestedModelSuffix)
		}
		if clientModelName != "" && upstreamModelName != "" && clientModelName != upstreamModelName {
			logger.Debug("[%s] Model mapping: client_model=%s upstream_model=%s", endpoint.Name, clientModelName, upstreamModelName)
		}

		var thinkingEnabled bool
		if strings.Contains(transformerName, "openai") {
			var openaiReq map[string]interface{}
			if err := json.Unmarshal(transformedBody, &openaiReq); err == nil {
				if enable, ok := openaiReq["enable_thinking"].(bool); ok {
					thinkingEnabled = enable
				}
			}
		}

		proxyReq, err := buildProxyRequest(r, endpoint, apiKey, transformedBody, transformerName, selectedCredential)
		if err != nil {
			logRequestAttemptError(obs, endpoint.Name, attemptNumber, 0, "build_request_failed", "Failed to create request: %v", err)
			p.recordEndpointError(endpoint.Name, "build_request_failed")
			p.markRequestInactive(endpoint.Name)
			if endpointAttempts >= endpointFastFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name) {
				advanceForFailure(endpoint, "build_request_failed", attemptNumber, nil)
			}
			continue
		}

		proxyURL := resolveProxyURLForRequest(p.config, proxyReq.URL)
		proxyLabel := strings.TrimSpace(proxyURL)
		action := "Requesting"
		if streamReq.Stream {
			action = "Streaming"
		}
		logRequestAttemptStart(obs, endpoint.Name, action, modelName, reqBytes, attemptNumber, proxyLabel)

		ctx := r.Context()
		upstreamStreaming := streamReq.Stream || shouldAggregateStreamingAsNonStreaming(endpoint, transformerName)
		responseHeaderTimeout := time.Duration(0)
		if upstreamStreaming {
			responseHeaderTimeout = p.streamHeaderTimeoutOrDefault()
		}
		resp, err := sendRequestWithResponseHeaderTimeout(ctx, proxyReq, p.httpClient, p.config, responseHeaderTimeout, !upstreamStreaming)
		if err != nil {
			if isClientCanceled(ctx, err) {
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, 0, "client_canceled", "Client canceled request: %v", err)
				p.markRequestInactive(endpoint.Name)
				return
			}
			retryReason := retryReasonForRequestError(err)
			logRequestAttemptError(obs, endpoint.Name, attemptNumber, 0, retryReason, "Request failed: %v", err)
			if isRetryableRequestErrorReason(retryReason) {
				status := p.recordEndpointFailure(endpoint.Name, retryReason)
				p.emitEndpointRuntimeEvent(endpoint.Name, "failure", status)
				p.markRequestInactive(endpoint.Name)
				if endpointAttempts >= endpointFastFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name) {
					advanceForFailure(endpoint, retryReason, attemptNumber, nil)
				} else {
					logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, 0, retryReason, "Retryable transport error, retrying same endpoint: %v", err)
					p.sleepBeforeRetry(300 * time.Millisecond)
				}
				continue
			}
			p.markCredentialFailure(credentialID, 0, err.Error())
			p.recordCredentialUsage(credentialID, endpoint.Name, 0, 1, 0, 0)
			p.recordEndpointError(endpoint.Name, retryReason)
			p.markRequestInactive(endpoint.Name)
			if endpointAttempts >= endpointFastFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name) {
				advanceForFailure(endpoint, retryReason, attemptNumber, nil)
			}
			continue
		}
		if isClientCanceled(ctx, nil) {
			logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, 0, "client_canceled", "Client canceled request after upstream response")
			if resp.Body != nil {
				resp.Body.Close()
			}
			p.markRequestInactive(endpoint.Name)
			return
		}

		if resp.StatusCode == http.StatusOK {
			p.captureCodexRateLimitsFromHeaders(endpoint, credentialID, resp.Header)
		}

		contentType := resp.Header.Get("Content-Type")
		isStreaming := shouldHandleAsStreamingResponse(contentType, streamReq.Stream, endpoint, transformerName)

		// Codex backend and force-stream endpoints may require stream=true upstream.
		// Bridge to non-stream client responses regardless of upstream Content-Type quirks.
		if resp.StatusCode == http.StatusOK && !streamReq.Stream && shouldAggregateStreamingAsNonStreaming(endpoint, transformerName) {
			inputTokens, outputTokens, outputText, err := p.handleStreamingAsNonStreaming(w, resp, endpoint, trans, credentialID)
			if err == nil {
				// Fallback: estimate tokens when usage is missing.
				if inputTokens == 0 || outputTokens == 0 {
					inputTokens, outputTokens = p.estimateTokens(bodyBytes, outputText, inputTokens, outputTokens, endpoint.Name)
				}

				p.stats.RecordRequest(endpoint.Name)
				p.stats.RecordTokens(endpoint.Name, inputTokens, outputTokens)
				p.recordCredentialUsage(credentialID, endpoint.Name, 1, 0, inputTokens, outputTokens)
				p.markCredentialSuccess(credentialID)
				p.clearEndpointCooldown(endpoint.Name)
				p.markRequestInactive(endpoint.Name)
				p.recordEndpointSuccessEvent(endpoint.Name)
				totalElapsed := time.Since(requestStart).Round(time.Millisecond)
				logRequestAttemptResult(obs, endpoint.Name, attemptNumber, http.StatusOK, "", "Requested tokens=%d/%d latency=%s cred_id=%d", inputTokens, outputTokens, totalElapsed, credentialID)
				return
			}
			if semanticErr, ok := asSemanticEmptyResponseError(err); ok {
				logRequestAttemptWarn(
					obs,
					endpoint.Name,
					attemptNumber,
					http.StatusOK,
					retryReasonSemanticEmptyResponse,
					"Semantic empty response from upstream; retrying empty_kind=%s cred_id=%d output_tokens=%d outputTextLen=%d",
					semanticErr.Kind,
					credentialID,
					semanticErr.OutputTokens,
					semanticErr.OutputTextLen,
				)
				semanticEmptyExhausted := endpointAttempts >= endpointSlowFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name)
				p.recordSemanticEmptyResponseFailure(endpoint.Name, credentialID, semanticErr, semanticEmptyExhausted)
				p.markRequestInactive(endpoint.Name)
				if semanticEmptyExhausted {
					advanceForFailure(endpoint, retryReasonSemanticEmptyResponse, attemptNumber, nil)
				}
				continue
			}
			if isClientCanceled(ctx, err) {
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, http.StatusOK, "client_canceled", "Client canceled while aggregating streaming response as non-stream: %v", err)
				p.markRequestInactive(endpoint.Name)
				return
			}
			logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, http.StatusOK, "aggregate_streaming_failed", "Failed to aggregate streaming response as non-stream: %v", err)
			p.markCredentialFailure(credentialID, 0, err.Error())
			p.recordCredentialUsage(credentialID, endpoint.Name, 0, 1, 0, 0)
			p.recordEndpointError(endpoint.Name, "aggregate_streaming_failed")
			p.markRequestInactive(endpoint.Name)
			if endpointAttempts >= endpointFastFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name) {
				advanceForFailure(endpoint, "aggregate_streaming_failed", attemptNumber, nil)
			}
			continue
		}

		if resp.StatusCode == http.StatusOK && isStreaming {
			streamResult := p.handleStreamingResponse(ctx, w, resp, endpoint, trans, transformerName, thinkingEnabled, modelName, bodyBytes, credentialID, streamSession)
			inputTokens := streamResult.InputTokens
			outputTokens := streamResult.OutputTokens
			outputText := streamResult.OutputText

			// Fallback: estimate tokens when usage is 0
			if inputTokens == 0 || outputTokens == 0 {
				inputTokens, outputTokens = p.estimateTokens(bodyBytes, outputText, inputTokens, outputTokens, endpoint.Name)
			}
			if streamResult.Err != nil {
				retryReason := streamResult.Reason
				if retryReason == "" {
					retryReason = "streaming_failed"
				}
				if retryReason == streamFinishClientCanceled || isClientCanceled(ctx, streamResult.Err) {
					logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, http.StatusOK, "client_canceled", "Client canceled streaming response: %v", streamResult.Err)
					p.markRequestInactive(endpoint.Name)
					return
				}
				if semanticErr, ok := asSemanticEmptyResponseError(streamResult.Err); ok {
					logRequestAttemptWarn(
						obs,
						endpoint.Name,
						attemptNumber,
						http.StatusOK,
						retryReasonSemanticEmptyResponse,
						"Semantic empty streaming response from upstream; retrying empty_kind=%s cred_id=%d output_tokens=%d outputTextLen=%d wrote_data=%t wrote_semantic_data=%t",
						semanticErr.Kind,
						credentialID,
						semanticErr.OutputTokens,
						semanticErr.OutputTextLen,
						streamResult.WroteData,
						streamResult.WroteSemanticData,
					)
					semanticEmptyExhausted := endpointAttempts >= endpointSlowFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name)
					p.recordSemanticEmptyResponseFailure(endpoint.Name, credentialID, semanticErr, semanticEmptyExhausted)
					p.markRequestInactive(endpoint.Name)
					if streamResult.WroteSemanticData {
						return
					}
					// Reasoning/progress already streamed to the client; cannot silently
					// retry on another endpoint, so close the open stream with an error.
					if streamResult.WroteData {
						if streamSession != nil && streamSession.Started() {
							_ = streamSession.WriteTypedError(retryReasonSemanticEmptyResponse, "Upstream returned an empty response after reasoning")
						}
						return
					}
					if semanticEmptyExhausted {
						advanceForFailure(endpoint, retryReasonSemanticEmptyResponse, attemptNumber, nil)
					}
					continue
				}
				if retryReason == streamFinishUpstreamStreamError {
					if streamResult.WroteSemanticData {
						logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, http.StatusOK, retryReason, "Streaming response failed after semantic output: %v", streamResult.Err)
						if streamSession != nil && streamSession.Started() {
							message := fmt.Sprintf("Upstream stream interrupted: %v", streamResult.Err)
							if writeErr := streamSession.WriteTypedError(streamFinishUpstreamStreamError, message); writeErr != nil {
								logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, http.StatusOK, streamFinishDownstreamWriteFailed, "Failed to write upstream stream error to downstream: %v", writeErr)
							}
							streamSession.Close()
						}
						p.markCredentialFailure(credentialID, 0, streamResult.Err.Error())
						p.recordCredentialUsage(credentialID, endpoint.Name, 0, 1, 0, 0)
						p.recordEndpointError(endpoint.Name, retryReason)
						p.markRequestInactive(endpoint.Name)
						return
					}

					logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, http.StatusOK, retryReason, "Streaming response failed before semantic output: %v", streamResult.Err)
					p.markCredentialFailure(credentialID, 0, streamResult.Err.Error())
					p.recordCredentialUsage(credentialID, endpoint.Name, 0, 1, 0, 0)
					p.recordEndpointError(endpoint.Name, retryReason)
					p.markRequestInactive(endpoint.Name)
					if endpointAttempts >= endpointFastFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name) {
						p.markEndpointCooldownForReason(endpoint.Name, retryReason, resp.Header, obs, attemptNumber)
						if useSpecificEndpoint {
							if streamSession != nil && streamSession.Started() {
								message := fmt.Sprintf("Upstream stream interrupted before output: %v", streamResult.Err)
								_ = streamSession.WriteTypedError(streamFinishUpstreamStreamError, message)
							}
							return
						}
						p.advanceRequestEndpoint(requestPlan, endpoint, obs, attemptNumber, retryReason)
						endpointAttempts = 0
					} else {
						logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, http.StatusOK, retryReason, "Retryable streaming response failure before semantic output, retrying same endpoint: %v", streamResult.Err)
						p.sleepBeforeRetry(300 * time.Millisecond)
					}
					continue
				}

				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, http.StatusOK, retryReason, "Streaming response failed: %v", streamResult.Err)
				p.markCredentialFailure(credentialID, 0, streamResult.Err.Error())
				p.recordCredentialUsage(credentialID, endpoint.Name, 0, 1, 0, 0)
				p.recordEndpointError(endpoint.Name, retryReason)
				p.markEndpointCooldownForReason(endpoint.Name, retryReason, resp.Header, obs, attemptNumber)
				p.markRequestInactive(endpoint.Name)
				p.switchCurrentEndpointAfterFailure(endpoint, retryReason, obs, attemptNumber)
				return
			}

			p.stats.RecordRequest(endpoint.Name)
			p.stats.RecordTokens(endpoint.Name, inputTokens, outputTokens)
			p.recordCredentialUsage(credentialID, endpoint.Name, 1, 0, inputTokens, outputTokens)
			p.markCredentialSuccess(credentialID)
			p.clearEndpointCooldown(endpoint.Name)
			p.markRequestInactive(endpoint.Name)
			p.recordEndpointSuccessEvent(endpoint.Name)
			totalElapsed := time.Since(requestStart).Round(time.Millisecond)
			logRequestAttemptResult(obs, endpoint.Name, attemptNumber, http.StatusOK, "", "Requested tokens=%d/%d latency=%s cred_id=%d", inputTokens, outputTokens, totalElapsed, credentialID)
			return
		}

		if resp.StatusCode == http.StatusOK {
			inputTokens, outputTokens, err := p.handleNonStreamingResponse(w, resp, endpoint, trans)
			if err == nil {
				p.stats.RecordRequest(endpoint.Name)
				p.stats.RecordTokens(endpoint.Name, inputTokens, outputTokens)
				p.recordCredentialUsage(credentialID, endpoint.Name, 1, 0, inputTokens, outputTokens)
				p.markCredentialSuccess(credentialID)
				p.clearEndpointCooldown(endpoint.Name)
				p.markRequestInactive(endpoint.Name)
				p.recordEndpointSuccessEvent(endpoint.Name)
				totalElapsed := time.Since(requestStart).Round(time.Millisecond)
				logRequestAttemptResult(obs, endpoint.Name, attemptNumber, http.StatusOK, "", "Requested tokens=%d/%d latency=%s cred_id=%d", inputTokens, outputTokens, totalElapsed, credentialID)
				return
			}
			if isClientCanceled(ctx, err) {
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, http.StatusOK, "client_canceled", "Client canceled non-streaming response: %v", err)
				p.markRequestInactive(endpoint.Name)
				return
			}
			if semanticErr, ok := asSemanticEmptyResponseError(err); ok {
				logRequestAttemptWarn(
					obs,
					endpoint.Name,
					attemptNumber,
					http.StatusOK,
					retryReasonSemanticEmptyResponse,
					"Semantic empty non-streaming response from upstream; retrying empty_kind=%s cred_id=%d output_tokens=%d outputTextLen=%d",
					semanticErr.Kind,
					credentialID,
					semanticErr.OutputTokens,
					semanticErr.OutputTextLen,
				)
				semanticEmptyExhausted := endpointAttempts >= endpointSlowFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name)
				p.recordSemanticEmptyResponseFailure(endpoint.Name, credentialID, semanticErr, semanticEmptyExhausted)
				p.markRequestInactive(endpoint.Name)
				if semanticEmptyExhausted {
					advanceForFailure(endpoint, retryReasonSemanticEmptyResponse, attemptNumber, nil)
				}
				continue
			}
			logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, http.StatusOK, "non_stream_response_failed", "Failed to handle non-streaming response: %v", err)
			p.markCredentialFailure(credentialID, 0, err.Error())
			p.recordCredentialUsage(credentialID, endpoint.Name, 0, 1, 0, 0)
			p.recordEndpointError(endpoint.Name, "non_stream_response_failed")
			p.markRequestInactive(endpoint.Name)
			if endpointAttempts >= endpointFastFailoverAttempts || p.isEndpointInActiveCooldown(endpoint.Name) {
				advanceForFailure(endpoint, "non_stream_response_failed", attemptNumber, nil)
			}
			continue
		}

		if shouldRetry(resp.StatusCode) {
			var errBody []byte
			if resp.Header.Get("Content-Encoding") == "gzip" {
				errBody, _ = decompressGzip(resp.Body)
			} else {
				errBody, _ = io.ReadAll(resp.Body)
			}
			resp.Body.Close()
			errMsg := string(errBody)
			logMsg := errMsg
			if len(logMsg) > 200 {
				logMsg = logMsg[:200] + "..."
			}
			if shouldRetryWithForcedStream(resp.StatusCode, errMsg, streamReq.Stream, transformerName) &&
				!forceStreamRetryEndpoints[endpoint.Name] {
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, "force_stream_required", "Upstream requires stream=true, retrying same endpoint with forced streaming: %s", logMsg)
				p.markRequestInactive(endpoint.Name)
				forceStreamRetryEndpoints[endpoint.Name] = true
				endpointAttempts = 0
				continue
			}
			if isUpstreamInvalidRequestHTTPFailure(resp.StatusCode, errMsg) {
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, "upstream_invalid_request", "Upstream returned invalid request error, not retrying endpoint: %s", logMsg)
				p.markRequestInactive(endpoint.Name)
				if streamSession != nil && streamSession.Started() {
					_ = streamSession.WriteError(fmt.Sprintf("Upstream returned invalid request: %s", logMsg))
					return
				}
				for key, values := range resp.Header {
					if key == "Content-Encoding" || key == "Content-Length" {
						continue
					}
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				w.Write(errBody)
				return
			}
			if shouldTreatAPIKeyEndpointAuthFailure(authMode, resp.StatusCode, errMsg) {
				p.recordEndpointError(endpoint.Name, retryReasonEndpointAuthFailed, resp.StatusCode)
				if !useSpecificEndpoint && requestPlan.Len() > 1 {
					logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, retryReasonEndpointAuthFailed, "API key auth failed, trying next endpoint: %s", logMsg)
					p.markRequestInactive(endpoint.Name)
					advanceForFailure(endpoint, retryReasonEndpointAuthFailed, attemptNumber, resp.Header)
					continue
				}
				p.markEndpointCooldownForReason(endpoint.Name, retryReasonEndpointAuthFailed, resp.Header, obs, attemptNumber)
				p.markRequestInactive(endpoint.Name)
				if useSpecificEndpoint {
					logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, retryReasonEndpointAuthFailed, "API key auth failed on specified endpoint, not failing over: %s", logMsg)
				} else {
					logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, retryReasonEndpointAuthFailed, "API key auth failed and no alternate endpoint is available: %s", logMsg)
				}
				if streamSession != nil && streamSession.Started() {
					_ = streamSession.WriteError(fmt.Sprintf("Upstream returned status %d: %s", resp.StatusCode, logMsg))
					return
				}
				for key, values := range resp.Header {
					if key == "Content-Encoding" || key == "Content-Length" {
						continue
					}
					for _, value := range values {
						w.Header().Add(key, value)
					}
				}
				w.WriteHeader(resp.StatusCode)
				w.Write(errBody)
				return
			}
			retryReason := retryReasonForHTTPStatus(resp.StatusCode, errMsg)
			logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, retryReason, "Request failed %d: %s", resp.StatusCode, logMsg)
			logger.DebugLog("[%s] Request failed %d: %s", endpoint.Name, resp.StatusCode, logMsg)
			p.markCredentialFailure(credentialID, resp.StatusCode, errMsg)
			p.recordCredentialUsage(credentialID, endpoint.Name, 0, 1, 0, 0)
			p.recordEndpointError(endpoint.Name, retryReason, resp.StatusCode)
			p.markRequestInactive(endpoint.Name)
			shouldFailover := shouldRotateEndpointAfterHTTPFailure(endpointAttempts, resp.StatusCode, errMsg) || p.isEndpointInActiveCooldown(endpoint.Name)
			if retryReason == "rate_limited" && !shouldFailover {
				backoff := rateLimitBackoffDuration(endpointAttempts, resp.Header)
				logger.Debug("[%s] Backing off before retry: %s %s retry_reason=%s", endpoint.Name, backoff, requestLogFields(obs, endpoint.Name, attemptNumber, resp.StatusCode, retryReason), retryReason)
				p.sleepBeforeRetry(backoff)
			}
			if shouldFailover {
				advanceForFailure(endpoint, retryReason, attemptNumber, resp.Header)
			}
			continue
		}

		var respBody []byte
		if resp.Header.Get("Content-Encoding") == "gzip" {
			respBody, _ = decompressGzip(resp.Body)
		} else {
			respBody, _ = io.ReadAll(resp.Body)
		}
		resp.Body.Close()
		skipCredentialPenalty := false
		respMsg := string(respBody)
		respLogMsg := respMsg
		if len(respLogMsg) > 500 {
			respLogMsg = respLogMsg[:500] + "..."
		}
		endpointAuthFailureHandled := false

		if shouldRetryWithForcedStream(resp.StatusCode, respMsg, streamReq.Stream, transformerName) &&
			!forceStreamRetryEndpoints[endpoint.Name] {
			logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, "force_stream_required", "Upstream requires stream=true, retrying same endpoint with forced streaming: %s", respLogMsg)
			p.markRequestInactive(endpoint.Name)
			forceStreamRetryEndpoints[endpoint.Name] = true
			endpointAttempts = 0
			continue
		}

		// Token pool mode: on 401/403, invalidate current credential and retry within the same endpoint.
		if (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden) && credentialID > 0 {
			if !shouldTreatCredentialAuthFailure(resp.StatusCode, respLogMsg) {
				skipCredentialPenalty = true
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, "route_gateway_denial", "Upstream %d looks like route/gateway denial, skipping credential invalidation", resp.StatusCode)
			}
			if skipCredentialPenalty {
				p.recordEndpointError(endpoint.Name, "route_gateway_denial", resp.StatusCode)
				p.markRequestInactive(endpoint.Name)
			} else {
				if selectedCredential != nil &&
					isCodexProviderType(selectedCredential.ProviderType) &&
					strings.TrimSpace(selectedCredential.RefreshToken) != "" &&
					!refreshedCredentialAttempts[credentialID] {
					refreshedCredentialAttempts[credentialID] = true
					refreshed, refreshErr := p.refreshCredential(endpoint, selectedCredential)
					if refreshErr == nil {
						logger.Info("[%s] Credential refreshed after %d, retrying with updated token (id=%d) %s", endpoint.Name, resp.StatusCode, credentialID, requestLogFields(obs, endpoint.Name, attemptNumber, resp.StatusCode, "credential_refreshed"))
						p.markRequestInactive(endpoint.Name)
						endpointAttempts = 0
						if refreshed != nil && refreshed.ID > 0 {
							refreshedCredentialAttempts[refreshed.ID] = true
						}
						continue
					}
					logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, "credential_refresh_failed", "Credential refresh failed after %d (id=%d): %v", resp.StatusCode, credentialID, refreshErr)
				}
				p.markCredentialFailure(credentialID, resp.StatusCode, respLogMsg)
				p.recordCredentialUsage(credentialID, endpoint.Name, 0, 1, 0, 0)
				p.recordEndpointError(endpoint.Name, "credential_auth_failed", resp.StatusCode)
				p.markRequestInactive(endpoint.Name)
				endpointAttempts = 0
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, "credential_auth_failed", "Credential auth failed (%d), retrying with next token", resp.StatusCode)
				continue
			}
		}

		if shouldTreatAPIKeyEndpointAuthFailure(authMode, resp.StatusCode, respMsg) {
			endpointAuthFailureHandled = true
			p.recordEndpointError(endpoint.Name, retryReasonEndpointAuthFailed, resp.StatusCode)
			if !useSpecificEndpoint && requestPlan.Len() > 1 {
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, retryReasonEndpointAuthFailed, "API key auth failed, trying next endpoint: %s", respLogMsg)
				p.markRequestInactive(endpoint.Name)
				advanceForFailure(endpoint, retryReasonEndpointAuthFailed, attemptNumber, resp.Header)
				continue
			}
			p.markEndpointCooldownForReason(endpoint.Name, retryReasonEndpointAuthFailed, resp.Header, obs, attemptNumber)
			if useSpecificEndpoint {
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, retryReasonEndpointAuthFailed, "API key auth failed on specified endpoint, not failing over: %s", respLogMsg)
			} else {
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, retryReasonEndpointAuthFailed, "API key auth failed and no alternate endpoint is available: %s", respLogMsg)
			}
		}

		p.markRequestInactive(endpoint.Name)
		// Log non-200 responses for debugging
		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode == http.StatusBadRequest &&
				strings.Contains(respLogMsg, "api.responses.write") &&
				strings.Contains(transformerName, "openai2") {
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, "responses_scope_rejected", "Upstream rejected /v1/responses scope (api.responses.write). Try transformer=openai (chat/completions) for this token.")
			}
			if skipCredentialPenalty {
				p.markCredentialFailure(credentialID, 0, respLogMsg)
				p.recordCredentialUsage(credentialID, endpoint.Name, 0, 1, 0, 0)
			} else {
				p.markCredentialFailure(credentialID, resp.StatusCode, respLogMsg)
				p.recordCredentialUsage(credentialID, endpoint.Name, 0, 1, 0, 0)
			}
			if !skipCredentialPenalty && !endpointAuthFailureHandled {
				p.recordEndpointError(endpoint.Name, "non_retryable_status", resp.StatusCode)
			}
			if !endpointAuthFailureHandled {
				logRequestAttemptWarn(obs, endpoint.Name, attemptNumber, resp.StatusCode, "non_retryable_status", "Response %d: %s", resp.StatusCode, respLogMsg)
			}
			logger.DebugLog("[%s] Response %d: %s", endpoint.Name, resp.StatusCode, respLogMsg)
		}
		if streamSession != nil && streamSession.Started() {
			_ = streamSession.WriteError(fmt.Sprintf("Upstream returned status %d: %s", resp.StatusCode, respLogMsg))
			return
		}
		// Remove Content-Encoding header since we've decompressed
		for key, values := range resp.Header {
			if key == "Content-Encoding" || key == "Content-Length" {
				continue
			}
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
		w.WriteHeader(resp.StatusCode)
		w.Write(respBody)
		return
	}

	if streamSession != nil && streamSession.Started() {
		_ = streamSession.WriteError("All endpoints failed")
		return
	}

	http.Error(w, "All endpoints failed", http.StatusServiceUnavailable)
}

func (p *Proxy) selectCredential(endpointName string) (*storage.EndpointCredential, error) {
	if p.storage == nil {
		return nil, nil
	}
	return p.storage.GetUsableEndpointCredential(endpointName, time.Now().UTC())
}

func (p *Proxy) markCredentialSuccess(credentialID int64) {
	if credentialID <= 0 || p.storage == nil {
		return
	}
	if err := p.storage.MarkCredentialSuccess(credentialID, time.Now().UTC()); err != nil {
		logger.Warn("Failed to mark credential success (id=%d): %v", credentialID, err)
	}
}

func (p *Proxy) recordCredentialUsage(credentialID int64, endpointName string, requests, errors, inputTokens, outputTokens int) {
	if credentialID <= 0 || p.storage == nil {
		return
	}
	if err := p.storage.UpsertCredentialUsage(credentialID, endpointName, requests, errors, inputTokens, outputTokens, time.Now().UTC()); err != nil {
		logger.Warn("Failed to record credential usage (id=%d): %v", credentialID, err)
	}
}

func (p *Proxy) markCredentialFailure(credentialID int64, statusCode int, errMsg string) {
	if credentialID <= 0 || p.storage == nil {
		return
	}
	if err := p.storage.MarkCredentialFailure(credentialID, statusCode, errMsg, time.Now().UTC()); err != nil {
		logger.Warn("Failed to mark credential failure (id=%d): %v", credentialID, err)
	}
}

func (p *Proxy) markCredentialCooldown(credentialID int64, duration time.Duration, errMsg string) {
	if credentialID <= 0 || p.storage == nil {
		return
	}
	if err := p.storage.MarkCredentialCooldown(credentialID, duration, errMsg, time.Now().UTC()); err != nil {
		logger.Warn("Failed to mark credential cooldown (id=%d): %v", credentialID, err)
	}
}

func (p *Proxy) semanticEmptyCredentialCooldown() time.Duration {
	duration := p.cooldownDurationForReason(retryReasonSemanticEmptyResponse, nil)
	if duration <= 0 {
		duration = 60 * time.Second
	}
	return duration
}

func (p *Proxy) recordSemanticEmptyResponseFailure(endpointName string, credentialID int64, err *semanticEmptyResponseError, recordEndpointFailure bool) {
	if err == nil {
		err = newSemanticEmptyResponseError("", 0, 0)
	}
	if credentialID > 0 {
		p.markCredentialCooldown(credentialID, p.semanticEmptyCredentialCooldown(), err.Error())
	}
	p.recordCredentialUsage(credentialID, endpointName, 0, 1, 0, 0)
	if recordEndpointFailure {
		p.recordEndpointError(endpointName, retryReasonSemanticEmptyResponse)
	}
}

func (p *Proxy) computeMaxRetries(endpoints []config.Endpoint) int {
	baseRetries := len(endpoints) * endpointSlowFailoverAttempts
	if p.storage == nil || len(endpoints) == 0 {
		return baseRetries
	}

	extraRetries := 0
	for _, endpoint := range endpoints {
		if !config.IsTokenPoolAuthMode(endpoint.AuthMode) {
			continue
		}

		stats, err := p.storage.GetTokenPoolStats(endpoint.Name)
		if err != nil {
			logger.Warn("[%s] Failed to load token pool stats: %v", endpoint.Name, err)
			continue
		}

		usable := stats.Active + stats.Expiring + stats.NeedRefresh
		if usable > 1 {
			extraRetries += usable - 1
		}
	}

	maxRetries := baseRetries + extraRetries
	if maxRetries < baseRetries {
		return baseRetries
	}
	return maxRetries
}

func shouldAggregateCodexStreaming(endpoint config.Endpoint, transformerName string) bool {
	if !strings.Contains(transformerName, "openai2") {
		return false
	}
	url := strings.ToLower(strings.TrimSpace(endpoint.APIUrl))
	return strings.Contains(url, "chatgpt.com/backend-api/codex")
}

func shouldAggregateStreamingAsNonStreaming(endpoint config.Endpoint, transformerName string) bool {
	if shouldAggregateCodexStreaming(endpoint, transformerName) {
		return true
	}
	if !endpoint.ForceStream {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(transformerName))
	return strings.Contains(name, "openai")
}

// shouldHandleAsStreamingResponse determines if an upstream 200 response should be
// processed as SSE. Some Codex upstreams intermittently omit Content-Type even when
// stream=true and body is SSE.
func shouldHandleAsStreamingResponse(contentType string, clientRequestedStream bool, endpoint config.Endpoint, transformerName string) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(contentType)), "text/event-stream") {
		return true
	}
	if !clientRequestedStream {
		return false
	}
	// Codex /responses may return SSE with an empty content-type header.
	if shouldAggregateCodexStreaming(endpoint, transformerName) {
		return true
	}
	return false
}

func shouldTreatCredentialAuthFailure(statusCode int, body string) bool {
	if statusCode == http.StatusUnauthorized {
		return true
	}
	if statusCode != http.StatusForbidden {
		return false
	}

	lower := strings.ToLower(strings.TrimSpace(body))
	if strings.HasPrefix(lower, "<!doctype html") ||
		strings.HasPrefix(lower, "<html") ||
		strings.Contains(lower, "<head>") ||
		strings.Contains(lower, "<body") {
		return false
	}
	return true
}

func isTransientNetworkError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	if strings.Contains(message, "eof") {
		return true
	}
	if strings.Contains(message, "timeout awaiting response headers") {
		return true
	}
	if strings.Contains(message, "i/o timeout") {
		return true
	}
	if strings.Contains(message, "connection reset by peer") {
		return true
	}
	return false
}
