package main

import (
	"os"
	"regexp"
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

	darwinBranch := regexp.MustCompile(`else\s+if\s+runtime\.GOOS\s*==\s*"darwin"\s*\{\s*trayIcon\s*=\s*trayIconMacOS\s*\}`)
	if !darwinBranch.MatchString(mainSource) {
		t.Error(`main.go must select trayIconMacOS in an else if runtime.GOOS == "darwin" branch`)
	}
}
