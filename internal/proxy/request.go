package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/net/proxy"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
	"github.com/lich0821/ccNexus/internal/providercompat"
	"github.com/lich0821/ccNexus/internal/storage"
	"github.com/lich0821/ccNexus/internal/transformer"
	"github.com/lich0821/ccNexus/internal/transformer/cc"
	"github.com/lich0821/ccNexus/internal/transformer/convert"
	"github.com/lich0821/ccNexus/internal/transformer/cx/chat"
	"github.com/lich0821/ccNexus/internal/transformer/cx/responses"
)

const (
	codexClientVersion = "0.101.0"
	codexUserAgent     = "codex_cli_rs/0.101.0 (Mac OS 26.0.1; arm64) Apple_Terminal/464"
)

// prepareTransformerForClient creates transformer based on client format and endpoint
func prepareTransformerForClient(clientFormat ClientFormat, endpoint config.Endpoint) (transformer.Transformer, error) {
	endpointTransformer := endpoint.Transformer
	if endpointTransformer == "" {
		endpointTransformer = "claude"
	}

	switch clientFormat {
	case ClientFormatClaude:
		return prepareCCTransformer(endpoint, endpointTransformer)
	case ClientFormatOpenAIChat:
		return prepareCxChatTransformer(endpoint, endpointTransformer)
	case ClientFormatOpenAIResponses:
		return prepareCxRespTransformer(endpoint, endpointTransformer)
	}

	return nil, fmt.Errorf("unsupported client format: %s", clientFormat)
}

// prepareCCTransformer creates transformer for Claude Code client
func prepareCCTransformer(endpoint config.Endpoint, endpointTransformer string) (transformer.Transformer, error) {
	switch providercompat.NormalizeTransformer(endpointTransformer) {
	case "claude":
		if endpoint.Model != "" {
			logger.Debug("[%s] Using cc_claude with model override: %s", endpoint.Name, endpoint.Model)
			return cc.NewClaudeTransformerWithModel(endpoint.Model), nil
		}
		return cc.NewClaudeTransformer(), nil
	case "openai", "deepseek", "kimi", "poe":
		if endpoint.Model == "" {
			return nil, fmt.Errorf("OpenAI transformer requires model field")
		}
		return cc.NewOpenAITransformerWithThinking(endpoint.Model, endpoint.Thinking), nil
	case "openai2":
		if endpoint.Model == "" {
			return nil, fmt.Errorf("OpenAI2 transformer requires model field")
		}
		return cc.NewOpenAI2TransformerWithThinking(endpoint.Model, endpoint.Thinking), nil
	case "gemini":
		if endpoint.Model == "" {
			return nil, fmt.Errorf("Gemini transformer requires model field")
		}
		return cc.NewGeminiTransformer(endpoint.Model), nil
	default:
		return nil, fmt.Errorf("unsupported endpoint transformer: %s", endpointTransformer)
	}
}

// prepareCxChatTransformer creates transformer for Codex Chat API client
func prepareCxChatTransformer(endpoint config.Endpoint, endpointTransformer string) (transformer.Transformer, error) {
	switch providercompat.NormalizeTransformer(endpointTransformer) {
	case "claude":
		model := endpoint.Model
		if model == "" {
			model = "claude-sonnet-4-20250514"
		}
		return chat.NewClaudeTransformer(model), nil
	case "openai", "deepseek", "kimi", "poe":
		if endpoint.Model == "" {
			return nil, fmt.Errorf("OpenAI transformer requires model field")
		}
		return chat.NewOpenAITransformer(endpoint.Model), nil
	case "openai2":
		if endpoint.Model == "" {
			return nil, fmt.Errorf("OpenAI2 transformer requires model field")
		}
		return chat.NewOpenAI2Transformer(endpoint.Model), nil
	case "gemini":
		if endpoint.Model == "" {
			return nil, fmt.Errorf("Gemini transformer requires model field")
		}
		return chat.NewGeminiTransformer(endpoint.Model), nil
	default:
		return nil, fmt.Errorf("unsupported endpoint transformer for Codex Chat: %s", endpointTransformer)
	}
}

// prepareCxRespTransformer creates transformer for Codex Responses API client
func prepareCxRespTransformer(endpoint config.Endpoint, endpointTransformer string) (transformer.Transformer, error) {
	switch providercompat.NormalizeTransformer(endpointTransformer) {
	case "claude":
		model := endpoint.Model
		if model == "" {
			model = "claude-sonnet-4-20250514"
		}
		return responses.NewClaudeTransformer(model), nil
	case "openai", "deepseek", "kimi", "poe":
		if endpoint.Model == "" {
			return nil, fmt.Errorf("OpenAI transformer requires model field")
		}
		return responses.NewOpenAITransformer(endpoint.Model), nil
	case "openai2":
		if endpoint.Model == "" {
			return nil, fmt.Errorf("OpenAI2 transformer requires model field")
		}
		return responses.NewOpenAI2Transformer(endpoint.Model), nil
	case "gemini":
		if endpoint.Model == "" {
			return nil, fmt.Errorf("Gemini transformer requires model field")
		}
		return responses.NewGeminiTransformer(endpoint.Model), nil
	default:
		return nil, fmt.Errorf("unsupported endpoint transformer for Codex Responses: %s", endpointTransformer)
	}
}

// getTargetPath determines the target API path based on transformer name
func getTargetPath(originalPath string, endpoint config.Endpoint, transformedBody []byte, transformerName string) string {
	switch transformerName {
	case "cc_claude", "cx_chat_claude", "cx_resp_claude":
		return "/v1/messages"
	case "cc_openai", "cx_chat_openai", "cx_resp_openai":
		return providercompat.OpenAIChatTargetPath(endpoint.Transformer, endpoint.APIUrl)
	case "cc_openai2", "cx_resp_openai2", "cx_chat_openai2":
		return "/v1/responses"
	case "cc_gemini", "cx_chat_gemini", "cx_resp_gemini":
		var geminiReq struct {
			Stream bool `json:"stream"`
		}
		json.Unmarshal(transformedBody, &geminiReq)
		if geminiReq.Stream {
			return fmt.Sprintf("/v1beta/models/%s:streamGenerateContent", endpoint.Model)
		}
		return fmt.Sprintf("/v1beta/models/%s:generateContent", endpoint.Model)
	}
	return originalPath
}

// buildProxyRequest creates an HTTP request for the target API
func buildProxyRequest(r *http.Request, endpoint config.Endpoint, apiKey string, transformedBody []byte, transformerName string, credential *storage.EndpointCredential) (*http.Request, error) {
	targetPath := getTargetPath(r.URL.Path, endpoint, transformedBody, transformerName)
	if targetPath == "" {
		targetPath = r.URL.Path
	}

	normalizedAPIUrl := normalizeAPIUrl(endpoint.APIUrl)
	targetPath = normalizeTargetPathForBaseURL(normalizedAPIUrl, targetPath)
	requestBody := transformedBody
	if isOpenAIChatTransformerName(transformerName) {
		requestBody = providercompat.AdaptOpenAIChatPayload(requestBody, endpoint.Transformer, normalizedAPIUrl, endpoint.Thinking)
	}
	if isCodexBackendBaseURL(normalizedAPIUrl) && isResponsesPath(targetPath) {
		requestBody = ensureCodexResponsesPayload(requestBody)
	}
	targetURL := fmt.Sprintf("%s%s", normalizedAPIUrl, targetPath)
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}

	proxyReq, err := http.NewRequest(r.Method, targetURL, bytes.NewReader(requestBody))
	if err != nil {
		return nil, err
	}

	// Copy headers (except Host and Accept-Encoding)
	for key, values := range r.Header {
		if key == "Host" || key == "Accept-Encoding" {
			continue
		}
		for _, value := range values {
			proxyReq.Header.Add(key, value)
		}
	}

	// Force gzip or no compression to avoid unsupported encodings (e.g., brotli)
	proxyReq.Header.Set("Accept-Encoding", "gzip, identity")

	// Set authentication based on transformer type
	switch transformerName {
	case "cc_openai", "cc_openai2", "cx_chat_openai", "cx_chat_openai2", "cx_resp_openai", "cx_resp_openai2":
		proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
	case "cc_gemini", "cx_chat_gemini", "cx_resp_gemini":
		q := proxyReq.URL.Query()
		q.Set("key", apiKey)
		q.Set("alt", "sse")
		proxyReq.URL.RawQuery = q.Encode()
	default:
		// Claude endpoints
		proxyReq.Header.Set("x-api-key", apiKey)
		proxyReq.Header.Set("Authorization", "Bearer "+apiKey)
		normalizeClaudeUpstreamHeaders(proxyReq.Header)
	}

	// Set Host header
	if parsedBase, err := url.Parse(normalizedAPIUrl); err == nil && strings.TrimSpace(parsedBase.Host) != "" {
		proxyReq.Header.Set("Host", parsedBase.Host)
	}
	applyCodexCredentialHeaders(proxyReq, credential, requestBody)

	return proxyReq, nil
}

func isOpenAIChatTransformerName(transformerName string) bool {
	switch strings.ToLower(strings.TrimSpace(transformerName)) {
	case "cc_openai", "cx_chat_openai", "cx_resp_openai":
		return true
	default:
		return false
	}
}

func applyCodexCredentialHeaders(req *http.Request, credential *storage.EndpointCredential, payload []byte) {
	if req == nil || credential == nil {
		return
	}
	if !isCodexProviderType(credential.ProviderType) {
		return
	}
	if !isResponsesPath(req.URL.Path) {
		return
	}

	// Match Codex client headers for oauth credentials.
	ensureHeader(req.Header, "Version", codexClientVersion)
	ensureHeader(req.Header, "Session_id", uuid.NewString())
	ensureHeader(req.Header, "User-Agent", codexUserAgent)

	if isStreamingRequest(payload) {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
	req.Header.Set("Connection", "Keep-Alive")
	req.Header.Set("Originator", "codex_cli_rs")
	if accountID := strings.TrimSpace(credential.AccountID); accountID != "" {
		req.Header.Set("Chatgpt-Account-Id", accountID)
	}
}

func ensureHeader(headers http.Header, key, value string) {
	if headers == nil || strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
		return
	}
	if strings.TrimSpace(headers.Get(key)) == "" {
		headers.Set(key, value)
	}
}

// claudeUpstreamUserAgent presents a Claude-CLI identity to Claude upstreams.
// Some Claude gateways run a WAF that returns 403 "Your request was blocked"
// purely on an OpenAI SDK User-Agent (e.g. "OpenAI/Python", "OpenAI/JS")
// forwarded from non-Claude clients like OpenClaw and Hermes. Overriding the
// User-Agent is sufficient to pass; other client headers are left untouched.
const claudeUpstreamUserAgent = "claude-cli/1.0.119 (external, cli)"

// normalizeClaudeUpstreamHeaders forces a Claude-compatible identity on requests
// sent to a Claude endpoint so foreign-client User-Agents do not trip the WAF.
func normalizeClaudeUpstreamHeaders(headers http.Header) {
	if headers == nil {
		return
	}
	headers.Set("User-Agent", claudeUpstreamUserAgent)
	ensureHeader(headers, "anthropic-version", "2023-06-01")
}

func isResponsesPath(path string) bool {
	trimmed := strings.TrimSpace(path)
	return strings.HasSuffix(trimmed, "/responses") || strings.HasSuffix(trimmed, "/responses/compact")
}

func isStreamingRequest(payload []byte) bool {
	var streamReq struct {
		Stream bool `json:"stream"`
	}
	if err := json.Unmarshal(payload, &streamReq); err != nil {
		return false
	}
	return streamReq.Stream
}

func isCodexProviderType(providerType string) bool {
	p := strings.ToLower(strings.TrimSpace(providerType))
	return p == "" || p == "codex"
}

// normalizeTargetPathForBaseURL adjusts OpenAI Responses paths for Codex backend base URLs.
// This is endpoint URL compatibility handling and is independent from auth mode.
func normalizeTargetPathForBaseURL(baseURL, targetPath string) string {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed == nil {
		return targetPath
	}

	cleanPath := path.Clean(strings.TrimSpace(parsed.Path))
	isCodexBackend := strings.HasSuffix(cleanPath, "/backend-api/codex")
	if !isCodexBackend {
		return providercompat.NormalizeTargetPathForBaseURL(baseURL, targetPath)
	}

	switch strings.TrimSpace(targetPath) {
	case "/v1/responses":
		return "/responses"
	case "/v1/responses/compact":
		return "/responses/compact"
	default:
		return targetPath
	}
}

func isCodexBackendBaseURL(baseURL string) bool {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed == nil {
		return false
	}
	cleanPath := path.Clean(strings.TrimSpace(parsed.Path))
	return strings.HasSuffix(cleanPath, "/backend-api/codex")
}

func ensureCodexResponsesPayload(payload []byte) []byte {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" || strings.HasPrefix(trimmed, "[") {
		return payload
	}

	var body map[string]interface{}
	if err := json.Unmarshal(payload, &body); err != nil {
		return payload
	}
	body["store"] = false
	body["stream"] = true
	if _, ok := body["instructions"]; !ok {
		body["instructions"] = ""
	}
	updated, err := json.Marshal(body)
	if err != nil {
		return payload
	}
	return updated
}

func validateClientJSONRequestBody(payload []byte) error {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return fmt.Errorf("request body is required")
	}

	var body map[string]interface{}
	if err := json.Unmarshal(trimmed, &body); err != nil {
		return fmt.Errorf("invalid JSON request body: %w", err)
	}
	if body == nil {
		return fmt.Errorf("request body must be a JSON object")
	}

	return nil
}

func validateClientRequestForFormat(payload []byte, clientFormat ClientFormat) error {
	var body map[string]interface{}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil
	}
	switch clientFormat {
	case ClientFormatOpenAIResponses:
		if value, ok := body["input"]; !ok || value == nil {
			return fmt.Errorf("field input is required for /v1/responses requests")
		}
	case ClientFormatOpenAIChat:
		if value, ok := body["messages"]; !ok || value == nil {
			return fmt.Errorf("field messages is required for /v1/chat/completions requests")
		}
	case ClientFormatClaude:
		if value, ok := body["messages"]; !ok || value == nil {
			return fmt.Errorf("field messages is required for /v1/messages requests")
		}
	}
	return nil
}

func writeInvalidRequestError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	errorResp := map[string]interface{}{
		"error": map[string]interface{}{
			"type":    "invalid_request_error",
			"message": err.Error(),
		},
	}
	if jsonBytes, marshalErr := json.Marshal(errorResp); marshalErr == nil {
		w.Write(jsonBytes)
	}
}

func writeClientToolChainInvalidError(w http.ResponseWriter, obs requestObservability, endpointName string, attemptNumber int, err error) bool {
	var toolChainErr *convert.InvalidToolChainError
	if !errors.As(err, &toolChainErr) {
		return false
	}

	logger.Warn(
		"Invalid client tool chain: %v %s",
		err,
		requestLogFields(obs, endpointName, attemptNumber, http.StatusBadRequest, "client_tool_chain_invalid"),
	)
	writeInvalidRequestError(w, err)
	return true
}

func validateClientToolChainForFormat(payload []byte, clientFormat ClientFormat) error {
	switch clientFormat {
	case ClientFormatClaude:
		_, err := convert.NormalizeClaudeRequestForUpstream(payload, "client_claude")
		return err
	case ClientFormatOpenAIChat:
		_, err := convert.NormalizeOpenAIChatRequestForUpstream(payload)
		return err
	case ClientFormatOpenAIResponses:
		_, err := convert.NormalizeOpenAI2RequestForUpstream(payload)
		return err
	default:
		return nil
	}
}

func forceStreamInPayload(payload []byte) []byte {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" || strings.HasPrefix(trimmed, "[") {
		return payload
	}

	var body map[string]interface{}
	if err := json.Unmarshal(payload, &body); err != nil {
		return payload
	}
	if body == nil {
		return payload
	}
	body["stream"] = true
	if _, ok := body["messages"]; ok {
		streamOptions, _ := body["stream_options"].(map[string]interface{})
		if streamOptions == nil {
			streamOptions = make(map[string]interface{})
		}
		streamOptions["include_usage"] = true
		body["stream_options"] = streamOptions
	}
	updated, err := json.Marshal(body)
	if err != nil {
		return payload
	}
	return updated
}

func injectEndpointThinkingInPayload(payload []byte, transformerName string, thinking string) []byte {
	effort := normalizeEndpointThinking(thinking)
	if effort == "" {
		return payload
	}

	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" || strings.HasPrefix(trimmed, "[") {
		return payload
	}

	var body map[string]interface{}
	if err := json.Unmarshal(payload, &body); err != nil {
		return payload
	}
	if body == nil {
		return payload
	}

	name := strings.ToLower(strings.TrimSpace(transformerName))
	switch {
	case strings.Contains(name, "openai2"):
		reasoning, _ := body["reasoning"].(map[string]interface{})
		if reasoning == nil {
			reasoning = make(map[string]interface{})
		}
		reasoning["effort"] = effort
		body["reasoning"] = reasoning
	case strings.Contains(name, "openai"):
		body["reasoning_effort"] = effort
	default:
		return payload
	}

	updated, err := json.Marshal(body)
	if err != nil {
		return payload
	}
	return updated
}

func normalizeEndpointThinking(thinking string) string {
	effort := strings.ToLower(strings.TrimSpace(thinking))
	if effort == "" || effort == "off" {
		return ""
	}
	return effort
}

func overrideModelInPayload(payload []byte, model string) []byte {
	if strings.TrimSpace(model) == "" {
		return payload
	}
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" || strings.HasPrefix(trimmed, "[") {
		return payload
	}

	var body map[string]interface{}
	if err := json.Unmarshal(payload, &body); err != nil {
		return payload
	}
	body["model"] = model
	updated, err := json.Marshal(body)
	if err != nil {
		return payload
	}
	return updated
}

func enforceEndpointModelInPayload(payload []byte, endpoint config.Endpoint, transformerName string) []byte {
	if strings.TrimSpace(endpoint.Model) == "" {
		return payload
	}
	if isGeminiTransformerName(transformerName) {
		return payload
	}
	return overrideModelInPayload(payload, endpoint.Model)
}

func isGeminiTransformerName(transformerName string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(transformerName)), "gemini")
}

func extractModelFromPayload(payload []byte) string {
	trimmed := strings.TrimSpace(string(payload))
	if trimmed == "" || strings.HasPrefix(trimmed, "[") {
		return ""
	}

	var body map[string]interface{}
	if err := json.Unmarshal(payload, &body); err != nil {
		return ""
	}
	model, _ := body["model"].(string)
	return strings.TrimSpace(model)
}

// sendRequest sends the HTTP request and returns the response.
func sendRequest(ctx context.Context, proxyReq *http.Request, endpoint config.Endpoint, httpClient *http.Client, cfg *config.Config) (*http.Response, error) {
	return sendRequestWithClientTimeout(ctx, proxyReq, endpoint, httpClient, cfg, true)
}

func sendRequestWithClientTimeout(ctx context.Context, proxyReq *http.Request, endpoint config.Endpoint, httpClient *http.Client, cfg *config.Config, useClientTimeout bool) (*http.Response, error) {
	proxyReq = proxyReq.WithContext(ctx)
	client := httpClient
	if !useClientTimeout {
		clientWithoutTimeout := *httpClient
		clientWithoutTimeout.Timeout = 0
		client = &clientWithoutTimeout
	}

	proxyURL := resolveProxyURLForRequest(cfg, proxyReq.URL, endpoint)
	// Apply proxy if configured
	if strings.TrimSpace(proxyURL) != "" {
		// Clone the client and replace transport for this request
		clientWithProxy := &http.Client{
			Timeout: client.Timeout,
		}

		transport, err := CreateProxyTransport(proxyURL)
		if err != nil {
			logger.Warn("Failed to create proxy transport: %v, using direct connection", err)
			clientWithProxy.Transport = client.Transport
		} else {
			clientWithProxy.Transport = transport
		}

		return clientWithProxy.Do(proxyReq)
	}

	return client.Do(proxyReq)
}

func sendRequestWithResponseHeaderTimeout(ctx context.Context, proxyReq *http.Request, endpoint config.Endpoint, httpClient *http.Client, cfg *config.Config, responseHeaderTimeout time.Duration, useClientTimeout bool) (*http.Response, error) {
	if responseHeaderTimeout <= 0 {
		return sendRequestWithClientTimeout(ctx, proxyReq, endpoint, httpClient, cfg, useClientTimeout)
	}

	requestCtx, cancel := context.WithCancel(ctx)
	var timedOut atomic.Bool
	timer := time.AfterFunc(responseHeaderTimeout, func() {
		timedOut.Store(true)
		cancel()
	})

	resp, err := sendRequestWithClientTimeout(requestCtx, proxyReq, endpoint, httpClient, cfg, useClientTimeout)
	if err != nil {
		timer.Stop()
		cancel()
		if timedOut.Load() {
			return nil, responseHeaderTimeoutError{timeout: responseHeaderTimeout, err: err}
		}
		return nil, err
	}

	timer.Stop()
	if resp != nil && resp.Body != nil {
		resp.Body = &cancelOnCloseReadCloser{
			ReadCloser: resp.Body,
			cancel:     cancel,
		}
	} else {
		cancel()
	}
	return resp, nil
}

type responseHeaderTimeoutError struct {
	timeout time.Duration
	err     error
}

func (e responseHeaderTimeoutError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("timeout awaiting response headers after %s", e.timeout)
	}
	return fmt.Sprintf("timeout awaiting response headers after %s: %v", e.timeout, e.err)
}

func (e responseHeaderTimeoutError) Unwrap() error {
	return e.err
}

type cancelOnCloseReadCloser struct {
	io.ReadCloser
	cancel func()
	once   sync.Once
}

func (r *cancelOnCloseReadCloser) Close() error {
	err := r.ReadCloser.Close()
	r.once.Do(func() {
		if r.cancel != nil {
			r.cancel()
		}
	})
	return err
}

func resolveProxyURLForRequest(cfg *config.Config, targetURL *url.URL, endpoint config.Endpoint) string {
	target := ""
	if targetURL != nil {
		target = targetURL.String()
	}
	if cfg == nil {
		return config.ResolveEndpointProxyURL(&endpoint, target, nil, nil)
	}
	return config.ResolveEndpointProxyURL(&endpoint, target, cfg.GetProxy(), cfg.GetCodexProxy())
}

func isCodexRequestURL(targetURL *url.URL) bool {
	if targetURL == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(targetURL.Host))
	if host != "chatgpt.com" {
		return false
	}
	cleanPath := path.Clean(strings.TrimSpace(targetURL.Path))
	return strings.Contains(cleanPath, "/backend-api/codex")
}

// CreateProxyTransport creates an http.Transport with proxy support
func CreateProxyTransport(proxyURL string) (*http.Transport, error) {
	parsed, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}

	transport := &http.Transport{
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
	}

	switch parsed.Scheme {
	case "socks5", "socks5h":
		auth := &proxy.Auth{}
		if parsed.User != nil {
			auth.User = parsed.User.Username()
			auth.Password, _ = parsed.User.Password()
		} else {
			auth = nil
		}
		dialer, err := proxy.SOCKS5("tcp", parsed.Host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("failed to create SOCKS5 dialer: %w", err)
		}
		transport.Dial = dialer.Dial
	case "http", "https":
		transport.Proxy = http.ProxyURL(parsed)
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", parsed.Scheme)
	}

	return transport, nil
}
