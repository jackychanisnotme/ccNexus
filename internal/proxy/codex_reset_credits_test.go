package proxy

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
	"github.com/lich0821/ccNexus/internal/storage"
)

func TestFetchCodexResetCreditsParsesSnapshotAndUsesCredentialHeaders(t *testing.T) {
	store, credential := newCodexResetCreditTestStore(t)
	endpoint := config.Endpoint{Name: "Codex", APIUrl: "https://chatgpt.com/backend-api", AuthMode: config.AuthModeCodexTokenPool}
	client := &http.Client{Transport: codexRefreshRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodGet || req.URL.String() != "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
		}
		if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("authorization header = %q", got)
		}
		if got := req.Header.Get("ChatGPT-Account-Id"); got != "acct-123" {
			t.Fatalf("account header = %q", got)
		}
		if got := req.Header.Get("OpenAI-Beta"); got != "codex-1" {
			t.Fatalf("OpenAI-Beta header = %q", got)
		}
		return jsonResponse(http.StatusOK, `{
			"credits": [
				{"id":"available-1","status":"available","type":"rate_limit","granted_at":1780000000,"expires_at":1790000000},
				{"id":"used-1","status":"redeemed","redeemed_at":1780000100},
				{"id":"expired-1","status":"expired","expires_at":1}
			]
		}`), nil
	})}
	p := newCodexRefreshTestProxy(store, client)

	snapshot, err := p.FetchCodexResetCredits(endpoint, credential.ID)
	if err != nil {
		t.Fatalf("fetch reset credits: %v", err)
	}
	if snapshot.AvailableCount != 1 {
		t.Fatalf("available count = %d, want 1: %#v", snapshot.AvailableCount, snapshot)
	}
	if snapshot.NextExpiresAt == nil || *snapshot.NextExpiresAt != 1790000000 {
		t.Fatalf("next expiry not parsed: %#v", snapshot.NextExpiresAt)
	}
	if len(snapshot.Credits) != 3 || snapshot.Credits[0].ID != "available-1" || snapshot.Credits[1].Status != "redeemed" {
		t.Fatalf("credits not preserved with statuses: %#v", snapshot.Credits)
	}
}

func TestConsumeCodexResetCreditPostsRedeemRequestID(t *testing.T) {
	store, credential := newCodexResetCreditTestStore(t)
	endpoint := config.Endpoint{Name: "Codex", APIUrl: "https://chatgpt.com/backend-api", AuthMode: config.AuthModeCodexTokenPool}
	client := &http.Client{Transport: codexRefreshRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPost || req.URL.String() != "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits/consume" {
			t.Fatalf("unexpected request: %s %s", req.Method, req.URL.String())
		}
		if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Fatalf("authorization header = %q", got)
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatalf("read request body: %v", err)
		}
		if !strings.Contains(string(body), `"redeem_request_id"`) {
			t.Fatalf("missing redeem_request_id in body: %s", body)
		}
		return jsonResponse(http.StatusOK, `{}`), nil
	})}
	p := newCodexRefreshTestProxy(store, client)

	result, err := p.ConsumeCodexResetCredit(endpoint, credential.ID)
	if err != nil {
		t.Fatalf("consume reset credit: %v", err)
	}
	if !result.Consumed || result.CredentialID != credential.ID {
		t.Fatalf("unexpected consume result: %#v", result)
	}
}

func TestFetchCodexResetCreditsRefreshesTokenAfterUnauthorized(t *testing.T) {
	store, credential := newCodexResetCreditTestStore(t)
	endpoint := config.Endpoint{Name: "Codex", APIUrl: "https://chatgpt.com/backend-api", AuthMode: config.AuthModeCodexTokenPool}
	var resetCalls atomic.Int32
	client := &http.Client{Transport: codexRefreshRoundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.String() {
		case "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits":
			if resetCalls.Add(1) == 1 {
				return jsonResponse(http.StatusUnauthorized, `{"error":{"code":"unauthorized"}}`), nil
			}
			if got := req.Header.Get("Authorization"); got != "Bearer refreshed-access" {
				t.Fatalf("retry authorization header = %q", got)
			}
			return jsonResponse(http.StatusOK, `{"available_count":1,"credits":[{"id":"available-1","status":"available"}]}`), nil
		case codexOAuthTokenURL:
			return jsonResponse(http.StatusOK, `{"access_token":"refreshed-access","refresh_token":"refreshed-refresh","expires_in":3600}`), nil
		default:
			t.Fatalf("unexpected request URL: %s", req.URL.String())
			return nil, nil
		}
	})}
	p := newCodexRefreshTestProxy(store, client)

	snapshot, err := p.FetchCodexResetCredits(endpoint, credential.ID)
	if err != nil {
		t.Fatalf("fetch reset credits after refresh: %v", err)
	}
	if snapshot.AvailableCount != 1 || resetCalls.Load() != 2 {
		t.Fatalf("unexpected snapshot/calls: snapshot=%#v calls=%d", snapshot, resetCalls.Load())
	}
	updated, err := store.GetCredentialByID(credential.ID)
	if err != nil {
		t.Fatalf("load updated credential: %v", err)
	}
	if updated.AccessToken != "refreshed-access" {
		t.Fatalf("credential access token was not refreshed: %#v", updated)
	}
}

func TestCodexResetCreditsRejectInvalidEndpointAndCredential(t *testing.T) {
	store, credential := newCodexResetCreditTestStore(t)
	p := newCodexRefreshTestProxy(store, &http.Client{Transport: codexRefreshRoundTripFunc(func(*http.Request) (*http.Response, error) {
		t.Fatal("unexpected HTTP request")
		return nil, nil
	})})

	if _, err := p.FetchCodexResetCredits(config.Endpoint{Name: "Codex", AuthMode: config.AuthModeAPIKey}, credential.ID); err == nil {
		t.Fatal("expected non-codex token pool endpoint to fail")
	}
	if _, err := p.ConsumeCodexResetCredit(config.Endpoint{Name: "Codex", AuthMode: config.AuthModeCodexTokenPool}, 9999); err == nil {
		t.Fatal("expected unknown credential to fail")
	}
}

func newCodexResetCreditTestStore(t *testing.T) (*storage.SQLiteStorage, storage.EndpointCredential) {
	t.Helper()
	store, err := storage.NewSQLiteStorage(filepath.Join(t.TempDir(), "ainexus.db"))
	if err != nil {
		t.Fatalf("open storage: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	credential := storage.EndpointCredential{
		EndpointName: "Codex",
		ProviderType: storage.ProviderTypeCodex,
		AccountID:    "acct-123",
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		Status:       "active",
		Enabled:      true,
	}
	if err := store.SaveEndpointCredential(&credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	return store, credential
}

func jsonResponse(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
