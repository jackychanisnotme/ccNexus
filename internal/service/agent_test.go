package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
)

func TestAgentRunRejectsEmptyTask(t *testing.T) {
	svc := NewAgentServiceWithOptions(config.DefaultConfig(), nil, nil, AgentServiceOptions{})

	result := svc.Run(AgentRunRequest{Task: "   "})

	if result.Success || result.Error != "no_task" {
		t.Fatalf("expected no_task failure, got %#v", result)
	}
}

func TestAgentRunRejectsNoEnabledEndpoints(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "disabled", Enabled: false}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{})

	result := svc.Run(AgentRunRequest{Task: "hello"})

	if result.Success || result.Error != "no_enabled_endpoints" {
		t.Fatalf("expected no_enabled_endpoints, got %#v", result)
	}
}

func TestAgentRunUsesResponsesThenReturnsAnswer(t *testing.T) {
	var requestedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if !strings.Contains(string(mustJSON(t, payload)), "Summarize endpoint status") {
			t.Fatalf("payload did not include task: %#v", payload)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":[{"content":[{"type":"output_text","text":"agent answer"}]}]}`))
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.UpdatePort(3000)
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "primary", Enabled: true}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		LocalBaseURL: server.URL,
		HTTPClient:   server.Client(),
	})

	result := svc.Run(AgentRunRequest{Task: "Summarize endpoint status"})

	if !result.Success || result.Answer != "agent answer" || requestedPath != "/v1/responses" {
		t.Fatalf("unexpected result %#v path=%s", result, requestedPath)
	}
	if result.CurrentEndpoint != "primary" {
		t.Fatalf("expected current endpoint primary, got %q", result.CurrentEndpoint)
	}
}

func TestAgentRunFallsBackToChatCompletions(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path == "/v1/responses" {
			http.Error(w, "unsupported", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"chat answer"}}]}`))
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{Name: "fallback", Enabled: true}})
	svc := NewAgentServiceWithOptions(cfg, nil, nil, AgentServiceOptions{
		LocalBaseURL: server.URL,
		HTTPClient:   server.Client(),
	})

	result := svc.Run(AgentRunRequest{Task: "hello"})

	if !result.Success || result.Answer != "chat answer" {
		t.Fatalf("unexpected result %#v", result)
	}
	if len(paths) != 2 || paths[0] != "/v1/responses" || paths[1] != "/v1/chat/completions" {
		t.Fatalf("expected responses then chat paths, got %#v", paths)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}
