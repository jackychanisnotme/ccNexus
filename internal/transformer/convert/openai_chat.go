package convert

import (
	"encoding/json"
	"strings"

	"github.com/lich0821/ccNexus/internal/transformer"
)

// NormalizeOpenAIChatRequestForUpstream maps newer OpenAI chat roles to roles
// commonly accepted by OpenAI-compatible upstreams such as DeepSeek and keeps
// tool-call/tool-result pairs adjacent when switching between endpoint formats.
func NormalizeOpenAIChatRequestForUpstream(payload []byte) ([]byte, error) {
	var req transformer.OpenAIRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return nil, err
	}
	if len(req.Messages) == 0 {
		return payload, nil
	}

	for i := range req.Messages {
		if strings.EqualFold(strings.TrimSpace(req.Messages[i].Role), "developer") {
			req.Messages[i].Role = "system"
		}
	}

	normalizedMessages, err := normalizeOpenAIMessagesForToolChains(req.Messages, "openai_chat_upstream")
	if err != nil {
		return nil, err
	}
	req.Messages = normalizedMessages

	return json.Marshal(req)
}
