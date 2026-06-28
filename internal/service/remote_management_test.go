package service

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

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
	outcome, err := executor.ExecuteRemoteCommand(onlinelicense.RemoteCommandPayload{
		CommandType: "endpoint.create",
		Payload:     payload,
	})
	if err != nil {
		t.Fatalf("execute endpoint.create: %v", err)
	}
	if outcome == nil || !outcome.ConfigChanged {
		t.Fatalf("endpoint.create outcome = %#v, want configChanged", outcome)
	}

	got := cfg.GetEndpoints()
	if len(got) != 2 || got[1].Name != "Remote Added" || got[1].APIUrl != "https://remote-added.example.com/v1" || got[1].APIKey != "remote-secret" {
		t.Fatalf("created endpoints = %#v", got)
	}
	if outcome.Snapshot == nil || len(outcome.Snapshot.Endpoints) != 2 || outcome.Snapshot.Endpoints[1].APIKeyMasked == "remote-secret" {
		t.Fatalf("snapshot after create = %#v", outcome.Snapshot)
	}
}

func TestRemoteManagementExecutorUpdatePreservesOmittedFields(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name:        "Existing",
		APIUrl:      "https://existing.example.com/v1",
		APIKey:      "existing-key",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai",
		Model:       "gpt-5",
		Thinking:    config.ThinkingHigh,
		ForceStream: true,
		ProxyURL:    "http://127.0.0.1:7890",
		Remark:      "keep remark",
	}})
	p := proxy.New(cfg, nil, nil, "remote-test-device")
	endpoints := NewEndpointService(cfg, p, nil)
	executor := NewRemoteManagementExecutor(cfg, nil, endpoints)

	outcome, err := executor.ExecuteRemoteCommand(onlinelicense.RemoteCommandPayload{
		CommandType: "endpoint.update",
		Payload:     json.RawMessage(`{"endpointName":"Existing","apiUrl":"https://updated.example.com/v1"}`),
	})
	if err != nil {
		t.Fatalf("execute endpoint.update: %v", err)
	}
	if outcome == nil || !outcome.ConfigChanged {
		t.Fatalf("endpoint.update outcome = %#v, want configChanged", outcome)
	}

	got := cfg.GetEndpoints()
	if len(got) != 1 {
		t.Fatalf("endpoints = %#v", got)
	}
	updated := got[0]
	if updated.APIUrl != "https://updated.example.com/v1" {
		t.Fatalf("apiUrl = %q", updated.APIUrl)
	}
	if updated.APIKey != "existing-key" || updated.Model != "gpt-5" || updated.Thinking != config.ThinkingHigh ||
		!updated.ForceStream || updated.ProxyURL != "http://127.0.0.1:7890" || updated.Remark != "keep remark" || !updated.Enabled {
		t.Fatalf("endpoint update did not preserve omitted fields: %#v", updated)
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

	outcome, err := executor.ExecuteRemoteCommand(onlinelicense.RemoteCommandPayload{
		CommandType: "endpoint.delete",
		Payload:     json.RawMessage(`{"endpointName":"Delete Me"}`),
	})
	if err != nil {
		t.Fatalf("execute endpoint.delete: %v", err)
	}
	if outcome == nil || !outcome.ConfigChanged {
		t.Fatalf("endpoint.delete outcome = %#v, want configChanged", outcome)
	}

	got := cfg.GetEndpoints()
	if len(got) != 1 || got[0].Name != "Keep" {
		t.Fatalf("remaining endpoints = %#v", got)
	}
	if outcome.Snapshot == nil || len(outcome.Snapshot.Endpoints) != 1 || outcome.Snapshot.Endpoints[0].Name != "Keep" {
		t.Fatalf("snapshot after delete = %#v", outcome.Snapshot)
	}
}

func TestRemoteManagementExecutorSecretRevealEncryptsAPIKey(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateEndpoints([]config.Endpoint{{
		Name:        "Existing",
		APIUrl:      "https://existing.example.com/v1",
		APIKey:      "api-secret-value",
		AuthMode:    config.AuthModeAPIKey,
		Enabled:     true,
		Transformer: "openai",
		Model:       "gpt-5",
	}})
	p := proxy.New(cfg, nil, nil, "remote-test-device")
	endpoints := NewEndpointService(cfg, p, nil)
	executor := NewRemoteManagementExecutor(cfg, nil, endpoints)
	adminKey := generateRevealAdminKey(t)
	expiresAt := time.Now().UTC().Add(time.Minute).Format(time.RFC3339)

	payload, err := json.Marshal(onlinelicense.RemoteSecretRevealPayload{
		EndpointName:   "Existing",
		Field:          "apiKey",
		AdminPublicKey: onlinelicense.EncodeRemoteRevealPublicKey(adminKey.PublicKey()),
		ExpiresAt:      expiresAt,
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	outcome, err := executor.ExecuteRemoteCommand(onlinelicense.RemoteCommandPayload{
		CommandType: "secret.reveal",
		Payload:     payload,
	})
	if err != nil {
		t.Fatalf("execute secret.reveal: %v", err)
	}
	if outcome == nil || outcome.SecretReveal == nil || !outcome.SecretRevealReady {
		t.Fatalf("secret reveal outcome = %#v", outcome)
	}
	wire := mustJSONString(t, outcome.SecretReveal)
	if strings.Contains(wire, "api-secret-value") {
		t.Fatalf("encrypted reveal result leaked plaintext: %s", wire)
	}
	plain, err := onlinelicense.DecryptRemoteSecretRevealResult(adminKey, *outcome.SecretReveal)
	if err != nil {
		t.Fatalf("decrypt reveal result: %v", err)
	}
	if plain.EndpointName != "Existing" || plain.Field != "apiKey" || plain.Value != "api-secret-value" {
		t.Fatalf("unexpected reveal plaintext: %#v", plain)
	}
}

func generateRevealAdminKey(t *testing.T) *ecdh.PrivateKey {
	t.Helper()
	key, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate reveal admin key: %v", err)
	}
	return key
}

func mustJSONString(t *testing.T, value interface{}) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(data)
}
