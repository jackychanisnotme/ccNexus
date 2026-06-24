package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestStorage(t *testing.T) *SQLiteStorage {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "ainexus.db")
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

func TestMarkCredentialSuccessDoesNotReactivateInvalidCredential(t *testing.T) {
	store := newTestStorage(t)
	defer store.Close()
	now := time.Now().UTC()

	cred := &EndpointCredential{EndpointName: "ep", AccessToken: "tok", Enabled: true}
	if err := store.SaveEndpointCredential(cred); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := store.MarkCredentialFailureIfCurrent(cred.ID, "tok", 401, "unauthorized", now); err != nil {
		t.Fatalf("mark failure: %v", err)
	}
	updated, err := store.MarkCredentialSuccessIfCurrent(cred.ID, "tok", now.Add(time.Second))
	if err != nil {
		t.Fatalf("mark success: %v", err)
	}
	if updated {
		t.Fatal("stale success unexpectedly updated invalid credential")
	}
	got, err := store.GetCredentialByID(cred.ID)
	if err != nil {
		t.Fatalf("load credential: %v", err)
	}
	if got == nil || got.Status != credentialStatusInvalid {
		t.Fatalf("credential = %#v, want invalid", got)
	}
}

func TestCredentialOutcomeDoesNotOverwriteNewAccessTokenGeneration(t *testing.T) {
	store := newTestStorage(t)
	defer store.Close()
	now := time.Now().UTC()

	cred := &EndpointCredential{EndpointName: "ep", AccessToken: "old-token", Enabled: true}
	if err := store.SaveEndpointCredential(cred); err != nil {
		t.Fatalf("save: %v", err)
	}
	cred.AccessToken = "new-token"
	if err := store.UpdateEndpointCredential(cred); err != nil {
		t.Fatalf("rotate token: %v", err)
	}

	if updated, err := store.MarkCredentialSuccessIfCurrent(cred.ID, "old-token", now); err != nil {
		t.Fatalf("mark stale success: %v", err)
	} else if updated {
		t.Fatal("stale success unexpectedly updated new token generation")
	}
	if updated, err := store.MarkCredentialFailureIfCurrent(cred.ID, "old-token", 401, "unauthorized", now); err != nil {
		t.Fatalf("mark stale failure: %v", err)
	} else if updated {
		t.Fatal("stale failure unexpectedly updated new token generation")
	}

	got, err := store.GetCredentialByID(cred.ID)
	if err != nil {
		t.Fatalf("load credential: %v", err)
	}
	if got == nil || got.AccessToken != "new-token" || got.Status == credentialStatusInvalid {
		t.Fatalf("credential = %#v, want active new token", got)
	}
}
