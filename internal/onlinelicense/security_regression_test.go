package onlinelicense

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRemoteCommandSignatureRejectsTamperingAndExpiry(t *testing.T) {
	now := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	deviceKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate device key: %v", err)
	}
	envelope, err := EncryptRemoteEnvelope(EncodeRemotePublicKey(deviceKey.PublicKey()), []byte(`{"commandType":"endpoint.delete","payload":{}}`))
	if err != nil {
		t.Fatalf("encrypt command: %v", err)
	}
	command := RemoteCommandRecord{
		DeviceID:    "device-a",
		CommandType: "endpoint.delete",
		Envelope:    envelope,
		ExpiresAt:   now.Add(5 * time.Minute),
		CreatedAt:   now,
	}
	if err := signRemoteCommand(privateKey, &command); err != nil {
		t.Fatalf("sign command: %v", err)
	}
	verifier := NewVerifier(publicKey, Options{Now: func() time.Time { return now }})
	if err := verifier.VerifyRemoteCommand(command, "device-a", now); err != nil {
		t.Fatalf("verify signed command: %v", err)
	}

	tampered := command
	tampered.CommandType = "secret.reveal"
	if err := verifier.VerifyRemoteCommand(tampered, "device-a", now); err == nil {
		t.Fatal("tampered command type passed signature verification")
	}
	tampered = command
	tampered.Envelope.Ciphertext += "A"
	if err := verifier.VerifyRemoteCommand(tampered, "device-a", now); err == nil {
		t.Fatal("tampered ciphertext passed signature verification")
	}
	unsigned := command
	unsigned.Signature = ""
	if err := verifier.VerifyRemoteCommand(unsigned, "device-a", now); err == nil {
		t.Fatal("unsigned command passed verification")
	}
	if err := verifier.VerifyRemoteCommand(command, "device-a", command.ExpiresAt); err == nil {
		t.Fatal("expired command passed verification")
	}
}

func TestRemoteCommandReplayStatePersistsAcrossClientInstances(t *testing.T) {
	now := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	store := newMemoryConfigStore()
	command := RemoteCommandRecord{
		CommandNonce: "unique-command-nonce",
		ExpiresAt:    now.Add(5 * time.Minute),
	}
	first := NewClientService(store, "device-a", ClientOptions{Now: func() time.Time { return now }})
	if err := first.rememberRemoteCommand(command); err != nil {
		t.Fatalf("remember command: %v", err)
	}
	second := NewClientService(store, "device-a", ClientOptions{Now: func() time.Time { return now }})
	if err := second.rememberRemoteCommand(command); err == nil || !strings.Contains(err.Error(), "replay") {
		t.Fatalf("second remember error = %v, want replay rejection", err)
	}
}

func TestRemoteInterfacesRejectExpiredDisabledAndMismatchedTickets(t *testing.T) {
	current := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	service := NewService(store, privateKey, Options{Now: func() time.Time { return current }})
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanCustom, Days: 1, Count: 1, MaxDevices: 1})
	activated, err := service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if _, err := service.PollRemoteCommands(RemotePollRequest{Ticket: activated.Ticket, DeviceID: "device-b"}); !errors.Is(err, ErrInvalidTicket) {
		t.Fatalf("wrong-device poll error = %v, want ErrInvalidTicket", err)
	}

	current = current.Add(2 * 24 * time.Hour)
	if _, err := service.PollRemoteCommands(RemotePollRequest{Ticket: activated.Ticket, DeviceID: "device-a"}); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expired poll error = %v, want ErrTicketExpired", err)
	}

	current = time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	card = mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	activated, err = service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: "device-c"})
	if err != nil {
		t.Fatalf("activate device-c: %v", err)
	}
	if err := service.DisableActivation(activated.ActivationID); err != nil {
		t.Fatalf("disable activation: %v", err)
	}
	if err := service.SubmitRemoteResult(RemoteResultRequest{Ticket: activated.Ticket, DeviceID: "device-c", Status: "snapshot"}); !errors.Is(err, ErrActivationBlocked) {
		t.Fatalf("disabled activation result error = %v, want ErrActivationBlocked", err)
	}

	card = mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	activated, err = service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: "device-d"})
	if err != nil {
		t.Fatalf("activate device-d: %v", err)
	}
	if err := service.DisableCard(card.ID); err != nil {
		t.Fatalf("disable card: %v", err)
	}
	if _, err := service.SubmitEndpointErrorTelemetry(EndpointErrorTelemetryRequest{
		Ticket:   activated.Ticket,
		DeviceID: "device-d",
	}); !errors.Is(err, ErrCardDisabled) && !errors.Is(err, ErrActivationBlocked) {
		t.Fatalf("disabled-card telemetry error = %v, want disabled rejection", err)
	}
}

func TestRemoteSecretRevealIsDisabledByDefaultAndCannotUseGenericQueue(t *testing.T) {
	service := newTestService(t, time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC))
	root := &AdminAccount{
		ID:          1,
		Username:    "root",
		Level:       AdminLevelRoot,
		Status:      AdminAccountStatusActive,
		Permissions: allAdminPermissions(),
	}
	if _, err := service.QueueRemoteSecretRevealFor(root, "device-a", RemoteSecretRevealRequest{}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("default secret reveal error = %v, want ErrForbidden", err)
	}
	if _, err := service.QueueRemoteCommandFor(root, "device-a", RemoteCommandRequest{
		CommandType: "secret.reveal",
		Payload:     map[string]string{"field": "apiKey"},
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("generic secret reveal error = %v, want ErrForbidden", err)
	}
}

func TestSQLiteStoreSecuresDatabaseFilePermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "license.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat database: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("database mode = %o, want 600", info.Mode().Perm())
	}
}

func TestRemoteCommandSignatureFieldsRoundTripThroughSQLite(t *testing.T) {
	now := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t)
	command := &RemoteCommandRecord{
		DeviceID:     "device-a",
		CommandType:  "endpoint.update",
		Status:       RemoteCommandStatusQueued,
		CommandNonce: "nonce-value",
		Signature:    "signature-value",
		Envelope:     RemoteEnvelope{ServerPublicKey: "public", Nonce: "nonce", Ciphertext: "ciphertext"},
		ExpiresAt:    now.Add(time.Minute),
		CreatedAt:    now,
	}
	if err := store.CreateRemoteCommand(command, 0); err != nil {
		t.Fatalf("create command: %v", err)
	}
	got, err := store.GetRemoteCommand("device-a", command.ID)
	if err != nil {
		t.Fatalf("get command: %v", err)
	}
	data, _ := json.Marshal(got)
	if got.CommandNonce != command.CommandNonce || got.Signature != command.Signature {
		t.Fatalf("signature fields did not round trip: %s", data)
	}
}

func TestPollBackfillsSignatureForLegacyQueuedCommand(t *testing.T) {
	now := time.Date(2026, 7, 10, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	service := NewService(store, privateKey, Options{Now: func() time.Time { return now }})
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	activated, err := service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	command := &RemoteCommandRecord{
		DeviceID:    "device-a",
		CommandType: "endpoint.update",
		Status:      RemoteCommandStatusQueued,
		Envelope: RemoteEnvelope{
			ServerPublicKey: "legacy-public",
			Nonce:           "legacy-nonce",
			Ciphertext:      "legacy-ciphertext",
		},
		ExpiresAt: now.Add(5 * time.Minute),
		CreatedAt: now,
	}
	if err := store.CreateRemoteCommand(command, 0); err != nil {
		t.Fatalf("create legacy command: %v", err)
	}
	response, err := service.PollRemoteCommands(RemotePollRequest{Ticket: activated.Ticket, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("poll legacy command: %v", err)
	}
	if len(response.Commands) != 1 || response.Commands[0].Signature == "" || response.Commands[0].CommandNonce == "" {
		t.Fatalf("legacy command was not signed: %#v", response.Commands)
	}
	verifier := NewVerifier(publicKey, Options{Now: func() time.Time { return now }})
	if err := verifier.VerifyRemoteCommand(response.Commands[0], "device-a", now); err != nil {
		t.Fatalf("verify backfilled signature: %v", err)
	}
}
