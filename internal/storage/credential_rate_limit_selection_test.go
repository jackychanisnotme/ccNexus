package storage

import (
	"errors"
	"testing"
	"time"
)

func TestGetUsableCodexCredentialSkipsSaturatedCredential(t *testing.T) {
	store := newTestStorage(t)
	defer store.Close()
	now := time.Date(2026, 6, 23, 16, 0, 0, 0, time.UTC)

	exhausted := saveCodexSelectionCredential(t, store, "pool", "exhausted")
	available := saveCodexSelectionCredential(t, store, "pool", "available")
	upsertCodexSelectionRateLimits(t, store, exhausted.ID, now, 100, now.Add(4*time.Hour), nil)

	got, err := store.GetUsableEndpointCredentialByProvider("pool", ProviderTypeCodex, now)
	if err != nil {
		t.Fatalf("select credential: %v", err)
	}
	if got == nil || got.ID != available.ID {
		t.Fatalf("selected credential = %#v, want id=%d", got, available.ID)
	}
}

func TestGetUsableCodexCredentialReportsEarliestRecoveryWhenAllSaturated(t *testing.T) {
	store := newTestStorage(t)
	defer store.Close()
	now := time.Date(2026, 6, 23, 16, 0, 0, 0, time.UTC)
	retryAt := now.Add(4 * time.Hour)

	credential := saveCodexSelectionCredential(t, store, "pool", "exhausted")
	upsertCodexSelectionRateLimits(t, store, credential.ID, now, 100, retryAt, nil)

	got, err := store.GetUsableEndpointCredentialByProvider("pool", ProviderTypeCodex, now)
	if got != nil {
		t.Fatalf("expected no usable credential, got %#v", got)
	}
	var rateLimitErr *CredentialRateLimitedError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("expected CredentialRateLimitedError, got %T: %v", err, err)
	}
	if !rateLimitErr.RetryAt.Equal(retryAt) {
		t.Fatalf("retry at = %s, want %s", rateLimitErr.RetryAt, retryAt)
	}
}

func TestGetUsableCodexCredentialReportsRateLimitWhileCredentialIsCoolingDown(t *testing.T) {
	store := newTestStorage(t)
	defer store.Close()
	now := time.Date(2026, 6, 23, 16, 0, 0, 0, time.UTC)
	retryAt := now.Add(4 * time.Hour)

	credential := saveCodexSelectionCredential(t, store, "pool", "exhausted")
	upsertCodexSelectionRateLimits(t, store, credential.ID, now, 100, retryAt, nil)
	if err := store.MarkCredentialCooldown(credential.ID, 5*time.Minute, "rate limited", now); err != nil {
		t.Fatalf("mark cooldown: %v", err)
	}

	got, err := store.GetUsableEndpointCredentialByProvider("pool", ProviderTypeCodex, now)
	if got != nil {
		t.Fatalf("expected no usable credential, got %#v", got)
	}
	var rateLimitErr *CredentialRateLimitedError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("expected CredentialRateLimitedError, got %T: %v", err, err)
	}
	if !rateLimitErr.RetryAt.Equal(retryAt) {
		t.Fatalf("retry at = %s, want %s", rateLimitErr.RetryAt, retryAt)
	}
}

func TestGetUsableCodexCredentialReportsRateLimitCooldownWithoutSnapshot(t *testing.T) {
	store := newTestStorage(t)
	defer store.Close()
	now := time.Date(2026, 6, 23, 16, 0, 0, 0, time.UTC)
	credential := saveCodexSelectionCredential(t, store, "pool", "exhausted")
	if err := store.MarkCredentialFailure(credential.ID, 429, "codex websocket upstream error status=429 type=usage_limit_reached", now); err != nil {
		t.Fatalf("mark rate limit: %v", err)
	}

	got, err := store.GetUsableEndpointCredentialByProvider("pool", ProviderTypeCodex, now)
	if got != nil {
		t.Fatalf("expected no usable credential, got %#v", got)
	}
	var rateLimitErr *CredentialRateLimitedError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("expected CredentialRateLimitedError, got %T: %v", err, err)
	}
	want := now.Add(defaultCooldown)
	if !rateLimitErr.RetryAt.Equal(want) {
		t.Fatalf("retry at = %s, want %s", rateLimitErr.RetryAt, want)
	}
}

func TestGetUsableCodexCredentialAllowsExpiredSnapshot(t *testing.T) {
	store := newTestStorage(t)
	defer store.Close()
	now := time.Date(2026, 6, 23, 16, 0, 0, 0, time.UTC)

	credential := saveCodexSelectionCredential(t, store, "pool", "reset")
	upsertCodexSelectionRateLimits(t, store, credential.ID, now.Add(-time.Hour), 100, now.Add(-time.Minute), nil)

	got, err := store.GetUsableEndpointCredentialByProvider("pool", ProviderTypeCodex, now)
	if err != nil {
		t.Fatalf("select credential: %v", err)
	}
	if got == nil || got.ID != credential.ID {
		t.Fatalf("selected credential = %#v, want id=%d", got, credential.ID)
	}
}

func TestGetUsableCodexCredentialWaitsForAllSaturatedWindows(t *testing.T) {
	store := newTestStorage(t)
	defer store.Close()
	now := time.Date(2026, 6, 23, 16, 0, 0, 0, time.UTC)
	primaryReset := now.Add(time.Hour).Unix()
	secondaryReset := now.Add(7 * 24 * time.Hour).Unix()
	credential := saveCodexSelectionCredential(t, store, "pool", "exhausted")
	snapshot := CodexRateLimitSnapshot{
		LimitID:   "codex",
		Primary:   &CodexRateLimitWindow{UsedPercent: 100, ResetsAt: &primaryReset},
		Secondary: &CodexRateLimitWindow{UsedPercent: 100, ResetsAt: &secondaryReset},
	}
	if err := store.UpsertCredentialRateLimits(credential.ID, &CodexRateLimitsData{
		Snapshot: &snapshot,
		Source:   "test",
	}, "ok", "", now); err != nil {
		t.Fatalf("upsert rate limits: %v", err)
	}

	_, err := store.GetUsableEndpointCredentialByProvider("pool", ProviderTypeCodex, now)
	var rateLimitErr *CredentialRateLimitedError
	if !errors.As(err, &rateLimitErr) {
		t.Fatalf("expected CredentialRateLimitedError, got %T: %v", err, err)
	}
	want := time.Unix(secondaryReset, 0).UTC()
	if !rateLimitErr.RetryAt.Equal(want) {
		t.Fatalf("retry at = %s, want %s", rateLimitErr.RetryAt, want)
	}
}

func TestGetUsableCodexCredentialAllowsCreditsAtPlanLimit(t *testing.T) {
	for _, credits := range []*CodexCreditsSnapshot{
		{HasCredits: true, Balance: "10"},
		{Unlimited: true},
	} {
		store := newTestStorage(t)
		now := time.Date(2026, 6, 23, 16, 0, 0, 0, time.UTC)
		credential := saveCodexSelectionCredential(t, store, "pool", "credits")
		upsertCodexSelectionRateLimits(t, store, credential.ID, now, 100, now.Add(4*time.Hour), credits)

		got, err := store.GetUsableEndpointCredentialByProvider("pool", ProviderTypeCodex, now)
		if err != nil {
			t.Fatalf("select credential: %v", err)
		}
		if got == nil || got.ID != credential.ID {
			t.Fatalf("selected credential = %#v, want id=%d", got, credential.ID)
		}
		_ = store.Close()
	}
}

func TestGetUsableCodexCredentialReturnsExpiredRefreshableFallback(t *testing.T) {
	store := newTestStorage(t)
	defer store.Close()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-time.Minute)

	credential := &EndpointCredential{
		EndpointName: "pool",
		ProviderType: ProviderTypeCodex,
		AccessToken:  "expired-access",
		RefreshToken: "refresh-token",
		ExpiresAt:    &expiredAt,
		Status:       credentialStatusActive,
		Enabled:      true,
	}
	if err := store.SaveEndpointCredential(credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	got, err := store.GetUsableEndpointCredentialByProvider("pool", ProviderTypeCodex, now)
	if err != nil {
		t.Fatalf("select credential: %v", err)
	}
	if got == nil || got.ID != credential.ID {
		t.Fatalf("selected credential = %#v, want id=%d", got, credential.ID)
	}
	if got.Status != credentialStatusExpired {
		t.Fatalf("status = %q, want %q", got.Status, credentialStatusExpired)
	}
}

func TestGetUsableCodexCredentialDoesNotReturnExpiredWithoutRefreshToken(t *testing.T) {
	store := newTestStorage(t)
	defer store.Close()
	now := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	expiredAt := now.Add(-time.Minute)

	credential := &EndpointCredential{
		EndpointName: "pool",
		ProviderType: ProviderTypeCodex,
		AccessToken:  "expired-access",
		ExpiresAt:    &expiredAt,
		Status:       credentialStatusActive,
		Enabled:      true,
	}
	if err := store.SaveEndpointCredential(credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}

	got, err := store.GetUsableEndpointCredentialByProvider("pool", ProviderTypeCodex, now)
	if err != nil {
		t.Fatalf("select credential: %v", err)
	}
	if got != nil {
		t.Fatalf("expected no usable credential, got %#v", got)
	}
}

func saveCodexSelectionCredential(t *testing.T, store *SQLiteStorage, endpointName, token string) *EndpointCredential {
	t.Helper()
	credential := &EndpointCredential{
		EndpointName: endpointName,
		ProviderType: ProviderTypeCodex,
		AccessToken:  token,
		Status:       credentialStatusActive,
		Enabled:      true,
	}
	if err := store.SaveEndpointCredential(credential); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	return credential
}

func upsertCodexSelectionRateLimits(t *testing.T, store *SQLiteStorage, credentialID int64, updatedAt time.Time, usedPercent float64, resetsAt time.Time, credits *CodexCreditsSnapshot) {
	t.Helper()
	resetUnix := resetsAt.Unix()
	snapshot := CodexRateLimitSnapshot{
		LimitID: "codex",
		Primary: &CodexRateLimitWindow{
			UsedPercent: usedPercent,
			ResetsAt:    &resetUnix,
		},
		Credits: credits,
	}
	if err := store.UpsertCredentialRateLimits(credentialID, &CodexRateLimitsData{
		Snapshot:  &snapshot,
		ByLimitID: map[string]CodexRateLimitSnapshot{"codex": snapshot},
		Source:    "test",
	}, "ok", "", updatedAt); err != nil {
		t.Fatalf("upsert rate limits: %v", err)
	}
}
