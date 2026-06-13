package main

import (
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
)

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
