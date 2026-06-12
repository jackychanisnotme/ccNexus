package service

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
)

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
