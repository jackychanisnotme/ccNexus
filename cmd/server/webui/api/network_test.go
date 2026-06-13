package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/proxy"
)

func TestNetworkAPIGetAndUpdateListenMode(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	port := freeAPILocalTCPPort(t)
	cfg.UpdatePort(port)
	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	startAPIProxy(t, proxyInstance)
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
	wantListenAddr := fmt.Sprintf("127.0.0.1:%d", port)
	if data["listenAddr"] != wantListenAddr {
		t.Fatalf("listenAddr = %#v, want %s", data["listenAddr"], wantListenAddr)
	}
	wantLocalURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	if data["localURL"] != wantLocalURL {
		t.Fatalf("localURL = %#v, want %s", data["localURL"], wantLocalURL)
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
	if got := cfg.GetPort(); got != port {
		t.Fatalf("cfg port = %d, want %d", got, port)
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

func TestNetworkAPIPutRebindsImmediatelyWithoutRestartRequired(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	oldPort := freeAPILocalTCPPort(t)
	newPort := freeAPILocalTCPPort(t)
	cfg.UpdatePort(oldPort)
	cfg.UpdateListenMode(config.ListenModeLocal)

	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	startAPIProxy(t, proxyInstance)
	handler := NewHandler(cfg, proxyInstance, store)
	oldURL := fmt.Sprintf("http://127.0.0.1:%d/health", oldPort)
	newURL := fmt.Sprintf("http://127.0.0.1:%d/health", newPort)
	waitAPIHTTPStatus(t, oldURL, http.StatusOK)

	body := bytes.NewBufferString(fmt.Sprintf(`{"listenMode":"local","port":%d}`, newPort))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/network", body)
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/network status=%d body=%s", rec.Code, rec.Body.String())
	}

	var resp SuccessResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	data := resp.Data.(map[string]interface{})
	restartRequired, ok := data["restartRequired"].(bool)
	if !ok || restartRequired {
		t.Fatalf("restartRequired = %#v, want false", data["restartRequired"])
	}
	if got := cfg.GetPort(); got != newPort {
		t.Fatalf("cfg port = %d, want %d", got, newPort)
	}
	waitAPIHTTPStatus(t, newURL, http.StatusOK)
	waitAPIHTTPFailure(t, oldURL)
}

func TestNetworkAPIPutBindFailureRollsBackConfigAndListener(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	oldPort := freeAPILocalTCPPort(t)
	cfg.UpdatePort(oldPort)
	cfg.UpdateListenMode(config.ListenModeLocal)

	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	startAPIProxy(t, proxyInstance)
	handler := NewHandler(cfg, proxyInstance, store)
	oldURL := fmt.Sprintf("http://127.0.0.1:%d/health", oldPort)
	waitAPIHTTPStatus(t, oldURL, http.StatusOK)

	blockingListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to occupy target port: %v", err)
	}
	defer blockingListener.Close()
	targetPort := blockingListener.Addr().(*net.TCPAddr).Port

	body := bytes.NewBufferString(fmt.Sprintf(`{"listenMode":"local","port":%d}`, targetPort))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/network", body)
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("PUT /api/network bind failure status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := cfg.GetPort(); got != oldPort {
		t.Fatalf("cfg port after failed bind = %d, want %d", got, oldPort)
	}
	if got := cfg.GetListenMode(); got != config.ListenModeLocal {
		t.Fatalf("cfg listen mode after failed bind = %q, want %q", got, config.ListenModeLocal)
	}
	waitAPIHTTPStatus(t, oldURL, http.StatusOK)
}

func TestConfigPortAPIPutRebindsImmediately(t *testing.T) {
	store := newAPITestStorage(t)
	cfg := config.DefaultConfig()
	cfg.BasicAuthEnabled = false
	oldPort := freeAPILocalTCPPort(t)
	newPort := freeAPILocalTCPPort(t)
	cfg.UpdatePort(oldPort)
	cfg.UpdateListenMode(config.ListenModeLocal)

	proxyInstance := proxy.New(cfg, nil, store, "test-device")
	startAPIProxy(t, proxyInstance)
	handler := NewHandler(cfg, proxyInstance, store)
	oldURL := fmt.Sprintf("http://127.0.0.1:%d/health", oldPort)
	newURL := fmt.Sprintf("http://127.0.0.1:%d/health", newPort)
	waitAPIHTTPStatus(t, oldURL, http.StatusOK)

	body := bytes.NewBufferString(fmt.Sprintf(`{"port":%d}`, newPort))
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/api/config/port", body)
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT /api/config/port status=%d body=%s", rec.Code, rec.Body.String())
	}
	if got := cfg.GetPort(); got != newPort {
		t.Fatalf("cfg port = %d, want %d", got, newPort)
	}
	waitAPIHTTPStatus(t, newURL, http.StatusOK)
	waitAPIHTTPFailure(t, oldURL)
}

func freeAPILocalTCPPort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate local TCP port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func startAPIProxy(t *testing.T, proxyInstance *proxy.Proxy) <-chan error {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		errCh <- proxyInstance.Start()
	}()
	t.Cleanup(func() {
		if err := proxyInstance.Stop(); err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	})
	return errCh
}

func waitAPIHTTPStatus(t *testing.T, url string, want int) {
	t.Helper()

	client := apiRebindTestHTTPClient()
	deadline := time.Now().Add(2 * time.Second)
	var lastErr error
	var lastStatus int
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			lastStatus = resp.StatusCode
			_ = resp.Body.Close()
			if resp.StatusCode == want {
				return
			}
		} else {
			lastErr = err
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("GET %s did not return %d before timeout; last status=%d err=%v", url, want, lastStatus, lastErr)
}

func waitAPIHTTPFailure(t *testing.T, url string) {
	t.Helper()

	client := apiRebindTestHTTPClient()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err != nil {
			return
		}
		_ = resp.Body.Close()
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("GET %s kept succeeding after listener should have moved", url)
}

func apiRebindTestHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 200 * time.Millisecond,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
}
