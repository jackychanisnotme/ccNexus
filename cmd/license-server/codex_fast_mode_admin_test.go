package main

import (
	"os"
	"strings"
	"testing"
)

func TestAdminRemoteEndpointCodexFastModeControls(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(source)
	for _, want := range []string{
		"ep.codexFastMode?'开启':'关闭'",
		"remoteSetCodexFastMode(",
		"codexFastMode:enabled",
		"const codexFastMode=authMode==='codex_token_pool'&&confirm",
		"codexFastMode})",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("admin remote endpoint UI missing %q", want)
		}
	}
	if strings.Contains(body, "1.5倍") || strings.Contains(body, "1.5x") {
		t.Fatalf("admin copy must not hard-code a 1.5x fast-mode multiplier")
	}
}
