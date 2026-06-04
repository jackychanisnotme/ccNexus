package providercompat

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strings"
)

const (
	TransformerClaude   = "claude"
	TransformerOpenAI   = "openai"
	TransformerOpenAI2  = "openai2"
	TransformerGemini   = "gemini"
	TransformerDeepSeek = "deepseek"
	TransformerKimi     = "kimi"

	ProviderDeepSeek = "deepseek"
	ProviderKimi     = "kimi"
)

const errorBodyMaxChars = 512

var compatSuffixes = []string{
	"/api/claudecode",
	"/api/anthropic",
	"/apps/anthropic",
	"/api/coding",
	"/claudecode",
	"/anthropic",
	"/step_plan",
	"/coding",
	"/claude",
}

func init() {
	sort.SliceStable(compatSuffixes, func(i, j int) bool {
		return len(compatSuffixes[i]) > len(compatSuffixes[j])
	})
}

// NormalizeTransformer canonicalizes endpoint transformer names and aliases.
func NormalizeTransformer(transformer string) string {
	switch strings.ToLower(strings.TrimSpace(transformer)) {
	case "", TransformerClaude:
		return TransformerClaude
	case TransformerOpenAI, "openai_chat", "chat", "chat_completions":
		return TransformerOpenAI
	case TransformerOpenAI2, "openai_responses", "responses":
		return TransformerOpenAI2
	case TransformerGemini, "google", "google_gemini":
		return TransformerGemini
	case TransformerDeepSeek, "deepseek_chat":
		return TransformerDeepSeek
	case TransformerKimi, "moonshot", "moonshotai":
		return TransformerKimi
	default:
		return strings.ToLower(strings.TrimSpace(transformer))
	}
}

func IsOpenAIChatTransformer(transformer string) bool {
	switch NormalizeTransformer(transformer) {
	case TransformerOpenAI, TransformerDeepSeek, TransformerKimi:
		return true
	default:
		return false
	}
}

func IsOpenAIResponsesTransformer(transformer string) bool {
	return NormalizeTransformer(transformer) == TransformerOpenAI2
}

func IsGeminiTransformer(transformer string) bool {
	return NormalizeTransformer(transformer) == TransformerGemini
}

func IsClaudeTransformer(transformer string) bool {
	return NormalizeTransformer(transformer) == TransformerClaude
}

func ProviderKind(transformer, baseURL string) string {
	switch NormalizeTransformer(transformer) {
	case TransformerDeepSeek:
		return ProviderDeepSeek
	case TransformerKimi:
		return ProviderKimi
	}

	parsed, err := url.Parse(NormalizeBaseURL(baseURL))
	if err != nil || parsed == nil {
		return ""
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Host))
	switch {
	case strings.Contains(host, "deepseek.com"):
		return ProviderDeepSeek
	case strings.Contains(host, "moonshot") || strings.Contains(host, "kimi"):
		return ProviderKimi
	default:
		return ""
	}
}

func OpenAIChatTargetPath(transformer, baseURL string) string {
	if UsesDeepSeekRootPaths(transformer, baseURL) {
		return "/chat/completions"
	}
	return "/v1/chat/completions"
}

func UsesDeepSeekRootPaths(transformer, baseURL string) bool {
	if NormalizeTransformer(transformer) != TransformerDeepSeek {
		return false
	}

	parsed, err := url.Parse(NormalizeBaseURL(baseURL))
	if err != nil || parsed == nil {
		return false
	}
	host := strings.ToLower(strings.TrimSpace(parsed.Hostname()))
	return host == "deepseek.com" || strings.HasSuffix(host, ".deepseek.com")
}

func DefaultModel(transformer string) string {
	switch NormalizeTransformer(transformer) {
	case TransformerClaude:
		return "claude-sonnet-4-5-20250929"
	case TransformerOpenAI2:
		return "gpt-5-codex"
	case TransformerGemini:
		return "gemini-2.0-flash"
	case TransformerDeepSeek:
		return "deepseek-v4-pro"
	case TransformerKimi:
		return "kimi-k2.6"
	default:
		return "gpt-4-turbo"
	}
}

func Owner(transformer string) string {
	switch NormalizeTransformer(transformer) {
	case TransformerClaude:
		return "anthropic"
	case TransformerGemini:
		return "google"
	case TransformerDeepSeek:
		return "deepseek"
	case TransformerKimi:
		return "moonshot"
	default:
		return "openai"
	}
}

func NormalizeBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "http://") && !strings.HasPrefix(trimmed, "https://") {
		return "https://" + trimmed
	}
	return trimmed
}

func JoinBaseURLAndPath(baseURL, targetPath string) string {
	base := NormalizeBaseURL(baseURL)
	return fmt.Sprintf("%s%s", base, NormalizeTargetPathForBaseURL(base, targetPath))
}

// NormalizeTargetPathForBaseURL prevents common duplicated-path mistakes:
// - base=https://host/v1 + target=/v1/chat/completions => /chat/completions
// - base=https://host/v1/chat/completions + target=/v1/chat/completions => ""
func NormalizeTargetPathForBaseURL(baseURL, targetPath string) string {
	target := cleanAPIPath(targetPath)
	if target == "" || target == "/" {
		return targetPath
	}

	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed == nil {
		return targetPath
	}

	basePath := cleanAPIPath(parsed.Path)
	if basePath == "" || basePath == "/" {
		return target
	}
	if basePath == target || strings.HasSuffix(basePath, target) {
		return ""
	}
	if strings.HasSuffix(basePath, "/v1") && strings.HasPrefix(target, "/v1/") {
		return strings.TrimPrefix(target, "/v1")
	}
	if strings.HasSuffix(basePath, "/v1beta") && strings.HasPrefix(target, "/v1beta/") {
		return strings.TrimPrefix(target, "/v1beta")
	}

	return target
}

func BuildOpenAIModelURLCandidates(baseURL, transformer string) ([]string, error) {
	normalized := NormalizeBaseURL(baseURL)
	if normalized == "" {
		return nil, fmt.Errorf("base URL is empty")
	}

	candidates := make([]string, 0, 5)
	add := func(candidate string) {
		candidate = strings.TrimRight(strings.TrimSpace(candidate), "/")
		if candidate == "" {
			return
		}
		for _, existing := range candidates {
			if existing == candidate {
				return
			}
		}
		candidates = append(candidates, candidate)
	}

	if UsesDeepSeekRootPaths(transformer, normalized) {
		if root := originURL(normalized); root != "" {
			add(root + "/models")
		}
	}

	add(JoinBaseURLAndPath(normalized, "/v1/models"))
	add(JoinBaseURLAndPath(normalized, "/models"))

	if stripped := StripCompatSuffix(normalized); stripped != "" {
		add(JoinBaseURLAndPath(stripped, "/v1/models"))
		add(JoinBaseURLAndPath(stripped, "/models"))
	}

	return candidates, nil
}

func StripCompatSuffix(baseURL string) string {
	normalized := NormalizeBaseURL(baseURL)
	parsed, err := url.Parse(normalized)
	if err != nil || parsed == nil {
		return ""
	}
	basePath := strings.TrimRight(cleanAPIPath(parsed.Path), "/")
	for _, suffix := range compatSuffixes {
		if strings.HasSuffix(basePath, suffix) {
			parsed.Path = strings.TrimRight(strings.TrimSuffix(basePath, suffix), "/")
			parsed.RawPath = ""
			parsed.RawQuery = ""
			parsed.Fragment = ""
			return strings.TrimRight(parsed.String(), "/")
		}
	}
	return ""
}

func TruncateErrorBody(body string) string {
	if len([]rune(body)) <= errorBodyMaxChars {
		return body
	}
	runes := []rune(body)
	return string(runes[:errorBodyMaxChars]) + "..."
}

func AdaptOpenAIChatPayload(payload []byte, transformer, baseURL, thinking string) []byte {
	provider := ProviderKind(transformer, baseURL)
	if provider == "" {
		return payload
	}

	var body map[string]interface{}
	if err := json.Unmarshal(payload, &body); err != nil || body == nil {
		return payload
	}

	if value, ok := body["max_completion_tokens"]; ok {
		if _, exists := body["max_tokens"]; !exists {
			body["max_tokens"] = value
		}
		delete(body, "max_completion_tokens")
	}

	endpointThinking := normalizeThinking(thinking)
	requestEffort := ""
	if reasoning, ok := body["reasoning"].(map[string]interface{}); ok {
		if value := stringFromMap(reasoning, "effort"); value != "" {
			requestEffort = value
		}
		delete(body, "reasoning")
	}
	if value := stringFromMap(body, "reasoning_effort"); value != "" {
		requestEffort = value
	}

	if provider == ProviderDeepSeek {
		applyDeepSeekThinking(body, endpointThinking, requestEffort)
	} else {
		effort := requestEffort
		if effort == "" && endpointThinking != "off" {
			effort = endpointThinking
		}
		if effort == "" {
			updated, err := json.Marshal(body)
			if err != nil {
				return payload
			}
			return updated
		}
		body["reasoning_effort"] = effort
		if _, exists := body["thinking"]; !exists {
			body["thinking"] = map[string]interface{}{"type": "enabled"}
		}
	}

	updated, err := json.Marshal(body)
	if err != nil {
		return payload
	}
	return updated
}

func applyDeepSeekThinking(body map[string]interface{}, endpointThinking, requestEffort string) {
	if endpointThinking == "off" {
		delete(body, "reasoning_effort")
		body["thinking"] = map[string]interface{}{"type": "disabled"}
		return
	}

	effort := requestEffort
	if endpointThinking != "" {
		effort = endpointThinking
	}

	effort = normalizeDeepSeekThinkingEffort(effort)
	if effort != "" {
		if deepSeekChatHistoryLacksReasoningContent(body) {
			delete(body, "reasoning_effort")
			body["thinking"] = map[string]interface{}{"type": "disabled"}
			return
		}
		body["reasoning_effort"] = effort
		body["thinking"] = map[string]interface{}{"type": "enabled"}
		return
	}
}

func deepSeekChatHistoryLacksReasoningContent(body map[string]interface{}) bool {
	messages, ok := body["messages"].([]interface{})
	if !ok {
		return false
	}

	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]interface{})
		if !ok {
			continue
		}
		role := strings.ToLower(strings.TrimSpace(stringFromMap(message, "role")))
		if role != "assistant" {
			continue
		}
		if strings.TrimSpace(stringFromMap(message, "reasoning_content")) == "" {
			return true
		}
	}
	return false
}

func cleanAPIPath(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	cleaned := path.Clean(trimmed)
	if cleaned == "." {
		return ""
	}
	if cleaned == "/" {
		return "/"
	}
	return strings.TrimRight(cleaned, "/")
}

func originURL(raw string) string {
	parsed, err := url.Parse(NormalizeBaseURL(raw))
	if err != nil || parsed == nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func normalizeThinking(thinking string) string {
	effort := strings.ToLower(strings.TrimSpace(thinking))
	switch effort {
	case "", "default", "auto", "inherit":
		return ""
	case "off":
		return "off"
	default:
		return effort
	}
}

func normalizeDeepSeekThinkingEffort(thinking string) string {
	switch strings.ToLower(strings.TrimSpace(thinking)) {
	case "low", "medium", "high":
		return "high"
	case "xhigh", "max":
		return "max"
	default:
		return ""
	}
}

func stringFromMap(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.ToLower(strings.TrimSpace(value))
}
