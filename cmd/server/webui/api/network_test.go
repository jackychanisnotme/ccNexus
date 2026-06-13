package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
)

func TestNetworkAPIGetAndUpdateListenMode(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	handler := NewHandler(cfg, proxyInstance, store)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/network", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/network status=%d body=%s", rec.Code, rec.Body.String())
	}

	var getResp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &getResp); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	data := getResp.Data.(map[string]interface{})
	if data["listenMode"] != config.ListenModeLocal {
		t.Fatalf("listenMode = %#v, want %q", data["listenMode"], config.ListenModeLocal)
	}
	if data["listenAddr"] != "127.0.0.1:3000" {
		t.Fatalf("listenAddr = %#v, want 127.0.0.1:3000", data["listenAddr"])
	}
	if data["localURL"] != "http://127.0.0.1:3000" {
		t.Fatalf("localURL = %#v, want http://127.0.0.1:3000", data["localURL"])
	}
	if _, ok := data["connections"].(map[string]interface{}); !ok {
		t.Fatalf("connections missing or wrong type: %#v", data["connections"])
	}

	body := bytes.NewBufferString(`{"listenMode":"lan"}`)
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPut, "/api/network", body)
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/network status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := cfg.GetListenMode(); got != config.ListenModeLAN {
		t.Fatalf("cfg listen mode = %q, want %q", got, config.ListenModeLAN)
	}
}

func TestNetworkAPIRejectsInvalidListenMode(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	handler := NewHandler(cfg, proxy.New(cfg, nil, store, "test-device"), store)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/network", bytes.NewBufferString(`{"listenMode":"public"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("PUT invalid listen mode status=%d body=%s", rec.Code, rec.Body.String())
	}
}
