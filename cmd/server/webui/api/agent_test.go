package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/service"
)

func TestAgentAPIRejectsEmptyTask(t *testing.T) {
	cfg := config.DefaultConfig()
	handler := &Handler{
		config: cfg,
		agent:  service.NewAgentServiceWithOptions(cfg, nil, nil, service.AgentServiceOptions{}),
	}

	req := httptest.NewRequest(http.MethodPost, "/api/agent/run", bytes.NewReader([]byte(`{"task":""}`)))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"no_task"`) {
		t.Fatalf("unexpected response code=%d body=%s", rec.Code, rec.Body.String())
	}
}
