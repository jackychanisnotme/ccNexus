package main

import (
	"os"
	"strings"
	"testing"
)

func TestMainUsesDedicatedMacOSTrayIcon(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	mainSource := string(source)
	if !strings.Contains(mainSource, "//go:embed build/trayicon-macos.png") {
		t.Error("main.go must embed build/trayicon-macos.png for the macOS tray")
	}
	if !strings.Contains(mainSource, `runtime.GOOS == "darwin"`) {
		t.Error("main.go must select the macOS tray icon using runtime.GOOS")
	}
}
