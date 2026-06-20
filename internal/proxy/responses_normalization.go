package proxy

import (
	"encoding/json"
	"strings"
)

func normalizeOpenAIResponsesToolSearchArguments(payload []byte, streaming bool) ([]byte, bool) {
	if streaming {
		return normalizeOpenAIResponsesToolSearchArgumentsSSE(payload)
	}
	return normalizeOpenAIResponsesToolSearchArgumentsJSON(payload)
}

func normalizeOpenAIResponsesToolSearchArgumentsSSE(payload []byte) ([]byte, bool) {
	lines := strings.SplitAfter(string(payload), "\n")
	changed := false

	for index, line := range lines {
		lineBody := line
		lineEnding := ""
		if strings.HasSuffix(lineBody, "\n") {
			lineBody = strings.TrimSuffix(lineBody, "\n")
			lineEnding = "\n"
		}
		if strings.HasSuffix(lineBody, "\r") {
			lineBody = strings.TrimSuffix(lineBody, "\r")
			lineEnding = "\r" + lineEnding
		}
		if !strings.HasPrefix(lineBody, "data:") {
			continue
		}

		data := strings.TrimSpace(strings.TrimPrefix(lineBody, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		normalized, dataChanged := normalizeOpenAIResponsesToolSearchArgumentsJSON([]byte(data))
		if !dataChanged {
			continue
		}

		lines[index] = "data: " + string(normalized) + lineEnding
		changed = true
	}

	if !changed {
		return payload, false
	}
	return []byte(strings.Join(lines, "")), true
}

func normalizeOpenAIResponsesToolSearchArgumentsJSON(payload []byte) ([]byte, bool) {
	var root map[string]interface{}

	if err := json.Unmarshal(payload, &root); err != nil {
		return payload, false
	}
	if !normalizeOpenAIResponsesToolSearchArgumentsObject(root) {
		return payload, false
	}

	normalized, err := json.Marshal(root)
	if err != nil {
		return payload, false
	}
	return normalized, true
}

func normalizeOpenAIResponsesToolSearchArgumentsObject(root map[string]interface{}) bool {
	changed := normalizeOpenAIResponsesToolSearchCall(root)

	if item, ok := root["item"].(map[string]interface{}); ok {
		changed = normalizeOpenAIResponsesToolSearchCall(item) || changed
	}
	if output, ok := root["output"].([]interface{}); ok {
		changed = normalizeOpenAIResponsesToolSearchArgumentsOutput(output) || changed
	}
	if response, ok := root["response"].(map[string]interface{}); ok {
		changed = normalizeOpenAIResponsesToolSearchArgumentsObject(response) || changed
	}

	return changed
}

func normalizeOpenAIResponsesToolSearchArgumentsOutput(output []interface{}) bool {
	changed := false
	for _, value := range output {
		item, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		changed = normalizeOpenAIResponsesToolSearchCall(item) || changed
	}
	return changed
}

func normalizeOpenAIResponsesToolSearchCall(item map[string]interface{}) bool {
	if item["type"] != "tool_search_call" {
		return false
	}
	arguments, ok := item["arguments"].(string)
	if !ok {
		return false
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &parsed); err != nil || parsed == nil {
		return false
	}
	item["arguments"] = parsed
	return true
}
