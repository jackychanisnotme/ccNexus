package convert

import "encoding/json"

// NormalizeOpenAIChatRequestForUpstream maps newer OpenAI chat roles to roles
// commonly accepted by OpenAI-compatible upstreams such as DeepSeek.
func NormalizeOpenAIChatRequestForUpstream(payload []byte) ([]byte, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(payload, &body); err != nil {
		return nil, err
	}

	messages, ok := body["messages"].([]interface{})
	if !ok {
		return payload, nil
	}

	changed := false
	for _, rawMessage := range messages {
		message, ok := rawMessage.(map[string]interface{})
		if !ok {
			continue
		}
		if role, _ := message["role"].(string); role == "developer" {
			message["role"] = "system"
			changed = true
		}
	}
	if !changed {
		return payload, nil
	}

	normalized, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}
