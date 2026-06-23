package proxy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lich0821/ccNexus/internal/config"
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

func TestOpenCodexWebSocketStreamBridgesCompletedResponseToSSE(t *testing.T) {
	requestErr := make(chan error, 1)
	largeDelta := strings.Repeat("x", 3*1024*1024)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			requestErr <- &testUnexpectedValueError{field: "Authorization", got: got, want: "Bearer test-token"}
			return
		}
		if got := r.Header.Get("Chatgpt-Account-Id"); got != "acct-1" {
			requestErr <- &testUnexpectedValueError{field: "Chatgpt-Account-Id", got: got, want: "acct-1"}
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			requestErr <- err
			return
		}
		defer conn.Close()

		_, frame, err := conn.ReadMessage()
		if err != nil {
			requestErr <- err
			return
		}
		var request map[string]interface{}
		if err := json.Unmarshal(frame, &request); err != nil {
			requestErr <- err
			return
		}
		if request["type"] != "response.create" {
			requestErr <- &testUnexpectedValueError{field: "type", got: request["type"], want: "response.create"}
			return
		}

		for _, event := range []map[string]interface{}{
			{"type": "response.created", "response": map[string]interface{}{"id": "resp-ws", "status": "in_progress"}},
			{"type": "response.custom_tool_call_input.delta", "delta": largeDelta},
			{"type": "response.completed", "response": map[string]interface{}{"id": "resp-ws", "status": "completed"}},
		} {
			if err := conn.WriteJSON(event); err != nil {
				requestErr <- err
				return
			}
		}
		requestErr <- nil
	}))
	defer upstream.Close()

	proxyReq, err := http.NewRequest(http.MethodPost, upstream.URL+"/backend-api/codex/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	proxyReq.Header.Set("Authorization", "Bearer test-token")
	proxyReq.Header.Set("Chatgpt-Account-Id", "acct-1")
	payload := []byte(`{"model":"gpt-5.5","stream":true,"input":[]}`)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := (&Proxy{}).openCodexWebSocketStream(ctx, proxyReq, config.Endpoint{Name: "Codex Pool"}, payload)
	if err != nil {
		t.Fatalf("openCodexWebSocketStream failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read bridged SSE failed: %v", err)
	}
	if err := <-requestErr; err != nil {
		t.Fatalf("upstream validation failed: %v", err)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
	bodyText := string(body)
	for _, want := range []string{"response.created", "response.custom_tool_call_input.delta", "response.completed", largeDelta} {
		if !strings.Contains(bodyText, want) {
			t.Fatalf("bridged SSE missing %q", want[:min(len(want), 80)])
		}
	}
}

func TestOpenCodexWebSocketStreamReturnsReadErrorWhenClosedBeforeCompleted(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
		_ = conn.WriteJSON(map[string]interface{}{
			"type":         "response.custom_tool_call_input.delta",
			"delta":        "partial",
			"item_id":      "tool-1",
			"call_id":      "call-1",
			"output_index": 0,
		})
	}))
	defer upstream.Close()

	proxyReq, err := http.NewRequest(http.MethodPost, upstream.URL+"/backend-api/codex/responses", nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resp, err := (&Proxy{}).openCodexWebSocketStream(ctx, proxyReq, config.Endpoint{Name: "Codex Pool"}, []byte(`{"model":"gpt-5.5","stream":true,"input":[]}`))
	if err != nil {
		t.Fatalf("openCodexWebSocketStream failed: %v", err)
	}
	defer resp.Body.Close()
	body, readErr := io.ReadAll(resp.Body)
	if readErr == nil {
		t.Fatalf("expected premature close error, got body %q", string(body))
	}
	if !strings.Contains(readErr.Error(), "before response.completed") {
		t.Fatalf("expected missing completion error, got %v", readErr)
	}
}

type testUnexpectedValueError struct {
	field string
	got   interface{}
	want  interface{}
}

func (e *testUnexpectedValueError) Error() string {
	return e.field + " mismatch: got " + stringifyTestValue(e.got) + ", want " + stringifyTestValue(e.want)
}

func stringifyTestValue(value interface{}) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
