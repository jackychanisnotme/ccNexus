package proxy

import (
	"sort"
	"strings"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/providercompat"
)

// prioritizeRequestEndpointsForClientFormat orders fallback candidates so endpoints
// whose transformer natively matches the client format are preferred when failing
// over. The manually-selected default endpoint (currentName) always remains the
// request's starting point and is never demoted for format reasons.
func prioritizeRequestEndpointsForClientFormat(endpoints []config.Endpoint, clientFormat ClientFormat, currentName string, obs requestObservability) ([]config.Endpoint, string) {
	if len(endpoints) <= 1 {
		return endpoints, currentName
	}

	prioritized := make([]config.Endpoint, len(endpoints))
	copy(prioritized, endpoints)

	sort.SliceStable(prioritized, func(i, j int) bool {
		return endpointClientFormatPreferenceTier(clientFormat, prioritized[i]) <
			endpointClientFormatPreferenceTier(clientFormat, prioritized[j])
	})

	return prioritized, strings.TrimSpace(currentName)
}

func endpointClientFormatPreferenceTier(clientFormat ClientFormat, endpoint config.Endpoint) int {
	transformerName := providercompat.NormalizeTransformer(endpoint.Transformer)

	switch clientFormat {
	case ClientFormatClaude:
		if transformerName == providercompat.TransformerClaude {
			return 0
		}
		if providercompat.IsOpenAIChatTransformer(transformerName) ||
			providercompat.IsOpenAIResponsesTransformer(transformerName) {
			return 1
		}
		return 2
	case ClientFormatOpenAIChat:
		if providercompat.IsOpenAIChatTransformer(transformerName) {
			return 0
		}
		if providercompat.IsOpenAIResponsesTransformer(transformerName) {
			return 1
		}
		if transformerName == providercompat.TransformerClaude {
			return 2
		}
		return 3
	case ClientFormatOpenAIResponses:
		if providercompat.IsOpenAIResponsesTransformer(transformerName) {
			return 0
		}
		if providercompat.IsOpenAIChatTransformer(transformerName) {
			return 1
		}
		if transformerName == providercompat.TransformerClaude {
			return 2
		}
		return 3
	default:
		return 0
	}
}

func findEndpointByName(endpoints []config.Endpoint, name string) config.Endpoint {
	for _, endpoint := range endpoints {
		if endpoint.Name == name {
			return endpoint
		}
	}
	return config.Endpoint{}
}

func clientFormatPathLabel(clientFormat ClientFormat) string {
	switch clientFormat {
	case ClientFormatOpenAIResponses:
		return "/v1/responses"
	case ClientFormatOpenAIChat:
		return "/v1/chat/completions"
	case ClientFormatClaude:
		return "/v1/messages"
	default:
		return string(clientFormat)
	}
}

func endpointTransformerDisplayName(transformer string) string {
	switch providercompat.NormalizeTransformer(transformer) {
	case providercompat.TransformerClaude:
		return "Claude"
	case providercompat.TransformerOpenAI2:
		return "OpenAI2"
	case providercompat.TransformerOpenAI:
		return "OpenAI"
	case providercompat.TransformerDeepSeek:
		return "DeepSeek"
	case providercompat.TransformerKimi:
		return "Kimi"
	case providercompat.TransformerGemini:
		return "Gemini"
	default:
		return sanitizeLogField(transformer)
	}
}
