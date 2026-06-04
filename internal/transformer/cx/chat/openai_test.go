package chat

import (
	"encoding/json"
	"testing"
)

func TestOpenAITransformerMapsDeveloperRoleToSystem(t *testing.T) {
	rawReq := `{
		"model":"gpt-5.5",
		"stream":true,
		"messages":[
			{"role":"developer","content":"follow policy"},
			{"role":"user","content":"hello"}
		]
	}`

	out, err := NewOpenAITransformer("deepseek-v4-pro").TransformRequest([]byte(rawReq))
	if err != nil {
		t.Fatalf("TransformRequest failed: %v", err)
	}

	var req map[string]interface{}
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatalf("unmarshal transformed req failed: %v", err)
	}
	messages := req["messages"].([]interface{})
	developerMessage := messages[0].(map[string]interface{})
	userMessage := messages[1].(map[string]interface{})

	if developerMessage["role"] != "system" {
		t.Fatalf("expected developer role to map to system, got %#v", developerMessage["role"])
	}
	if developerMessage["content"] != "follow policy" {
		t.Fatalf("expected developer content to be preserved, got %#v", developerMessage["content"])
	}
	if userMessage["role"] != "user" {
		t.Fatalf("expected user role to remain user, got %#v", userMessage["role"])
	}
}
