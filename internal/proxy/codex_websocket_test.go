package proxy

import (
	"encoding/json"
	"testing"
)

func TestCodexWebSocketURL(t *testing.T) {
	tests := []struct {
		name    string
		rawURL  string
		want    string
		wantErr bool
	}{
		{
			name:   "secure official endpoint",
			rawURL: "https://chatgpt.com/backend-api/codex/responses",
			want:   "wss://chatgpt.com/backend-api/codex/responses",
		},
		{
			name:   "local test endpoint",
			rawURL: "http://127.0.0.1:8080/backend-api/codex/responses",
			want:   "ws://127.0.0.1:8080/backend-api/codex/responses",
		},
		{
			name:    "unsupported scheme",
			rawURL:  "ftp://chatgpt.com/backend-api/codex/responses",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := codexWebSocketURL(tt.rawURL)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q, got URL %q", tt.rawURL, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("codexWebSocketURL failed: %v", err)
			}
			if got != tt.want {
				t.Fatalf("codexWebSocketURL(%q) = %q, want %q", tt.rawURL, got, tt.want)
			}
		})
	}
}

func TestBuildCodexWebSocketFrameAddsResponseCreateType(t *testing.T) {
	raw := []byte(`{"model":"gpt-5.5","input":[{"role":"user","content":"hi"}],"tools":[{"type":"custom","name":"exec"}],"prompt_cache_key":"trace-1"}`)

	frame, err := buildCodexWebSocketFrame(raw)
	if err != nil {
		t.Fatalf("buildCodexWebSocketFrame failed: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(frame, &payload); err != nil {
		t.Fatalf("unmarshal frame failed: %v", err)
	}
	if got := payload["type"]; got != "response.create" {
		t.Fatalf("type = %#v, want response.create", got)
	}
	if got := payload["model"]; got != "gpt-5.5" {
		t.Fatalf("model = %#v, want gpt-5.5", got)
	}
	if got := payload["prompt_cache_key"]; got != "trace-1" {
		t.Fatalf("prompt_cache_key = %#v, want trace-1", got)
	}
	if tools, ok := payload["tools"].([]interface{}); !ok || len(tools) != 1 {
		t.Fatalf("expected tools to be preserved, got %#v", payload["tools"])
	}
	if input, ok := payload["input"].([]interface{}); !ok || len(input) != 1 {
		t.Fatalf("expected input to be preserved, got %#v", payload["input"])
	}
}

func TestBuildCodexWebSocketFrameRejectsInvalidPayload(t *testing.T) {
	if _, err := buildCodexWebSocketFrame([]byte(`not-json`)); err == nil {
		t.Fatal("expected invalid payload error")
	}
}
