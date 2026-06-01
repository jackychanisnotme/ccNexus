package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStorage(t *testing.T) *SQLiteStorage {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ccnexus.db")
	store, err := NewSQLiteStorage(dbPath)
	if err != nil {
		t.Fatalf("new storage: %v", err)
	}
	return store
}

// TestMarkCredentialFailureCooldownThreshold verifies that repeated generic
// (non-401/403/429) failures eventually cool a credential down instead of
// leaving it active forever (regression for failure isolation).
func TestMarkCredentialFailureCooldownThreshold(t *testing.T) {
	store := newTestStorage(t)
	now := time.Now().UTC()

	cred := &EndpointCredential{EndpointName: "ep", AccessToken: "tok", Enabled: true}
	if err := store.SaveEndpointCredential(cred); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Below threshold: still usable.
	for i := 0; i < maxConsecutiveFailures-1; i++ {
		if err := store.MarkCredentialFailure(cred.ID, 0, "boom", now); err != nil {
			t.Fatalf("mark failure: %v", err)
		}
	}
	if u, _ := store.GetUsableEndpointCredential("ep", now); u == nil {
		t.Fatalf("credential should still be usable below threshold")
	}

	// Reaching the threshold puts it into cooldown.
	if err := store.MarkCredentialFailure(cred.ID, 0, "boom", now); err != nil {
		t.Fatalf("mark failure: %v", err)
	}
	if u, _ := store.GetUsableEndpointCredential("ep", now); u != nil {
		t.Fatalf("credential should be in cooldown after %d failures", maxConsecutiveFailures)
	}
}

// TestMarkCredentialFailureAuthInvalidates checks 401 still hard-invalidates.
func TestMarkCredentialFailureAuthInvalidates(t *testing.T) {
	store := newTestStorage(t)
	now := time.Now().UTC()

	cred := &EndpointCredential{EndpointName: "ep", AccessToken: "tok", Enabled: true}
	if err := store.SaveEndpointCredential(cred); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := store.MarkCredentialFailure(cred.ID, 401, "unauthorized", now); err != nil {
		t.Fatalf("mark failure: %v", err)
	}
	if u, _ := store.GetUsableEndpointCredential("ep", now); u != nil {
		t.Fatalf("credential should be invalid after 401")
	}
}
