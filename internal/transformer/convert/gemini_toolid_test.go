package convert

import (
	"encoding/json"
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
