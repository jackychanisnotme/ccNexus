package branding

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLookupEnvPrefersPrimaryOverLegacy(t *testing.T) {
	t.Setenv("AINEXUS_PORT", "4000")
	t.Setenv("CCNEXUS_PORT", "3000")

	if got := LookupEnv("AINEXUS_PORT", "CCNEXUS_PORT"); got != "4000" {
		t.Fatalf("LookupEnv returned %q, want %q", got, "4000")
	}
}

func TestLookupEnvFallsBackToLegacy(t *testing.T) {
	t.Setenv("CCNEXUS_PORT", "3000")

	if got := LookupEnv("AINEXUS_PORT", "CCNEXUS_PORT"); got != "3000" {
		t.Fatalf("LookupEnv returned %q, want %q", got, "3000")
	}
}

func TestResolveDataDirPrefersNewDir(t *testing.T) {
	home := t.TempDir()
	newDir := filepath.Join(home, DefaultDataDirName)
	legacyDir := filepath.Join(home, LegacyDataDirName)
	if err := os.MkdirAll(newDir, 0755); err != nil {
		t.Fatalf("create new dir: %v", err)
	}
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}

	if got := ResolveDataDir(home); got != newDir {
		t.Fatalf("ResolveDataDir returned %q, want %q", got, newDir)
	}
}

func TestResolveDataDirFallsBackToLegacyDir(t *testing.T) {
	home := t.TempDir()
	legacyDir := filepath.Join(home, LegacyDataDirName)
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}

	if got := ResolveDataDir(home); got != legacyDir {
		t.Fatalf("ResolveDataDir returned %q, want %q", got, legacyDir)
	}
}

func TestResolveDatabasePathFallsBackToLegacyDatabase(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, DefaultDataDirName)
	legacyPath := filepath.Join(home, LegacyDataDirName, LegacyDatabaseFilename)
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0755); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0644); err != nil {
		t.Fatalf("create legacy database: %v", err)
	}

	if got := ResolveDatabasePath(home, dataDir); got != legacyPath {
		t.Fatalf("ResolveDatabasePath returned %q, want %q", got, legacyPath)
	}
}
