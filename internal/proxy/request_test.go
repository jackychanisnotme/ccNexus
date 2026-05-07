package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/logger"
)

func TestEnsureCodexResponsesPayload(t *testing.T) {
	raw := []byte(`{"model":"gpt-4.1","stream":true}`)
	out := ensureCodexResponsesPayload(raw)

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	store, ok := payload["store"].(bool)
	if !ok || store {
		t.Fatalf("expected store=false, got %#v", payload["store"])
	}
	stream, ok := payload["stream"].(bool)
	if !ok || !stream {
		t.Fatalf("expected stream=true, got %#v", payload["stream"])
	}
	if instructions, ok := payload["instructions"].(string); !ok || instructions != "" {
		t.Fatalf("expected instructions empty string, got %#v", payload["instructions"])
	}
}

func TestEnsureCodexResponsesPayloadOverridesStoreAndStream(t *testing.T) {
	raw := []byte(`{"model":"gpt-4.1","store":true}`)
	out := ensureCodexResponsesPayload(raw)

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}

	store, ok := payload["store"].(bool)
	if !ok || store {
		t.Fatalf("expected store=false, got %#v", payload["store"])
	}
	stream, ok := payload["stream"].(bool)
	if !ok || !stream {
		t.Fatalf("expected stream=true, got %#v", payload["stream"])
	}
}

func TestNormalizeTargetPathForBaseURLOnCodexBackend(t *testing.T) {
	got := normalizeTargetPathForBaseURL("https://chatgpt.com/backend-api/codex", "/v1/responses")
	if got != "/responses" {
		t.Fatalf("expected /responses, got %s", got)
	}
}

func TestOverrideModelInPayload(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.3-codex","stream":true}`)
	out := overrideModelInPayload(raw, "gpt-5.2-codex")

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if payload["model"] != "gpt-5.2-codex" {
		t.Fatalf("expected model override to gpt-5.2-codex, got %#v", payload["model"])
	}
}

func TestValidateClientJSONRequestBody(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr bool
	}{
		{name: "object", body: `{"model":"gpt-5.5"}`},
		{name: "empty", body: ``, wantErr: true},
		{name: "whitespace", body: `   `, wantErr: true},
		{name: "truncated", body: `{"model":`, wantErr: true},
		{name: "array", body: `[]`, wantErr: true},
		{name: "null", body: `null`, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateClientJSONRequestBody([]byte(tt.body))
			if tt.wantErr && err == nil {
				t.Fatal("expected validation error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("did not expect validation error: %v", err)
			}
		})
	}
}

func TestHandleProxyInvalidBodyLogIncludesMethodAndPath(t *testing.T) {
	logger.GetLogger().Clear()

	req := httptest.NewRequest(http.MethodPost, "/v1/responses?token=hidden", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	(&Proxy{}).handleProxy(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected HTTP 400, got %d", rec.Code)
	}

	for _, entry := range logger.GetLogger().GetLogs() {
		if !strings.Contains(entry.Message, "Invalid request body") {
			continue
		}
		if !strings.Contains(entry.Message, "method=POST") {
			t.Fatalf("expected invalid-body log to include method, got %q", entry.Message)
		}
		if !strings.Contains(entry.Message, "path=/v1/responses") {
			t.Fatalf("expected invalid-body log to include path, got %q", entry.Message)
		}
		if strings.Contains(entry.Message, "token=hidden") {
			t.Fatalf("expected invalid-body log to omit query string, got %q", entry.Message)
		}
		return
	}

	t.Fatal("expected invalid-body log entry")
}

func TestForceStreamInPayloadAddsChatUsageOptions(t *testing.T) {
	raw := []byte(`{"model":"gpt-4.1","messages":[{"role":"user","content":"hi"}]}`)
	out := forceStreamInPayload(raw)

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if stream, ok := payload["stream"].(bool); !ok || !stream {
		t.Fatalf("expected stream=true, got %#v", payload["stream"])
	}
	streamOptions, ok := payload["stream_options"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected stream_options object, got %#v", payload["stream_options"])
	}
	if includeUsage, ok := streamOptions["include_usage"].(bool); !ok || !includeUsage {
		t.Fatalf("expected include_usage=true, got %#v", streamOptions["include_usage"])
	}
}

func TestShouldHandleAsStreamingResponseForCodexWithoutContentType(t *testing.T) {
	endpoint := config.Endpoint{
		Name:        "TokenPool",
		APIUrl:      "https://chatgpt.com/backend-api/codex",
		Transformer: "openai2",
	}
	if !shouldHandleAsStreamingResponse("", true, endpoint, "cx_chat_openai2") {
		t.Fatal("expected stream=true Codex response with empty content-type to be treated as streaming")
	}
	if shouldHandleAsStreamingResponse("", false, endpoint, "cx_chat_openai2") {
		t.Fatal("expected non-stream client request to not be treated as streaming when content-type is empty")
	}
	if !shouldHandleAsStreamingResponse("text/event-stream", false, endpoint, "cx_chat_openai2") {
		t.Fatal("expected text/event-stream content-type to be treated as streaming")
	}
}
