package service

import (
	"encoding/json"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/onlinelicense"
	"github.com/lich0821/ccNexus/internal/proxy"
)

func TestRemoteManagementExecutorCreatesEndpoint(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name:        "Existing",
		APIUrl:      "https://existing.example.com/v1",
		APIKey:      "existing-key",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai",
		Model:       "gpt-5",
	}})
	p := proxy.New(cfg, nil, nil, "remote-test-device")
	endpoints := NewEndpointService(cfg, p, nil)
	executor := NewRemoteManagementExecutor(cfg, nil, endpoints)

	payload := json.RawMessage(`{
		"name":"Remote Added",
		"apiUrl":"https://remote-added.example.com/v1",
		"apiKey":"remote-secret",
		"authMode":"api_key",
		"transformer":"openai",
		"model":"gpt-5",
		"enabled":true,
		"remark":"created remotely"
	}`)
	snapshot, _, err := executor.ExecuteRemoteCommand(onlinelicense.RemoteCommandPayload{
		CommandType: "endpoint.create",
		Payload:     payload,
	})
	if err != nil {
		t.Fatalf("execute endpoint.create: %v", err)
	}

	got := cfg.GetEndpoints()
	if len(got) != 2 || got[1].Name != "Remote Added" || got[1].APIUrl != "https://remote-added.example.com/v1" || got[1].APIKey != "remote-secret" {
		t.Fatalf("created endpoints = %#v", got)
	}
	if snapshot == nil || len(snapshot.Endpoints) != 2 || snapshot.Endpoints[1].APIKeyMasked == "remote-secret" {
		t.Fatalf("snapshot after create = %#v", snapshot)
	}
}

func TestRemoteManagementExecutorDeletesEndpoint(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{
		{
			Name:        "Keep",
			APIUrl:      "https://keep.example.com/v1",
			APIKey:      "keep-key",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "openai",
			Model:       "gpt-5",
		},
		{
			Name:        "Delete Me",
			APIUrl:      "https://delete.example.com/v1",
			APIKey:      "delete-key",
			AuthMode:    config.AuthModeAPIKey,
			Enabled:     true,
			Transformer: "openai",
			Model:       "gpt-5",
		},
	})
	p := proxy.New(cfg, nil, nil, "remote-test-device")
	endpoints := NewEndpointService(cfg, p, nil)
	executor := NewRemoteManagementExecutor(cfg, nil, endpoints)

	snapshot, _, err := executor.ExecuteRemoteCommand(onlinelicense.RemoteCommandPayload{
		CommandType: "endpoint.delete",
		Payload:     json.RawMessage(`{"endpointName":"Delete Me"}`),
	})
	if err != nil {
		t.Fatalf("execute endpoint.delete: %v", err)
	}

	got := cfg.GetEndpoints()
	if len(got) != 1 || got[0].Name != "Keep" {
		t.Fatalf("remaining endpoints = %#v", got)
	}
	if snapshot == nil || len(snapshot.Endpoints) != 1 || snapshot.Endpoints[0].Name != "Keep" {
		t.Fatalf("snapshot after delete = %#v", snapshot)
	}
}
