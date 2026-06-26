package convert

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lich0821/ccNexus/internal/transformer"
)

func NormalizeClaudeRequestForUpstream(payload []byte, protocol string) ([]byte, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, err
	}

	rawMessages, ok := body["messages"].([]interface{})
	if !ok {
		return payload, nil
	}

	messages := make([]map[string]interface{}, 0, len(rawMessages))
	for idx, rawMessage := range rawMessages {
		message, ok := rawMessage.(map[string]interface{})
		if !ok {
			return nil, &InvalidToolChainError{
				Protocol: protocol, MessageIndex: idx,
				Reason: "message is not an object",
			}
		}
		messages = append(messages, message)
	}

	normalizedMessages, err := normalizeClaudeMapMessagesForToolChains(messages, protocol)
	if err != nil {
		return nil, err
	}
	body["messages"] = normalizedMessages

	return json.Marshal(body)
}

// InvalidToolChainError reports client history that cannot be made valid for a
// target protocol without inventing or dropping tool results.
type InvalidToolChainError struct {
	Protocol     string
	CallID       string
	MessageIndex int
	Reason       string
}

func (e *InvalidToolChainError) Error() string {
	protocol := strings.TrimSpace(e.Protocol)
	if protocol == "" {
		protocol = "unknown"
	}
	reason := strings.TrimSpace(e.Reason)
	if reason == "" {
		reason = "invalid tool call chain"
	}
	if strings.TrimSpace(e.CallID) == "" {
		return fmt.Sprintf("%s: %s at message/item %d", protocol, reason, e.MessageIndex)
	}
	return fmt.Sprintf("%s: %s for call_id %s at message/item %d", protocol, reason, e.CallID, e.MessageIndex)
}

type claudeToolChainGroup struct {
	protocol       string
	startIndex     int
	pending        map[string]bool
	toolMessages   []map[string]interface{}
	resultBlocks   []map[string]interface{}
	deferred       []map[string]interface{}
	deferredOthers []map[string]interface{}
}

func newClaudeToolChainGroup(protocol string, startIndex int) *claudeToolChainGroup {
	return &claudeToolChainGroup{
		protocol:   protocol,
		startIndex: startIndex,
		pending:    make(map[string]bool),
	}
}

func normalizeClaudeTypedMessagesForToolChains(messages []transformer.ClaudeMessage, protocol string) ([]transformer.ClaudeMessage, error) {
	mapped := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		m := map[string]interface{}{
			"role":    msg.Role,
			"content": msg.Content,
		}
		if msg.CacheControl != nil {
			m["cache_control"] = msg.CacheControl
		}
		mapped = append(mapped, m)
	}

	normalized, err := normalizeClaudeMapMessagesForToolChains(mapped, protocol)
	if err != nil {
		return nil, err
	}

	result := make([]transformer.ClaudeMessage, 0, len(normalized))
	for _, msg := range normalized {
		role, _ := msg["role"].(string)
		result = append(result, transformer.ClaudeMessage{
			Role:         role,
			Content:      msg["content"],
			CacheControl: msg["cache_control"],
		})
	}
	return result, nil
}

func normalizeClaudeMapMessagesForToolChains(messages []map[string]interface{}, protocol string) ([]map[string]interface{}, error) {
	var output []map[string]interface{}
	var group *claudeToolChainGroup

	flushGroup := func() {
		output = append(output, group.toolMessages...)
		resultContent := make([]interface{}, 0, len(group.resultBlocks)+len(group.deferred))
		for _, block := range group.resultBlocks {
			resultContent = append(resultContent, block)
		}
		for _, deferred := range group.deferred {
			for _, block := range claudeContentToBlocks(deferred["content"]) {
				resultContent = append(resultContent, block)
			}
		}
		output = append(output, map[string]interface{}{
			"role":    "user",
			"content": resultContent,
		})
		output = append(output, group.deferredOthers...)
		group = nil
	}

	for idx, msg := range messages {
		toolUseIDs, err := claudeToolUseIDs(msg, idx, protocol)
		if err != nil {
			return nil, err
		}
		toolResultIDs, toolResultBlocks, err := claudeToolResultIDsAndBlocks(msg, idx, protocol)
		if err != nil {
			return nil, err
		}

		switch {
		case len(toolUseIDs) > 0:
			if group == nil {
				group = newClaudeToolChainGroup(protocol, idx)
			}
			for _, callID := range toolUseIDs {
				if group.pending[callID] {
					return nil, &InvalidToolChainError{
						Protocol: protocol, CallID: callID, MessageIndex: idx,
						Reason: "duplicate tool call id",
					}
				}
				group.pending[callID] = true
			}
			group.toolMessages = append(group.toolMessages, msg)

		case len(toolResultIDs) > 0:
			if group == nil {
				return nil, &InvalidToolChainError{
					Protocol: protocol, CallID: toolResultIDs[0], MessageIndex: idx,
					Reason: "tool result without preceding tool call",
				}
			}
			for _, callID := range toolResultIDs {
				if !group.pending[callID] {
					return nil, &InvalidToolChainError{
						Protocol: protocol, CallID: callID, MessageIndex: idx,
						Reason: "tool result does not match pending tool call",
					}
				}
				delete(group.pending, callID)
			}
			group.resultBlocks = append(group.resultBlocks, toolResultBlocks...)
			if len(group.pending) == 0 {
				flushGroup()
			}

		default:
			if group == nil {
				output = append(output, msg)
				continue
			}
			if role, _ := msg["role"].(string); role == "user" {
				group.deferred = append(group.deferred, msg)
			} else {
				group.deferredOthers = append(group.deferredOthers, msg)
			}
		}
	}

	if group != nil {
		callID := firstPendingCallID(group.pending)
		return nil, &InvalidToolChainError{
			Protocol: protocol, CallID: callID, MessageIndex: group.startIndex,
			Reason: "tool call missing corresponding tool result",
		}
	}

	return output, nil
}

func claudeToolUseIDs(msg map[string]interface{}, idx int, protocol string) ([]string, error) {
	role, _ := msg["role"].(string)
	if role != "assistant" {
		return nil, nil
	}
	return extractClaudeBlockIDs(msg["content"], "tool_use", "id", idx, protocol)
}

func claudeToolResultIDsAndBlocks(msg map[string]interface{}, idx int, protocol string) ([]string, []map[string]interface{}, error) {
	role, _ := msg["role"].(string)
	if role != "user" {
		return nil, nil, nil
	}
	blocks := claudeContentToBlocks(msg["content"])
	var ids []string
	for _, block := range blocks {
		if blockType, _ := block["type"].(string); blockType != "tool_result" {
			continue
		}
		callID, _ := block["tool_use_id"].(string)
		callID = strings.TrimSpace(callID)
		if callID == "" {
			continue
		}
		ids = append(ids, callID)
	}
	return ids, blocks, nil
}

func extractClaudeBlockIDs(content interface{}, blockType string, field string, idx int, protocol string) ([]string, error) {
	blocks := claudeContentToBlocks(content)
	var ids []string
	for _, block := range blocks {
		if t, _ := block["type"].(string); t != blockType {
			continue
		}
		id, _ := block[field].(string)
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func claudeContentToBlocks(content interface{}) []map[string]interface{} {
	switch value := content.(type) {
	case string:
		if value == "" {
			return nil
		}
		return []map[string]interface{}{{"type": "text", "text": value}}
	case []map[string]interface{}:
		blocks := make([]map[string]interface{}, 0, len(value))
		for _, block := range value {
			blocks = append(blocks, cloneMapWithoutKeys(block))
		}
		return blocks
	case []interface{}:
		blocks := make([]map[string]interface{}, 0, len(value))
		for _, raw := range value {
			block, ok := raw.(map[string]interface{})
			if !ok {
				continue
			}
			blocks = append(blocks, cloneMapWithoutKeys(block))
		}
		return blocks
	default:
		return nil
	}
}

func firstPendingCallID(pending map[string]bool) string {
	for callID := range pending {
		return callID
	}
	return ""
}

func normalizeOpenAIMessagesForToolChains(messages []transformer.OpenAIMessage, protocol string) ([]transformer.OpenAIMessage, error) {
	var output []transformer.OpenAIMessage
	var group *openAIToolChainGroup

	flushGroup := func() {
		output = append(output, group.toolMessages...)
		output = append(output, group.resultMessages...)
		output = append(output, group.deferred...)
		group = nil
	}

	for idx, msg := range messages {
		switch {
		case msg.Role == "assistant" && len(msg.ToolCalls) > 0:
			if group == nil {
				group = newOpenAIToolChainGroup(protocol, idx)
			}
			for _, toolCall := range msg.ToolCalls {
				callID := strings.TrimSpace(toolCall.ID)
				if callID == "" {
					return nil, &InvalidToolChainError{Protocol: protocol, MessageIndex: idx, Reason: "tool call missing id"}
				}
				if group.pending[callID] {
					return nil, &InvalidToolChainError{Protocol: protocol, CallID: callID, MessageIndex: idx, Reason: "duplicate tool call id"}
				}
				group.pending[callID] = true
			}
			group.toolMessages = append(group.toolMessages, msg)

		case msg.Role == "tool":
			callID := strings.TrimSpace(msg.ToolCallID)
			if callID == "" {
				return nil, &InvalidToolChainError{Protocol: protocol, MessageIndex: idx, Reason: "tool result missing tool_call_id"}
			}
			if group == nil || !group.pending[callID] {
				return nil, &InvalidToolChainError{Protocol: protocol, CallID: callID, MessageIndex: idx, Reason: "tool result without pending tool call"}
			}
			delete(group.pending, callID)
			group.resultMessages = append(group.resultMessages, msg)
			if len(group.pending) == 0 {
				flushGroup()
			}

		default:
			if group == nil {
				output = append(output, msg)
			} else {
				group.deferred = append(group.deferred, msg)
			}
		}
	}

	if group != nil {
		callID := firstPendingCallID(group.pending)
		return nil, &InvalidToolChainError{Protocol: protocol, CallID: callID, MessageIndex: group.startIndex, Reason: "tool call missing corresponding tool result"}
	}
	return output, nil
}

type openAIToolChainGroup struct {
	protocol       string
	startIndex     int
	pending        map[string]bool
	toolMessages   []transformer.OpenAIMessage
	resultMessages []transformer.OpenAIMessage
	deferred       []transformer.OpenAIMessage
}

func newOpenAIToolChainGroup(protocol string, startIndex int) *openAIToolChainGroup {
	return &openAIToolChainGroup{protocol: protocol, startIndex: startIndex, pending: make(map[string]bool)}
}

func normalizeOpenAI2InputForToolChains(input []interface{}, protocol string) ([]interface{}, error) {
	var output []interface{}
	var group *openAI2ToolChainGroup

	flushGroup := func() {
		output = append(output, group.toolItems...)
		output = append(output, group.resultItems...)
		output = append(output, group.deferred...)
		group = nil
	}

	for idx, raw := range input {
		item, ok := raw.(map[string]interface{})
		if !ok {
			if group == nil {
				output = append(output, raw)
			} else {
				group.deferred = append(group.deferred, raw)
			}
			continue
		}

		itemType := strings.ToLower(strings.TrimSpace(stringFromMap(item, "type")))
		switch itemType {
		case "function_call":
			callID := firstStringFromMap(item, "call_id", "id")
			if callID == "" {
				return nil, &InvalidToolChainError{Protocol: protocol, MessageIndex: idx, Reason: "function_call missing call_id"}
			}
			if group == nil {
				group = newOpenAI2ToolChainGroup(protocol, idx)
			}
			if group.pending[callID] {
				return nil, &InvalidToolChainError{Protocol: protocol, CallID: callID, MessageIndex: idx, Reason: "duplicate function_call id"}
			}
			group.pending[callID] = true
			group.toolItems = append(group.toolItems, raw)

		case "function_call_output":
			callID := firstStringFromMap(item, "call_id", "tool_call_id", "id")
			if callID == "" {
				return nil, &InvalidToolChainError{Protocol: protocol, MessageIndex: idx, Reason: "function_call_output missing call_id"}
			}
			if group == nil || !group.pending[callID] {
				return nil, &InvalidToolChainError{Protocol: protocol, CallID: callID, MessageIndex: idx, Reason: "function_call_output without pending function_call"}
			}
			delete(group.pending, callID)
			group.resultItems = append(group.resultItems, raw)
			if len(group.pending) == 0 {
				flushGroup()
			}

		default:
			if group == nil {
				output = append(output, raw)
			} else {
				group.deferred = append(group.deferred, raw)
			}
		}
	}

	if group != nil {
		callID := firstPendingCallID(group.pending)
		return nil, &InvalidToolChainError{Protocol: protocol, CallID: callID, MessageIndex: group.startIndex, Reason: "function_call missing corresponding function_call_output"}
	}
	return output, nil
}

type openAI2ToolChainGroup struct {
	protocol    string
	startIndex  int
	pending     map[string]bool
	toolItems   []interface{}
	resultItems []interface{}
	deferred    []interface{}
}

func newOpenAI2ToolChainGroup(protocol string, startIndex int) *openAI2ToolChainGroup {
	return &openAI2ToolChainGroup{protocol: protocol, startIndex: startIndex, pending: make(map[string]bool)}
}
