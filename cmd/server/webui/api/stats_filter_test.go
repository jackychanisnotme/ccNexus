package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestStatsAPISupportsEndpointAndIPFilters(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	proxyInstance := proxy.New(cfg, storage.NewStatsStorageAdapter(store), store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)
	today := time.Now().Format("2006-01-02")

	seedAPIStat(t, store, "Primary", today, "192.168.1.10", 1, 0, 10, 20)
	seedAPIStat(t, store, "Primary", today, "192.168.1.20", 2, 1, 30, 40)
	seedAPIStat(t, store, "Secondary", today, "10.0.0.5", 3, 0, 50, 60)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/stats/daily?endpoint=Primary&clientIp=192.168.1.20", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET filtered stats status=%d body=%s", rec.Code, rec.Body.String())
	}

	stats := decodeStatsPayload(t, rec)
	if stats["totalRequests"] != float64(2) || stats["totalErrors"] != float64(1) {
		t.Fatalf("filtered totals = %#v, want only Primary 192.168.1.20", stats)
	}
	endpoints := stats["endpoints"].(map[string]interface{})
	if len(endpoints) != 1 {
		t.Fatalf("filtered endpoints = %#v, want one endpoint", endpoints)
	}
	primary := endpoints["Primary"].(map[string]interface{})
	if primary["inputTokens"] != float64(30) || primary["outputTokens"] != float64(40) {
		t.Fatalf("filtered Primary stats = %#v, want second IP usage", primary)
	}
}

func TestStatsAPIFilterOptionsIncludeDeletedEndpointsAndClientIPs(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	cfg.UpdateEndpoints([]config.Endpoint{
		{Name: "Current", APIUrl: "https://api.example.com", APIKey: "key", AuthMode: config.AuthModeAPIKey, Enabled: true, Transformer: "openai", Model: "gpt-test"},
	})
	if err := cfg.SaveToStorage(storage.NewConfigStorageAdapter(store)); err != nil {
		t.Fatalf("save config: %v", err)
	}
	proxyInstance := proxy.New(cfg, storage.NewStatsStorageAdapter(store), store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)

	seedAPIStat(t, store, "DeletedEndpoint", "2026-06-13", "192.168.1.50", 1, 0, 1, 2)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/stats/filters", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET stats filters status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := resp.Data.(map[string]interface{})
	endpoints := data["endpoints"].([]interface{})
	if !apiEndpointOptionExists(endpoints, "Current", false) {
		t.Fatalf("expected current endpoint option, got %#v", endpoints)
	}
	if !apiEndpointOptionExists(endpoints, "DeletedEndpoint", true) {
		t.Fatalf("expected deleted endpoint option, got %#v", endpoints)
	}
	clientIPs := data["clientIps"].([]interface{})
	if len(clientIPs) != 1 || clientIPs[0] != "192.168.1.50" {
		t.Fatalf("client IP options = %#v, want [192.168.1.50]", clientIPs)
	}
}

func seedAPIStat(t *testing.T, store *storage.SQLiteStorage, endpointName, date, clientIP string, requests, errors, inputTokens, outputTokens int) {
	t.Helper()
	if err := store.RecordDailyStat(&storage.DailyStat{
		EndpointName: endpointName,
		Date:         date,
		Requests:     requests,
		Errors:       errors,
		InputTokens:  inputTokens,
		OutputTokens: outputTokens,
		DeviceID:     "test-device",
		ClientIP:     clientIP,
	}); err != nil {
		t.Fatalf("record stat: %v", err)
	}
}

func decodeStatsPayload(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := resp.Data.(map[string]interface{})
	stats := data["stats"].(map[string]interface{})
	return stats
}

func apiEndpointOptionExists(options []interface{}, name string, deleted bool) bool {
	for _, option := range options {
		asMap, ok := option.(map[string]interface{})
		if !ok {
			continue
		}
		if asMap["name"] == name && asMap["deleted"] == deleted {
			return true
		}
	}
	return false
}
