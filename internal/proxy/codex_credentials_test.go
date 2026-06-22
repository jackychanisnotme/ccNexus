package proxy

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/storage"
)

type codexRefreshRoundTripFunc func(*http.Request) (*http.Response, error)

func (f codexRefreshRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRefreshCredentialCoalescesConcurrentRefreshes(t *testing.T) {
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	cred := storage.EndpointCredential{
		EndpointName: "Codex",
		ProviderType: storage.ProviderTypeCodex,
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
		Status:       "active",
		Enabled:      true,
	}
	if err := store.SaveEndpointCredential(&cred); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	var refreshCalls atomic.Int32
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	client := &http.Client{Transport: codexRefreshRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if got := req.URL.String(); got != codexOAuthTokenURL {
			t.Fatalf("refresh URL = %q, want %q", got, codexOAuthTokenURL)
		}
		if refreshCalls.Add(1) == 1 {
			close(requestStarted)
			<-releaseRequest
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":3600}`,
			)),
		}, nil
	})}

	p := newCodexRefreshTestProxy(store, client)
	endpoint := config.Endpoint{Name: "Codex", AuthMode: config.AuthModeCodexTokenPool}

	results := make(chan *storage.EndpointCredential, 2)
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			refreshed, refreshErr := p.RefreshCodexCredential(endpoint, cred.ID)
			results <- refreshed
			errs <- refreshErr
		}()
	}

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for refresh request")
	}
	close(releaseRequest)
	wg.Wait()
	close(results)
	close(errs)

	for refreshErr := range errs {
		if refreshErr != nil {
			t.Fatalf("concurrent refresh returned error: %v", refreshErr)
		}
	}
	for refreshed := range results {
		if refreshed == nil || refreshed.AccessToken != "new-access" || refreshed.RefreshToken != "new-refresh" {
			t.Fatalf("unexpected refreshed credential: %#v", refreshed)
		}
	}
	if got := refreshCalls.Load(); got != 1 {
		t.Fatalf("refresh request count = %d, want 1", got)
	}
}

func TestRefreshCredentialReuseInvalidatesCredentialAndRequiresLogin(t *testing.T) {
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	defer store.Close()

	cred := storage.EndpointCredential{
		EndpointName: "Codex",
		ProviderType: storage.ProviderTypeCodex,
		AccessToken:  "old-access",
		RefreshToken: "used-refresh",
		Status:       "active",
		Enabled:      true,
	}
	if err := store.SaveEndpointCredential(&cred); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	client := &http.Client{Transport: codexRefreshRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"error":{"message":"Your refresh token has already been used to generate a new access token. Please try signing in again.","type":"invalid_request_error","code":"refresh_token_reused"}}`,
			)),
		}, nil
	})}

	p := newCodexRefreshTestProxy(store, client)
	endpoint := config.Endpoint{Name: "Codex", AuthMode: config.AuthModeCodexTokenPool}

	_, err = p.RefreshCodexCredential(endpoint, cred.ID)
	if err == nil {
		t.Fatal("expected refresh token reuse error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "sign in again") {
		t.Fatalf("error should tell the user to sign in again, got %q", err)
	}

	updated, err := store.GetCredentialByID(cred.ID)
	if err != nil {
		t.Fatalf("load credential: %v", err)
	}
	if updated == nil || updated.Status != "invalid" {
		t.Fatalf("credential should be invalid after refresh token reuse, got %#v", updated)
	}
	if !strings.Contains(updated.LastError, "refresh_token_reused") {
		t.Fatalf("last error should retain the terminal error code, got %q", updated.LastError)
	}
}

func newCodexRefreshTestProxy(store *storage.SQLiteStorage, client *http.Client) *Proxy {
	cfg := config.DefaultConfig()
	p := New(cfg, &noopStatsStorage{}, store, "test-device")
	p.httpClient = client
	return p
}
