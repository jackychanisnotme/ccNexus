package onlinelicense

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestGenerateCardsStoresOnlyHashAndEnforcesDeviceLimit(t *testing.T) {
	now := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	store := newTestStore(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	service := NewService(store, privateKey, Options{Now: func() time.Time { return now }})

	generated, err := service.GenerateCards(GenerateCardsRequest{
		Plan:       PlanMonthly,
		Days:       30,
		Count:      1,
		MaxDevices: 2,
		Customer:   "Alice",
		Remark:     "first batch",
	})
	if err != nil {
		t.Fatalf("generate cards: %v", err)
	}
	if len(generated.Cards) != 1 {
		t.Fatalf("generated %d cards, want 1", len(generated.Cards))
	}
	card := generated.Cards[0]
	if card.CardKey == "" || card.CardHash == "" {
		t.Fatalf("card key/hash should be populated: %+v", card)
	}
	if card.CardHash == card.CardKey {
		t.Fatalf("card hash must not equal plaintext card key")
	}
	if card.MaxDevices != 2 {
		t.Fatalf("max devices = %d, want 2", card.MaxDevices)
	}

	for _, deviceID := range []string{"device-a", "device-b"} {
		if _, err := service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: deviceID, Platform: "darwin", AppVersion: "6.0.1"}); err != nil {
			t.Fatalf("activate %s: %v", deviceID, err)
		}
	}
	if _, err := service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: "device-c"}); err == nil {
		t.Fatalf("third device activation succeeded, want device limit error")
	}

	again, err := service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("repeat activation on same device: %v", err)
	}
	if again.Status != ActivationStatusActive {
		t.Fatalf("repeat activation status = %q, want active", again.Status)
	}
}

func TestDisabledCardAndActivationAreRejected(t *testing.T) {
	now := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	service := newTestService(t, now)

	generated := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	if err := service.DisableCard(generated.ID); err != nil {
		t.Fatalf("disable card: %v", err)
	}
	if _, err := service.Activate(ActivationRequest{CardKey: generated.CardKey, DeviceID: "device-a"}); err == nil {
		t.Fatalf("disabled card activation succeeded")
	}

	second := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	activated, err := service.Activate(ActivationRequest{CardKey: second.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("activate second card: %v", err)
	}
	if err := service.DisableActivation(activated.ActivationID); err != nil {
		t.Fatalf("disable activation: %v", err)
	}
	if _, err := service.Refresh(RefreshRequest{Ticket: activated.Ticket}); err == nil {
		t.Fatalf("disabled activation refresh succeeded")
	}
}

func TestDisabledCardRejectsRefresh(t *testing.T) {
	now := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	service := newTestService(t, now)
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	activated, err := service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if err := service.DisableCard(card.ID); err != nil {
		t.Fatalf("disable card: %v", err)
	}
	if _, err := service.Refresh(RefreshRequest{Ticket: activated.Ticket, DeviceID: "device-a"}); err == nil {
		t.Fatalf("refresh for disabled card succeeded")
	}
}

func TestConcurrentActivationsRespectDeviceLimit(t *testing.T) {
	now := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	workers := 16
	store := newTestStore(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	service := NewService(store, privateKey, Options{Now: func() time.Time { return now }})
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})

	start := make(chan struct{})
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		deviceID := "device-" + string(rune('a'+i))
		go func() {
			<-start
			_, err := service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: deviceID})
			errs <- err
		}()
	}
	close(start)

	successes := 0
	limitErrors := 0
	for i := 0; i < workers; i++ {
		err := <-errs
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrDeviceLimit):
			limitErrors++
		default:
			t.Fatalf("activation returned unexpected error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful activations = %d, want 1", successes)
	}
	if limitErrors != workers-1 {
		t.Fatalf("device limit errors = %d, want %d", limitErrors, workers-1)
	}
	active, err := store.ActiveActivationCount(card.ID)
	if err != nil {
		t.Fatalf("active activation count: %v", err)
	}
	if active != 1 {
		t.Fatalf("active activations = %d, want 1", active)
	}
}

func TestActivationExtendsFromExistingExpiryWhenRenewing(t *testing.T) {
	now := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	current := now
	store := newTestStore(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	service := NewService(store, privateKey, Options{Now: func() time.Time { return current }})

	first := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	firstActivation, err := service.Activate(ActivationRequest{CardKey: first.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("activate first: %v", err)
	}
	wantFirstExpiry := now.AddDate(0, 0, 30)
	if !firstActivation.ExpiresAt.Equal(wantFirstExpiry) {
		t.Fatalf("first expiry = %s, want %s", firstActivation.ExpiresAt, wantFirstExpiry)
	}

	current = now.AddDate(0, 0, 10)
	second := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	secondActivation, err := service.Activate(ActivationRequest{CardKey: second.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("activate renewal: %v", err)
	}
	wantRenewedExpiry := wantFirstExpiry.AddDate(0, 0, 30)
	if !secondActivation.ExpiresAt.Equal(wantRenewedExpiry) {
		t.Fatalf("renewed expiry = %s, want %s", secondActivation.ExpiresAt, wantRenewedExpiry)
	}

	current = wantRenewedExpiry.AddDate(0, 0, 3)
	third := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})
	thirdActivation, err := service.Activate(ActivationRequest{CardKey: third.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("activate after expiry: %v", err)
	}
	wantAfterExpired := current.AddDate(0, 0, 30)
	if !thirdActivation.ExpiresAt.Equal(wantAfterExpired) {
		t.Fatalf("post-expiry renewal = %s, want %s", thirdActivation.ExpiresAt, wantAfterExpired)
	}
}

func TestTicketVerificationAllowsThirtyDayGrace(t *testing.T) {
	issuedAt := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	service := newTestService(t, issuedAt)
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})

	activated, err := service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	verified, err := service.VerifyTicket(activated.Ticket, "device-a", issuedAt.AddDate(0, 0, 29))
	if err != nil {
		t.Fatalf("verify inside grace: %v", err)
	}
	if !verified.Licensed {
		t.Fatalf("inside grace licensed = false")
	}
	if !verified.GraceUntil.Equal(issuedAt.AddDate(0, 0, 30)) {
		t.Fatalf("grace until = %s, want 30 days after issue", verified.GraceUntil)
	}
	if _, err := service.VerifyTicket(activated.Ticket, "device-a", issuedAt.AddDate(0, 0, 31)); err == nil {
		t.Fatalf("verify beyond grace succeeded")
	}
}

func TestActivationResultIncludesRemainingDays(t *testing.T) {
	now := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	service := newTestService(t, now)
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1, MaxDevices: 1})

	result, err := service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	if result.RemainingDays != 30 {
		t.Fatalf("remaining days = %d, want 30", result.RemainingDays)
	}
}

func TestRefreshCanRecoverAfterOfflineGraceWhenServerIsReachable(t *testing.T) {
	issuedAt := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	current := issuedAt
	store := newTestStore(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	service := NewService(store, privateKey, Options{Now: func() time.Time { return current }})
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanYearly, Count: 1, MaxDevices: 1})
	activated, err := service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("activate: %v", err)
	}
	current = issuedAt.AddDate(0, 0, 31)
	refreshed, err := service.Refresh(RefreshRequest{Ticket: activated.Ticket, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("refresh after grace with server reachable: %v", err)
	}
	if !refreshed.Licensed {
		t.Fatalf("refreshed license should be licensed")
	}
	if !refreshed.GraceUntil.Equal(current.AddDate(0, 0, 30)) {
		t.Fatalf("new grace = %s, want %s", refreshed.GraceUntil, current.AddDate(0, 0, 30))
	}
}

func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	store, err := NewSQLiteStore(filepath.Join(t.TempDir(), "license.db"))
	if err != nil {
		t.Fatalf("new sqlite store: %v", err)
	}
	t.Cleanup(func() {
		_ = store.Close()
	})
	return store
}

func newTestService(t *testing.T, now time.Time) *Service {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return NewService(newTestStore(t), privateKey, Options{Now: func() time.Time { return now }})
}

func mustGenerateOne(t *testing.T, service *Service, req GenerateCardsRequest) GeneratedCard {
	t.Helper()
	result, err := service.GenerateCards(req)
	if err != nil {
		t.Fatalf("generate card: %v", err)
	}
	if len(result.Cards) != 1 {
		t.Fatalf("generated %d cards, want 1", len(result.Cards))
	}
	return result.Cards[0]
}
