package service

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestCodexEndpointTestUsesStableClientIdentity(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader(`{"model":"gpt-5.5","stream":true}`))
	credential := &storage.EndpointCredential{ProviderType: storage.ProviderTypeCodex}

	applyCodexCredentialHeadersForTest(req, credential, []byte(`{"model":"gpt-5.5","stream":true}`))

	if got := req.Header.Get("Version"); got != "0.144.1" {
		t.Fatalf("Version = %q, want %q", got, "0.144.1")
	}
	if got := req.Header.Get("User-Agent"); !strings.Contains(got, "codex_cli_rs/0.144.1") {
		t.Fatalf("User-Agent = %q, want stable Codex identity", got)
	}
}

func TestCodexLightEndpointTestUsesNewEnoughClientIdentity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if codexVersionOlderThan(r.Header.Get("Version"), "0.143.0") {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"detail":"The 'gpt-5.6-sol' model requires a newer version of Codex. Please upgrade to the latest app or CLI and try again."}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"resp-ok","output":[]}`))
	}))
	defer upstream.Close()

	service := NewEndpointService(config.DefaultConfig(), nil, nil)
	endpoint := config.Endpoint{
		Name:        "Codex Pool",
		APIUrl:      upstream.URL + "/backend-api/codex",
		AuthMode:    config.AuthModeCodexTokenPool,
		Transformer: config.CodexTokenPoolTransformer,
		Model:       "gpt-5.6-sol",
	}
	credential := &storage.EndpointCredential{
		ProviderType: storage.ProviderTypeCodex,
		AccessToken:  "test-token",
	}

	statusCode, err := service.testMinimalRequest(endpoint, endpoint.APIUrl, credential.AccessToken, endpoint.Transformer, endpoint.Model, credential)
	if err != nil {
		t.Fatalf("expected Codex light test to pass with new enough client identity, status=%d err=%v", statusCode, err)
	}
	if statusCode != http.StatusOK {
		t.Fatalf("statusCode = %d, want %d", statusCode, http.StatusOK)
	}
}

func codexVersionOlderThan(got, minimum string) bool {
	gotParts := strings.Split(strings.Split(strings.TrimSpace(got), "-")[0], ".")
	minimumParts := strings.Split(strings.Split(strings.TrimSpace(minimum), "-")[0], ".")
	for len(gotParts) < 3 {
		gotParts = append(gotParts, "0")
	}
	for len(minimumParts) < 3 {
		minimumParts = append(minimumParts, "0")
	}
	for i := 0; i < 3; i++ {
		gotPart, _ := strconv.Atoi(gotParts[i])
		minimumPart, _ := strconv.Atoi(minimumParts[i])
		if gotPart < minimumPart {
			return true
		}
		if gotPart > minimumPart {
			return false
		}
	}
	return false
}

func TestFetchModelsUsesProxyURL(t *testing.T) {
	cfg := config.DefaultConfig()
	service := NewEndpointService(cfg, nil, nil)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "direct upstream should not be used", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	var proxyHits int
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"service-model-through-proxy"}]}`))
	}))
	defer proxyServer.Close()

	raw := service.FetchModels(upstream.URL, "test-key", "openai", proxyServer.URL)
	if !strings.Contains(raw, "service-model-through-proxy") {
		t.Fatalf("expected proxy response models, got %s", raw)
	}
	if proxyHits == 0 {
		t.Fatal("expected fetch models request to go through proxy")
	}
}
