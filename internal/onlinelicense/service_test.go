package onlinelicense

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"path/filepath"
	"strings"
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

	beforeRepeat, err := service.store.FindActivation(card.ID, "device-a")
	if err != nil {
		t.Fatalf("find activation before repeat: %v", err)
	}
	again, err := service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("repeat activation on same device: %v", err)
	}
	if again.Status != ActivationStatusActive {
		t.Fatalf("repeat activation status = %q, want active", again.Status)
	}
	if !again.ExpiresAt.Equal(beforeRepeat.ExpiresAt) {
		t.Fatalf("repeat activation extended expiry from %s to %s", beforeRepeat.ExpiresAt, again.ExpiresAt)
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

func TestListDevicesGroupsRedemptionsByDevice(t *testing.T) {
	now := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	current := now
	store := newTestStore(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	service := NewService(store, privateKey, Options{Now: func() time.Time { return current }})

	first := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1})
	if _, err := service.Activate(ActivationRequest{CardKey: first.CardKey, DeviceID: "device-a", Platform: "darwin", AppVersion: "6.3.2"}); err != nil {
		t.Fatalf("activate first card: %v", err)
	}
	current = now.AddDate(0, 0, 1)
	second := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanYearly, Count: 1})
	secondActivation, err := service.Activate(ActivationRequest{CardKey: second.CardKey, DeviceID: "device-a", Platform: "darwin", AppVersion: "6.3.3"})
	if err != nil {
		t.Fatalf("activate second card: %v", err)
	}

	devices, err := service.ListDevices()
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 {
		t.Fatalf("devices = %d, want 1: %#v", len(devices), devices)
	}
	device := devices[0]
	if device.DeviceID != "device-a" || len(device.Licenses) != 2 {
		t.Fatalf("unexpected grouped device: %#v", device)
	}
	if !device.ExpiresAt.Equal(secondActivation.ExpiresAt) {
		t.Fatalf("device expiry = %s, want %s", device.ExpiresAt, secondActivation.ExpiresAt)
	}
	if device.AppVersion != "6.3.3" || device.Status != ActivationStatusActive {
		t.Fatalf("unexpected device metadata/status: %#v", device)
	}
}

func TestDisableCardClampsItsActivationAndRollsDeviceBack(t *testing.T) {
	now := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	current := now
	store := newTestStore(t)
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	service := NewService(store, privateKey, Options{Now: func() time.Time { return current }})

	first := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1})
	firstActivation, err := service.Activate(ActivationRequest{CardKey: first.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("activate first card: %v", err)
	}
	current = now.AddDate(0, 0, 1)
	second := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1})
	secondActivation, err := service.Activate(ActivationRequest{CardKey: second.CardKey, DeviceID: "device-a"})
	if err != nil {
		t.Fatalf("activate second card: %v", err)
	}
	if err := service.DisableCard(second.ID); err != nil {
		t.Fatalf("disable second card: %v", err)
	}

	disabled, err := store.GetActivation(secondActivation.ActivationID)
	if err != nil {
		t.Fatalf("get disabled activation: %v", err)
	}
	if disabled.Status != ActivationStatusDisabled || disabled.ExpiresAt.After(current) {
		t.Fatalf("disabled activation was not clamped: %#v", disabled)
	}
	devices, err := service.ListDevices()
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 || !devices[0].ExpiresAt.Equal(firstActivation.ExpiresAt) {
		t.Fatalf("device did not roll back to prior entitlement: %#v", devices)
	}
}

func TestSetDeviceExpiryChangesEffectiveExpiryAndWritesAudit(t *testing.T) {
	now := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	service := newTestService(t, now)
	first := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1})
	if _, err := service.Activate(ActivationRequest{CardKey: first.CardKey, DeviceID: "device-a"}); err != nil {
		t.Fatalf("activate first card: %v", err)
	}
	second := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1})
	if _, err := service.Activate(ActivationRequest{CardKey: second.CardKey, DeviceID: "device-a"}); err != nil {
		t.Fatalf("activate second card: %v", err)
	}

	wantExpiry := now.AddDate(0, 0, 7)
	if err := service.SetDeviceExpiry("device-a", wantExpiry); err != nil {
		t.Fatalf("set device expiry: %v", err)
	}
	devices, err := service.ListDevices()
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 || !devices[0].ExpiresAt.Equal(wantExpiry) {
		t.Fatalf("device expiry = %#v, want %s", devices, wantExpiry)
	}
	history, err := service.ListAudit()
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	found := false
	for _, item := range history {
		if item.Action == "set_device_expiry" && strings.Contains(item.Detail, "device-a") && strings.Contains(item.Detail, wantExpiry.Format(time.RFC3339)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("set_device_expiry audit missing: %#v", history)
	}
}

func TestSetDeviceRemarkShowsOnDeviceAndWritesAudit(t *testing.T) {
	now := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	service := newTestService(t, now)
	card := mustGenerateOne(t, service, GenerateCardsRequest{Plan: PlanMonthly, Count: 1})
	if _, err := service.Activate(ActivationRequest{CardKey: card.CardKey, DeviceID: "device-a"}); err != nil {
		t.Fatalf("activate card: %v", err)
	}

	if err := service.SetDeviceRemark("device-a", "VIP customer"); err != nil {
		t.Fatalf("set device remark: %v", err)
	}
	devices, err := service.ListDevices()
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 1 || devices[0].Remark != "VIP customer" {
		t.Fatalf("device remark = %#v, want VIP customer", devices)
	}
	history, err := service.ListAudit()
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	found := false
	for _, item := range history {
		if item.Action == "set_device_remark" && strings.Contains(item.Detail, "device-a") && strings.Contains(item.Detail, "VIP customer") {
			found = true
		}
	}
	if !found {
		t.Fatalf("set_device_remark audit missing: %#v", history)
	}
}

func TestStoreInitClampsLegacyDisabledActivationExpiry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "license.db")
	store, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	now := time.Date(2026, 6, 19, 8, 0, 0, 0, time.UTC)
	card := &CardRecord{
		CardHash:   HashCardKey("legacy-card"),
		Plan:       PlanMonthly,
		Days:       30,
		MaxDevices: 1,
		Status:     CardStatusActive,
		CreatedAt:  now,
	}
	if err := store.CreateCard(card); err != nil {
		t.Fatalf("create card: %v", err)
	}
	activation := &ActivationRecord{
		CardID:        card.ID,
		DeviceID:      "device-legacy",
		Status:        ActivationStatusActive,
		ActivatedAt:   now,
		ExpiresAt:     now.AddDate(1, 0, 0),
		LastCheckedAt: now,
	}
	if err := store.UpsertActivation(activation); err != nil {
		t.Fatalf("create activation: %v", err)
	}
	disabledAt := now.AddDate(0, 0, 2)
	if _, err := store.db.Exec(`
		UPDATE license_activations
		SET status = ?, disabled_at = ?, expires_at = ?
		WHERE id = ?
	`, ActivationStatusDisabled, formatTime(disabledAt), formatTime(now.AddDate(2, 0, 0)), activation.ID); err != nil {
		t.Fatalf("seed legacy activation: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	reopened, err := NewSQLiteStore(path)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer reopened.Close()
	got, err := reopened.GetActivation(activation.ID)
	if err != nil {
		t.Fatalf("get migrated activation: %v", err)
	}
	if !got.ExpiresAt.Equal(disabledAt) {
		t.Fatalf("legacy disabled expiry = %s, want %s", got.ExpiresAt, disabledAt)
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
