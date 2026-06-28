package onlinelicense

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteStateMigrationAndSnapshotAreBackwardCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "license.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close initial store: %v", err)
	}

	store, err = NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen migrated store: %v", err)
	}
	defer store.Close()

	now := time.Date(2026, 6, 28, 9, 0, 0, 0, time.UTC)
	state := RemoteDeviceState{
		DeviceID:           "device-a",
		Supported:          true,
		Enabled:            true,
		ClientVersion:      "6.4.0",
		Capabilities:       []string{"endpoints:view", "endpoints:write"},
		DevicePublicKey:    "device-public-key",
		LastHeartbeatAt:    now,
		LastActivationID:   11,
		OwnerAccountID:     7,
		OwnerUsername:      "owner",
		LastSnapshotStatus: "ok",
	}
	if err := store.UpsertRemoteDeviceState(&state, now); err != nil {
		t.Fatalf("upsert remote state: %v", err)
	}
	if err := store.UpsertRemoteSnapshot("device-a", RemoteSnapshot{
		Endpoints: []RemoteEndpointSnapshot{{
			Name:         "OpenAI",
			APIUrl:       "https://api.openai.example/v1",
			APIKeyMasked: "sk-***abcd",
			AuthMode:     "api_key",
			Enabled:      true,
			Stats:        RemoteUsageStats{Requests: 3, InputTokens: 10, OutputTokens: 20},
		}},
		TokenPools: []RemoteTokenPoolSnapshot{{
			EndpointName: "Codex",
			Credentials:  []RemoteCredentialSnapshot{{ID: 9, EmailMasked: "u***@example.com", Usage: RemoteUsageStats{Requests: 1}}},
		}},
	}, now); err != nil {
		t.Fatalf("upsert remote snapshot: %v", err)
	}

	got, err := store.GetRemoteDevice("device-a")
	if err != nil {
		t.Fatalf("get remote device: %v", err)
	}
	if !got.Supported || !got.Enabled || got.ClientVersion != "6.4.0" || len(got.Snapshot.Endpoints) != 1 {
		t.Fatalf("unexpected remote device: %#v", got)
	}
	if strings.Contains(mustJSON(t, got), "sk-secret") {
		t.Fatalf("remote snapshot leaked plaintext secret: %s", mustJSON(t, got))
	}
}

func TestRemoteAdminAPIsEnforceScopeAndPermissions(t *testing.T) {
	handler := newTestHTTPHandler(t)
	rootCookie := loginAdmin(t, handler)
	reseller := createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username: "reseller",
		Password: "reseller-pass",
		Level:    AdminLevelReseller,
	})
	other := createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username: "other",
		Password: "other-pass",
		Level:    AdminLevelReseller,
	})

	resellerKey := generateHTTPCardForOwner(t, handler, rootCookie, reseller.ID, 1)
	otherKey := generateHTTPCardForOwner(t, handler, rootCookie, other.ID, 1)
	activateHTTPCardWithRemote(t, handler, resellerKey, "device-reseller")
	activateHTTPCardWithRemote(t, handler, otherKey, "device-other")

	resellerCookie := loginAdminAs(t, handler, "reseller", "reseller-pass")
	req := httptest.NewRequest(http.MethodGet, "/api/admin/devices/device-reseller/remote", nil)
	req.AddCookie(resellerCookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("own remote status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/devices/device-other/remote", nil)
	req.AddCookie(resellerCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("sibling remote status = %d body=%s, want 403", rec.Code, rec.Body.String())
	}

	noSecret := createAdminAccount(t, handler, rootCookie, CreateAdminAccountRequest{
		Username:    "limited",
		Password:    "limited-pass",
		Level:       AdminLevelReseller,
		Permissions: []string{PermissionDevicesView, PermissionDevicesRemoteView},
	})
	limitedKey := generateHTTPCardForOwner(t, handler, rootCookie, noSecret.ID, 1)
	activateHTTPCardWithRemote(t, handler, limitedKey, "device-limited")
	limitedCookie := loginAdminAs(t, handler, "limited", "limited-pass")
	req = httptest.NewRequest(http.MethodPost, "/api/admin/devices/device-limited/remote/secrets/reveal", strings.NewReader(`{"endpointName":"OpenAI","field":"apiKey"}`))
	req.AddCookie(limitedCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("limited reveal status = %d body=%s, want 403", rec.Code, rec.Body.String())
	}
}

func TestRemoteCommandPayloadIsEncryptedAndTamperRejected(t *testing.T) {
	handler := newTestHTTPHandler(t)
	rootCookie := loginAdmin(t, handler)
	cardKey := generateHTTPCard(t, handler, 1)
	deviceKey, devicePub := testRemoteDeviceKey(t)
	activated := activateHTTPCardWithRemoteKey(t, handler, cardKey, "device-a", devicePub)

	req := httptest.NewRequest(http.MethodPost, "/api/admin/devices/device-a/remote/commands", strings.NewReader(`{"commandType":"endpoint.update","payload":{"endpointName":"OpenAI","apiKey":"sk-secret-value"}}`))
	req.AddCookie(rootCookie)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("queue command status = %d body=%s", rec.Code, rec.Body.String())
	}

	pollBody := `{"ticket":"` + activated.Ticket + `","deviceId":"device-a"}`
	req = httptest.NewRequest(http.MethodPost, "/api/license/remote/poll", strings.NewReader(pollBody))
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("poll status = %d body=%s", rec.Code, rec.Body.String())
	}
	var poll struct {
		Data RemotePollResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &poll); err != nil {
		t.Fatalf("decode poll: %v", err)
	}
	if len(poll.Data.Commands) != 1 {
		t.Fatalf("commands = %#v, want one", poll.Data.Commands)
	}
	wire := mustJSON(t, poll.Data.Commands[0])
	if strings.Contains(wire, "sk-secret-value") {
		t.Fatalf("encrypted command leaked plaintext: %s", wire)
	}
	plain, err := DecryptRemoteEnvelope(deviceKey, poll.Data.Commands[0].Envelope)
	if err != nil {
		t.Fatalf("decrypt command: %v", err)
	}
	if !bytes.Contains(plain, []byte("sk-secret-value")) {
		t.Fatalf("decrypted command missing secret: %s", string(plain))
	}

	tampered := poll.Data.Commands[0].Envelope
	tampered.Ciphertext = tampered.Ciphertext[:len(tampered.Ciphertext)-2] + "AA"
	if _, err := DecryptRemoteEnvelope(deviceKey, tampered); err == nil {
		t.Fatalf("tampered envelope decrypted successfully")
	}
}

func TestRemoteSnapshotResultWithoutCommandUpdatesDeviceDetail(t *testing.T) {
	handler := newTestHTTPHandler(t)
	rootCookie := loginAdmin(t, handler)
	cardKey := generateHTTPCard(t, handler, 1)
	activated := activateHTTPCardWithRemote(t, handler, cardKey, "device-a")

	body, err := json.Marshal(RemoteResultRequest{
		Ticket:   activated.Ticket,
		DeviceID: "device-a",
		Status:   "snapshot",
		Snapshot: &RemoteSnapshot{Endpoints: []RemoteEndpointSnapshot{{
			Name:         "Primary",
			APIUrl:       "https://example.test",
			APIKeyMasked: "sk-***1234",
		}}},
	})
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/license/remote/result", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot result status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/admin/devices/device-a/remote", nil)
	req.AddCookie(rootCookie)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("remote detail status = %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Primary") || strings.Contains(rec.Body.String(), "sk-secret") {
		t.Fatalf("unexpected remote detail after snapshot: %s", rec.Body.String())
	}
}

func activateHTTPCardWithRemote(t *testing.T, handler http.Handler, cardKey, deviceID string) ActivationResult {
	t.Helper()
	_, pub := testRemoteDeviceKey(t)
	return activateHTTPCardWithRemoteKey(t, handler, cardKey, deviceID, pub)
}

func activateHTTPCardWithRemoteKey(t *testing.T, handler http.Handler, cardKey, deviceID, publicKey string) ActivationResult {
	t.Helper()
	body := `{"cardKey":"` + cardKey + `","deviceId":"` + deviceID + `","remote":{"supported":true,"enabled":true,"publicKey":"` + publicKey + `","capabilities":["endpoints:view","endpoints:write","secrets:reveal"]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/license/activate", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("activate remote %s status = %d body=%s", deviceID, rec.Code, rec.Body.String())
	}
	var decoded struct {
		Data ActivationResult `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode activation: %v", err)
	}
	return decoded.Data
}

func testRemoteDeviceKey(t *testing.T) (*ecdh.PrivateKey, string) {
	t.Helper()
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate remote key: %v", err)
	}
	return key, EncodeRemotePublicKey(key.PublicKey())
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal json: %v", err)
	}
	return string(data)
}
