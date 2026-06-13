package proxy

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
)

func TestRebindListenerMovesActiveListenerToNewPort(t *testing.T) {
	cfg := config.DefaultConfig()
	firstPort := freeLocalTCPPort(t)
	secondPort := freeLocalTCPPort(t)
	cfg.UpdatePort(firstPort)
	cfg.UpdateListenMode(config.ListenModeLocal)

	p := New(cfg, noopStatsStorage{}, nil, "test-device")
	errCh := startProxyForRebindTest(t, p)
	oldURL := fmt.Sprintf("http://127.0.0.1:%d/health", firstPort)
	newURL := fmt.Sprintf("http://127.0.0.1:%d/health", secondPort)
	waitForHTTPStatus(t, oldURL, http.StatusOK)

	cfg.UpdatePort(secondPort)
	if err := p.RebindListener(); err != nil {
		t.Fatalf("RebindListener returned error: %v", err)
	}

	waitForHTTPStatus(t, newURL, http.StatusOK)
	waitForHTTPFailure(t, oldURL)
	assertProxyStillRunning(t, errCh)
}

func TestRebindListenerRestoresOldListenerWhenBindFails(t *testing.T) {
	cfg := config.DefaultConfig()
	oldPort := freeLocalTCPPort(t)
	cfg.UpdatePort(oldPort)
	cfg.UpdateListenMode(config.ListenModeLocal)

	p := New(cfg, noopStatsStorage{}, nil, "test-device")
	startProxyForRebindTest(t, p)
	oldURL := fmt.Sprintf("http://127.0.0.1:%d/health", oldPort)
	waitForHTTPStatus(t, oldURL, http.StatusOK)

	blockingListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to occupy target port: %v", err)
	}
	defer blockingListener.Close()
	targetPort := blockingListener.Addr().(*net.TCPAddr).Port

	cfg.UpdatePort(targetPort)
	if err := p.RebindListener(); err == nil {
		t.Fatal("RebindListener returned nil, want bind failure")
	}

	waitForHTTPStatus(t, oldURL, http.StatusOK)
}

func TestRebindListenerSwitchesListenModeWithoutStoppingStartWithMux(t *testing.T) {
	cfg := config.DefaultConfig()
	port := freeLocalTCPPort(t)
	cfg.UpdatePort(port)
	cfg.UpdateListenMode(config.ListenModeLocal)

	p := New(cfg, noopStatsStorage{}, nil, "test-device")
	errCh := startProxyForRebindTest(t, p)
	waitForHTTPStatus(t, fmt.Sprintf("http://127.0.0.1:%d/health", port), http.StatusOK)

	cfg.UpdateListenMode(config.ListenModeLAN)
	if err := p.RebindListener(); err != nil {
		t.Fatalf("RebindListener returned error: %v", err)
	}

	p.listenerMu.Lock()
	activeAddr := p.listenerAddr
	p.listenerMu.Unlock()
	if activeAddr != fmt.Sprintf("0.0.0.0:%d", port) {
		t.Fatalf("active listener addr = %q, want 0.0.0.0:%d", activeAddr, port)
	}
	assertProxyStillRunning(t, errCh)
}

func startProxyForRebindTest(t *testing.T, p *Proxy) <-chan error {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		errCh <- p.Start()
	}()
	t.Cleanup(func() {
		if err := p.Stop(); err != nil {
			t.Fatalf("Stop returned error: %v", err)
		}
	})
	return errCh
}

func freeLocalTCPPort(t *testing.T) int {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to allocate local TCP port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForHTTPStatus(t *testing.T, url string, want int) {
	t.Helper()

	client := rebindTestHTTPClient()
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

func waitForHTTPFailure(t *testing.T, url string) {
	t.Helper()

	client := rebindTestHTTPClient()
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

func assertProxyStillRunning(t *testing.T, errCh <-chan error) {
	t.Helper()

	select {
	case err := <-errCh:
		t.Fatalf("Start returned during hot rebind: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
}

func rebindTestHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 200 * time.Millisecond,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}
}
