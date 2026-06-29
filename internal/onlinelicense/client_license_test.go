package onlinelicense

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type memoryConfigStore struct {
	values map[string]string
}

func newMemoryConfigStore() *memoryConfigStore {
	return &memoryConfigStore{values: make(map[string]string)}
}

func (s *memoryConfigStore) GetConfig(key string) (string, error) {
	return s.values[key], nil
}

func (s *memoryConfigStore) SetConfig(key, value string) error {
	s.values[key] = value
	return nil
}

type testRemoteExecutor struct {
	snapshotCalls int
	executed      []RemoteCommandPayload
}

func (e *testRemoteExecutor) Snapshot() (RemoteSnapshot, error) {
	e.snapshotCalls++
	return RemoteSnapshot{Endpoints: []RemoteEndpointSnapshot{{Name: "Primary"}}}, nil
}

func (e *testRemoteExecutor) ExecuteRemoteCommand(command RemoteCommandPayload) (*RemoteExecutionOutcome, error) {
	e.executed = append(e.executed, command)
	return &RemoteExecutionOutcome{Message: "ok"}, nil
}

func TestClientActivateRequiresVerifierBeforeContactingServer(t *testing.T) {
	store := newMemoryConfigStore()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		writeJSONSuccess(w, ActivationResult{Licensed: true, Ticket: "ticket"})
	}))
	t.Cleanup(server.Close)

	client := NewClientService(store, "device-a", ClientOptions{
		ServerURL: server.URL,
		Now: func() time.Time {
			return time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC)
		},
	})

	if _, err := client.Activate("CCNX-ONL-EXAMPLE", time.Time{}); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("Activate error = %v, want ErrInvalidTicket", err)
	}
	if requests != 0 {
		t.Fatalf("activation contacted server %d times without a verifier", requests)
	}
	if state := store.values[clientStateConfigKey]; state != "" {
		t.Fatalf("license state was saved without a verifier: %s", state)
	}
}

func TestClientDefaultLicenseServerURLsUseDomainThenIP(t *testing.T) {
	t.Setenv("CCNEXUS_LICENSE_SERVER_URL", "")
	t.Setenv("CCNEXUS_LICENSE_SERVER_URLS", "")

	client := NewClientService(newMemoryConfigStore(), "device-a", ClientOptions{})

	want := []string{"https://license.wenche.xyz", "http://207.57.134.147:24220"}
	if !equalStrings(client.serverURLs, want) {
		t.Fatalf("serverURLs = %#v, want %#v", client.serverURLs, want)
	}
	if client.serverURL != want[0] {
		t.Fatalf("serverURL = %q, want %q", client.serverURL, want[0])
	}
}

func TestClientLicenseServerURLIPAddsDomainBackup(t *testing.T) {
	t.Setenv("CCNEXUS_LICENSE_SERVER_URL", "http://207.57.134.147:24220")
	t.Setenv("CCNEXUS_LICENSE_SERVER_URLS", "")

	client := NewClientService(newMemoryConfigStore(), "device-a", ClientOptions{})

	want := []string{"http://207.57.134.147:24220", "https://license.wenche.xyz"}
	if !equalStrings(client.serverURLs, want) {
		t.Fatalf("serverURLs = %#v, want %#v", client.serverURLs, want)
	}
}

func TestClientLicenseServerURLSDeduplicatesAndPreservesOrder(t *testing.T) {
	t.Setenv("CCNEXUS_LICENSE_SERVER_URL", "")
	t.Setenv("CCNEXUS_LICENSE_SERVER_URLS", " http://a.example.test , https://b.example.test/ , http://a.example.test/")

	client := NewClientService(newMemoryConfigStore(), "device-a", ClientOptions{})

	want := []string{"http://a.example.test", "https://b.example.test"}
	if !equalStrings(client.serverURLs, want) {
		t.Fatalf("serverURLs = %#v, want %#v", client.serverURLs, want)
	}
}

func TestConfiguredClientServiceLicenseServerURLSOverridesSingleURL(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate public key: %v", err)
	}
	previousPublicKey := AppPublicKey
	AppPublicKey = base64.StdEncoding.EncodeToString(publicKey)
	t.Cleanup(func() {
		AppPublicKey = previousPublicKey
	})
	t.Setenv("CCNEXUS_LICENSE_SERVER_URL", "http://legacy.example.test")
	t.Setenv("CCNEXUS_LICENSE_SERVER_URLS", "https://primary.example.test,http://backup.example.test")

	client, err := NewConfiguredClientService(newMemoryConfigStore(), "device-a", "test")
	if err != nil {
		t.Fatalf("NewConfiguredClientService error: %v", err)
	}

	want := []string{"https://primary.example.test", "http://backup.example.test"}
	if !equalStrings(client.serverURLs, want) {
		t.Fatalf("serverURLs = %#v, want %#v", client.serverURLs, want)
	}
}

func TestClientPostJSONFallsBackAfterRetryableStatus(t *testing.T) {
	t.Setenv("CCNEXUS_LICENSE_SERVER_URL", "")
	firstHits := 0
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHits++
		writeJSONError(w, http.StatusServiceUnavailable, "temporary unavailable")
	}))
	t.Cleanup(first.Close)
	secondHits := 0
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits++
		writeJSONSuccess(w, map[string]string{"ok": "yes"})
	}))
	t.Cleanup(second.Close)
	t.Setenv("CCNEXUS_LICENSE_SERVER_URLS", first.URL+","+second.URL)

	client := NewClientService(newMemoryConfigStore(), "device-a", ClientOptions{})
	var out map[string]string
	if err := client.postJSON("/test", map[string]string{"hello": "world"}, &out); err != nil {
		t.Fatalf("postJSON fallback error: %v", err)
	}
	if firstHits != 1 || secondHits != 1 {
		t.Fatalf("hits first=%d second=%d, want 1/1", firstHits, secondHits)
	}
	if out["ok"] != "yes" {
		t.Fatalf("response = %#v", out)
	}
	if client.serverURL != second.URL {
		t.Fatalf("current serverURL = %q, want %q", client.serverURL, second.URL)
	}
}

func TestClientPostJSONFallsBackAfterTimeout(t *testing.T) {
	t.Setenv("CCNEXUS_LICENSE_SERVER_URL", "")
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(120 * time.Millisecond)
		writeJSONSuccess(w, map[string]string{"slow": "yes"})
	}))
	t.Cleanup(first.Close)
	secondHits := 0
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits++
		writeJSONSuccess(w, map[string]string{"ok": "yes"})
	}))
	t.Cleanup(second.Close)
	t.Setenv("CCNEXUS_LICENSE_SERVER_URLS", first.URL+","+second.URL)

	client := NewClientService(newMemoryConfigStore(), "device-a", ClientOptions{
		Client: &http.Client{Timeout: 20 * time.Millisecond},
	})
	var out map[string]string
	if err := client.postJSON("/test", map[string]string{"hello": "world"}, &out); err != nil {
		t.Fatalf("postJSON timeout fallback error: %v", err)
	}
	if secondHits != 1 || out["ok"] != "yes" {
		t.Fatalf("second hits=%d response=%#v", secondHits, out)
	}
}

func TestClientPostJSONDoesNotFallbackOnBusinessError(t *testing.T) {
	t.Setenv("CCNEXUS_LICENSE_SERVER_URL", "")
	firstHits := 0
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		firstHits++
		writeJSONError(w, http.StatusBadRequest, "invalid license ticket")
	}))
	t.Cleanup(first.Close)
	secondHits := 0
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondHits++
		writeJSONSuccess(w, map[string]string{"ok": "yes"})
	}))
	t.Cleanup(second.Close)
	t.Setenv("CCNEXUS_LICENSE_SERVER_URLS", first.URL+","+second.URL)

	client := NewClientService(newMemoryConfigStore(), "device-a", ClientOptions{})
	var out map[string]string
	err := client.postJSON("/test", map[string]string{"hello": "world"}, &out)
	if err == nil || !strings.Contains(err.Error(), "invalid license ticket") {
		t.Fatalf("postJSON error = %v, want invalid license ticket", err)
	}
	if firstHits != 1 || secondHits != 0 {
		t.Fatalf("hits first=%d second=%d, want 1/0", firstHits, secondHits)
	}
}

func TestClientActivateRejectsTicketSignedByDifferentKey(t *testing.T) {
	now := time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC)
	_, serverPrivateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	service := NewService(newTestStore(t), serverPrivateKey, Options{Now: func() time.Time { return now }})
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	server := httptest.NewServer(NewHTTPHandler(service, AdminConfig{}))
	t.Cleanup(server.Close)

	wrongPublicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate client key: %v", err)
	}
	store := newMemoryConfigStore()
	client := NewClientService(store, "device-a", ClientOptions{
		ServerURL: server.URL,
		PublicKey: wrongPublicKey,
		Now:       func() time.Time { return now },
	})

	if _, err := client.Activate(card.CardKey, time.Time{}); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("Activate error = %v, want ErrInvalidTicket", err)
	}
	if state := store.values[clientStateConfigKey]; state != "" {
		t.Fatalf("license state was saved with an unverifiable ticket: %s", state)
	}
}

func TestClientStatusPersistsPlanAfterActivation(t *testing.T) {
	now := time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	service := NewService(newTestStore(t), privateKey, Options{Now: func() time.Time { return now }})
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	server := httptest.NewServer(NewHTTPHandler(service, AdminConfig{}))
	t.Cleanup(server.Close)

	publicKey := privateKey.Public().(ed25519.PublicKey)
	store := newMemoryConfigStore()
	client := NewClientService(store, "device-a", ClientOptions{
		ServerURL: server.URL,
		PublicKey: publicKey,
		Now:       func() time.Time { return now },
	})

	if _, err := client.Activate(card.CardKey, time.Time{}); err != nil {
		t.Fatalf("Activate error = %v", err)
	}
	status, err := client.Status(time.Time{})
	if err != nil {
		t.Fatalf("Status error = %v", err)
	}
	raw, err := json.Marshal(status)
	if err != nil {
		t.Fatalf("marshal status: %v", err)
	}
	if !strings.Contains(string(raw), `"lastPlan":"monthly"`) {
		t.Fatalf("status JSON = %s, want lastPlan monthly", raw)
	}
}

func TestClientStatusUsesCachedTicketWithoutContactingServer(t *testing.T) {
	now := time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	service := NewService(newTestStore(t), privateKey, Options{Now: func() time.Time { return now }})
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	handler := NewHTTPHandler(service, AdminConfig{})
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	client := NewClientService(newMemoryConfigStore(), "device-a", ClientOptions{
		ServerURL: server.URL,
		PublicKey: privateKey.Public().(ed25519.PublicKey),
		Now:       func() time.Time { return now },
	})
	if _, err := client.Activate(card.CardKey, time.Time{}); err != nil {
		t.Fatalf("Activate error = %v", err)
	}
	requestsAfterActivate := requests

	status, err := client.Status(now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Status error = %v", err)
	}
	if !status.Licensed {
		t.Fatalf("Status licensed = false, want true: %#v", status)
	}
	if requests != requestsAfterActivate {
		t.Fatalf("Status contacted server: requests before=%d after=%d", requestsAfterActivate, requests)
	}
}

func TestClientActivateAcceptsLegacyTicketWithoutPlanField(t *testing.T) {
	now := time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/license/activate" {
			writeJSONError(w, 404, "not found")
			return
		}
		payload := struct {
			Product      string    `json:"product"`
			LicenseID    int64     `json:"licenseId"`
			ActivationID int64     `json:"activationId"`
			DeviceID     string    `json:"deviceId"`
			ExpiresAt    time.Time `json:"expiresAt"`
			NextCheckAt  time.Time `json:"nextCheckAt"`
			GraceUntil   time.Time `json:"graceUntil"`
			IssuedAt     time.Time `json:"issuedAt"`
		}{
			Product:      ProductCCNexusPro,
			LicenseID:    7,
			ActivationID: 8,
			DeviceID:     "device-a",
			ExpiresAt:    now.AddDate(0, 0, 30),
			NextCheckAt:  now.Add(24 * time.Hour),
			GraceUntil:   now.AddDate(0, 0, 30),
			IssuedAt:     now,
		}
		canonical, err := json.Marshal(payload)
		if err != nil {
			panic(fmt.Errorf("marshal legacy payload: %w", err))
		}
		sig := ed25519.Sign(privateKey, canonical)
		ticketPayload := map[string]any{
			"product":      payload.Product,
			"licenseId":    payload.LicenseID,
			"activationId": payload.ActivationID,
			"deviceId":     payload.DeviceID,
			"expiresAt":    payload.ExpiresAt,
			"nextCheckAt":  payload.NextCheckAt,
			"graceUntil":   payload.GraceUntil,
			"issuedAt":     payload.IssuedAt,
		}
		legacyEnvelope := map[string]any{
			"payload":   ticketPayload,
			"signature": base64.RawURLEncoding.EncodeToString(sig),
		}
		raw, err := json.Marshal(legacyEnvelope)
		if err != nil {
			panic(fmt.Errorf("marshal legacy envelope: %w", err))
		}
		writeJSONSuccess(w, ActivationResult{
			Licensed:     true,
			LicenseID:    7,
			ActivationID: 8,
			DeviceID:     "device-a",
			Status:       ActivationStatusActive,
			ExpiresAt:    now.AddDate(0, 0, 30),
			NextCheckAt:  now.Add(24 * time.Hour),
			GraceUntil:   now.AddDate(0, 0, 30),
			Ticket:       base64.RawURLEncoding.EncodeToString(raw),
			Message:      "license is active",
		})
	}))
	t.Cleanup(server.Close)

	store := newMemoryConfigStore()
	client := NewClientService(store, "device-a", ClientOptions{
		ServerURL: server.URL,
		PublicKey: publicKey,
		Now:       func() time.Time { return now },
	})

	if _, err := client.Activate("CCNX-ONL-EXAMPLE", time.Time{}); err != nil {
		t.Fatalf("Activate error = %v, want legacy ticket to verify", err)
	}
}

func TestClientRefreshRecordsRemoteDisabledWithoutDroppingTicket(t *testing.T) {
	current := time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	service := NewService(store, privateKey, Options{Now: func() time.Time { return current }})
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanYearly, Count: 1, MaxDevices: 1})
	server := httptest.NewServer(NewHTTPHandler(service, AdminConfig{}))
	t.Cleanup(server.Close)

	configStore := newMemoryConfigStore()
	client := NewClientService(configStore, "device-a", ClientOptions{
		ServerURL: server.URL,
		PublicKey: privateKey.Public().(ed25519.PublicKey),
		Now:       func() time.Time { return current },
	})
	activated, err := client.Activate(card.CardKey, time.Time{})
	if err != nil {
		t.Fatalf("Activate error: %v", err)
	}
	before := configStore.values[clientStateConfigKey]
	if !strings.Contains(before, activated.Ticket) {
		t.Fatalf("saved state does not contain activation ticket: %s", before)
	}
	if err := service.DisableActivation(activated.ActivationID); err != nil {
		t.Fatalf("disable activation: %v", err)
	}

	current = current.Add(5 * time.Minute)
	refreshed, err := client.Refresh(time.Time{})
	if err != nil {
		t.Fatalf("Refresh error = %v, want remote disabled status", err)
	}
	if refreshed.Licensed {
		t.Fatalf("Refresh licensed = true, want false for disabled activation")
	}
	if refreshed.Status != ActivationStatusDisabled {
		t.Fatalf("Refresh status = %q, want %q", refreshed.Status, ActivationStatusDisabled)
	}
	after := configStore.values[clientStateConfigKey]
	if !strings.Contains(after, activated.Ticket) {
		t.Fatalf("disabled refresh dropped cached ticket: %s", after)
	}
	if !strings.Contains(after, `"status":"disabled"`) {
		t.Fatalf("disabled refresh did not persist remote status: %s", after)
	}

	status, err := client.Status(time.Time{})
	if err != nil {
		t.Fatalf("Status error: %v", err)
	}
	if status.Licensed {
		t.Fatalf("Status licensed = true, want false after remote disabled status")
	}
	if status.Message != ErrActivationBlocked.Error() {
		t.Fatalf("Status message = %q, want %q", status.Message, ErrActivationBlocked.Error())
	}
}

func TestPollRemoteCommandsOnlyDoesNotSubmitSnapshotWhenIdle(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	service := NewService(newTestStore(t), privateKey, Options{Now: func() time.Time { return now }})
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	resultPosts := 0
	handler := NewHTTPHandler(service, AdminConfig{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/license/remote/result" {
			resultPosts++
		}
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)

	store := newMemoryConfigStore()
	client := NewClientService(store, "device-a", ClientOptions{
		ServerURL: server.URL,
		PublicKey: privateKey.Public().(ed25519.PublicKey),
		Now:       func() time.Time { return now },
	})
	executor := &testRemoteExecutor{}
	client.SetRemoteExecutor(executor)
	if _, err := client.Activate(card.CardKey, time.Time{}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	outcome, err := client.PollRemoteCommandsOnly()
	if err != nil {
		t.Fatalf("light poll: %v", err)
	}
	if outcome.CommandCount != 0 {
		t.Fatalf("command count = %d, want 0", outcome.CommandCount)
	}
	if resultPosts != 0 {
		t.Fatalf("light idle poll submitted %d result posts, want 0", resultPosts)
	}
	if executor.snapshotCalls != 0 {
		t.Fatalf("light idle poll took %d snapshots, want 0", executor.snapshotCalls)
	}
}

func TestPollRemoteCommandsOnlyExecutesDeliveredCommand(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	store := newTestStore(t)
	service := NewService(store, privateKey, Options{Now: func() time.Time { return now }})
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	server := httptest.NewServer(NewHTTPHandler(service, AdminConfig{}))
	t.Cleanup(server.Close)

	deviceKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate device key: %v", err)
	}
	configStore := newMemoryConfigStore()
	configStore.values[clientRemoteKeyConfigKey] = base64.RawURLEncoding.EncodeToString(deviceKey.Bytes())
	client := NewClientService(configStore, "device-a", ClientOptions{
		ServerURL: server.URL,
		PublicKey: privateKey.Public().(ed25519.PublicKey),
		Now:       func() time.Time { return now },
	})
	executor := &testRemoteExecutor{}
	client.SetRemoteExecutor(executor)
	if _, err := client.Activate(card.CardKey, time.Time{}); err != nil {
		t.Fatalf("activate: %v", err)
	}

	plain, err := json.Marshal(RemoteCommandPayload{CommandType: "endpoint.update", Payload: json.RawMessage(`{"endpointName":"Primary"}`)})
	if err != nil {
		t.Fatalf("marshal command: %v", err)
	}
	envelope, err := EncryptRemoteEnvelope(EncodeRemotePublicKey(deviceKey.PublicKey()), plain)
	if err != nil {
		t.Fatalf("encrypt command: %v", err)
	}
	if err := store.CreateRemoteCommand(&RemoteCommandRecord{
		DeviceID:    "device-a",
		CommandType: "endpoint.update",
		Status:      "queued",
		Envelope:    envelope,
		CreatedAt:   now,
		UpdatedAt:   now,
	}, 0); err != nil {
		t.Fatalf("create command: %v", err)
	}

	outcome, err := client.PollRemoteCommandsOnly()
	if err != nil {
		t.Fatalf("light poll: %v", err)
	}
	if outcome.CommandCount != 1 {
		t.Fatalf("command count = %d, want 1", outcome.CommandCount)
	}
	if len(executor.executed) != 1 || executor.executed[0].CommandType != "endpoint.update" {
		t.Fatalf("executed commands = %#v", executor.executed)
	}
	commands, err := store.ListRemoteCommands("device-a", 10)
	if err != nil {
		t.Fatalf("list commands: %v", err)
	}
	if len(commands) != 1 || commands[0].Status != "applied" {
		t.Fatalf("command status = %#v, want applied", commands)
	}
	if bytes.Contains([]byte(mustJSONClient(t, commands[0])), []byte("Primary")) {
		t.Fatalf("command result leaked plaintext payload: %s", mustJSONClient(t, commands[0]))
	}
}

func mustJSONClient(t *testing.T, v interface{}) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(data)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestClientRefreshPreservesCachedTicketWhenServerUnavailable(t *testing.T) {
	current := time.Date(2026, 6, 22, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	service := NewService(store, privateKey, Options{Now: func() time.Time { return current }})
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	server := httptest.NewServer(NewHTTPHandler(service, AdminConfig{}))

	configStore := newMemoryConfigStore()
	client := NewClientService(configStore, "device-a", ClientOptions{
		ServerURL: server.URL,
		PublicKey: privateKey.Public().(ed25519.PublicKey),
		Now:       func() time.Time { return current },
	})
	if _, err := client.Activate(card.CardKey, time.Time{}); err != nil {
		t.Fatalf("Activate error: %v", err)
	}
	before := configStore.values[clientStateConfigKey]
	server.Close()

	if _, err := client.Refresh(time.Time{}); err == nil {
		t.Fatalf("Refresh succeeded with server unavailable")
	}
	after := configStore.values[clientStateConfigKey]
	if after != before {
		t.Fatalf("unavailable server changed cached state:\nbefore=%s\nafter=%s", before, after)
	}
}
