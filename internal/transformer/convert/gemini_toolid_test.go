package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/transformer"
)

// TestGeminiRespToClaudeUniqueToolIDs verifies that two function calls to the
// same tool name in one response produce distinct Claude tool_use IDs
// (regression for name-based ID collisions).
func TestGeminiRespToClaudeUniqueToolIDs(t *testing.T) {
	resp := `{"candidates":[{"content":{"role":"model","parts":[` +
		`{"functionCall":{"name":"read_file","args":{"path":"a"}}},` +
		`{"functionCall":{"name":"read_file","args":{"path":"b"}}}` +
		`]},"finishReason":"STOP"}]}`

	out, err := GeminiRespToClaude([]byte(resp))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed struct {
		Content []map[string]interface{} `json:"content"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	ids := map[string]bool{}
	for _, block := range parsed.Content {
		if block["type"] == "tool_use" {
			id, _ := block["id"].(string)
			if id == "" {
				t.Fatalf("tool_use block missing id: %v", block)
			}
			if ids[id] {
				t.Fatalf("duplicate tool_use id: %s", id)
			}
			ids[id] = true
		}
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 unique tool_use ids, got %d", len(ids))
	}
}

// TestGeminiStreamToClaudeUniqueToolIDs verifies the streaming path also emits
// distinct IDs for repeated same-name function calls across events.
func TestGeminiStreamToClaudeUniqueToolIDs(t *testing.T) {
	ctx := transformer.NewStreamContext()
	ids := map[string]bool{}

	collect := func(out []byte) {
		for _, line := range splitDataLines(out) {
			var evt map[string]interface{}
			if json.Unmarshal([]byte(line), &evt) != nil {
				continue
			}
			cb, _ := evt["content_block"].(map[string]interface{})
			if cb != nil && cb["type"] == "tool_use" {
				if id, _ := cb["id"].(string); id != "" {
					if ids[id] {
						t.Fatalf("duplicate streaming tool_use id: %s", id)
					}
					ids[id] = true
				}
			}
		}
	}

	for i := 0; i < 2; i++ {
		event := []byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"functionCall\":{\"name\":\"read_file\",\"args\":{}}}]}}]}\n\n")
		out, err := GeminiStreamToClaude(event, ctx)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		collect(out)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 unique streaming tool_use ids, got %d", len(ids))
	}
}

func TestGeminiStreamToClaudeMapsThoughtToThinking(t *testing.T) {
	ctx := transformer.NewStreamContext()
	event := []byte("data: {\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hidden reason\",\"thought\":true},{\"text\":\"visible answer\"}]},\"finishReason\":\"STOP\"}]}\n\n")
	out, err := GeminiStreamToClaude(event, ctx)
	if err != nil {
		t.Fatalf("GeminiStreamToClaude failed: %v", err)
	}
	full := string(out)
	if !strings.Contains(full, `"type":"thinking"`) || !strings.Contains(full, `"thinking":"hidden reason"`) {
		t.Fatalf("expected Gemini thought to become Claude thinking, got: %s", full)
	}
	if !strings.Contains(full, `"text":"visible answer"`) {
		t.Fatalf("expected visible answer text, got: %s", full)
	}
	if strings.Contains(full, `"type":"text_delta","text":"hidden reason"`) {
		t.Fatalf("Gemini thought leaked as visible text: %s", full)
	}
}

func TestOpenAI2ReqToGeminiFunctionOutputFallsBackToItemID(t *testing.T) {
	req := `{
		"model":"gpt-4.1",
		"input":[
			{"type":"function_call","id":"fc_1","name":"lookup","arguments":"{\"q\":\"weather\"}"},
			{"type":"function_call_output","id":"fc_1","output":"sunny"}
		]
	}`
	out, err := OpenAI2ReqToGemini([]byte(req), "gemini-2.5-pro")
	if err != nil {
		t.Fatalf("OpenAI2ReqToGemini failed: %v", err)
	}
	var geminiReq map[string]interface{}
	if err := json.Unmarshal(out, &geminiReq); err != nil {
		t.Fatalf("unmarshal gemini request: %v", err)
	}
	contents := geminiReq["contents"].([]interface{})
	if len(contents) != 2 {
		t.Fatalf("expected model functionCall and user functionResponse contents, got %#v", contents)
	}
	responseContent := contents[1].(map[string]interface{})
	parts := responseContent["parts"].([]interface{})
	functionResponse := parts[0].(map[string]interface{})["functionResponse"].(map[string]interface{})
	if functionResponse["name"] != "lookup" {
		t.Fatalf("expected functionResponse name lookup via id fallback, got %#v", functionResponse)
	}
}

func splitDataLines(out []byte) []string {
	var lines []string
	for _, raw := range splitLines(string(out)) {
		if len(raw) > 6 && raw[:6] == "data: " {
			lines = append(lines, raw[6:])
		}
	}
	return lines
}

func splitLines(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == '\n' {
			out = append(out, cur)
			cur = ""
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
