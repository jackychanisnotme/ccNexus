package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lich0821/ccNexus/internal/branding"
	"github.com/lich0821/ccNexus/internal/config"
)

func TestResolveDataDirFallsBackToLegacyHomeDir(t *testing.T) {
	home := t.TempDir()
	legacyDir := filepath.Join(home, branding.LegacyDataDirName)
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		t.Fatalf("create legacy dir: %v", err)
	}

	if got := resolveDataDirForHome(home); got != legacyDir {
		t.Fatalf("resolveDataDirForHome returned %q, want %q", got, legacyDir)
	}
}

func TestApplyEnvOverridesListenMode(t *testing.T) {
	cfg := config.DefaultConfig()
	t.Setenv("CCNEXUS_LISTEN_MODE", config.ListenModeLAN)

	applyEnvOverrides(cfg)

	if got := cfg.GetListenMode(); got != config.ListenModeLAN {
		t.Fatalf("listen mode = %q, want %q", got, config.ListenModeLAN)
	}
}

func TestApplyEnvOverridesInvalidListenModeFallsBackLocal(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.UpdateListenMode(config.ListenModeLAN)
	t.Setenv("CCNEXUS_LISTEN_MODE", "public")

	applyEnvOverrides(cfg)

	if got := cfg.GetListenMode(); got != config.ListenModeLocal {
		t.Fatalf("listen mode = %q, want %q", got, config.ListenModeLocal)
	}
}

func TestApplyEnvOverridesSupportsAINexusEnvVars(t *testing.T) {
	cfg := config.DefaultConfig()
	t.Setenv("AINEXUS_PORT", "4010")
	t.Setenv("AINEXUS_LOG_LEVEL", "2")
	t.Setenv("AINEXUS_LISTEN_MODE", config.ListenModeLAN)
	t.Setenv("AINEXUS_BASIC_AUTH_ENABLED", "false")
	t.Setenv("AINEXUS_BASIC_AUTH_USERNAME", "ainexus")
	t.Setenv("AINEXUS_BASIC_AUTH_PASSWORD", "secret")

	applyEnvOverrides(cfg)

	if got := cfg.GetPort(); got != 4010 {
		t.Fatalf("port = %d, want 4010", got)
	}
	if got := cfg.GetLogLevel(); got != 2 {
		t.Fatalf("log level = %d, want 2", got)
	}
	if got := cfg.GetListenMode(); got != config.ListenModeLAN {
		t.Fatalf("listen mode = %q, want %q", got, config.ListenModeLAN)
	}
	if cfg.GetBasicAuthEnabled() {
		t.Fatal("basic auth should be disabled")
	}
	if got := cfg.GetBasicAuthUsername(); got != "ainexus" {
		t.Fatalf("username = %q, want ainexus", got)
	}
	if got := cfg.GetBasicAuthPassword(); got != "secret" {
		t.Fatalf("password = %q, want secret", got)
	}
}

func TestApplyEnvOverridesAINexusEnvVarsTakePrecedence(t *testing.T) {
	cfg := config.DefaultConfig()
	t.Setenv("AINEXUS_PORT", "4010")
	t.Setenv("CCNEXUS_PORT", "3001")

	applyEnvOverrides(cfg)

	if got := cfg.GetPort(); got != 4010 {
		t.Fatalf("port = %d, want 4010", got)
	}
}
