package main

import (
	"os"
	"regexp"
	"testing"
)

func TestMainUsesDedicatedMacOSTrayIcon(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}

	mainSource := string(source)
	macOSEmbed := regexp.MustCompile(`(?m)^//go:embed build/trayicon-macos\.png\s*\n\s*var\s+trayIconMacOS\s+\[\]byte\s*$`)
	if !macOSEmbed.MatchString(mainSource) {
		t.Error("main.go must embed build/trayicon-macos.png into trayIconMacOS")
	}

	darwinBranch := regexp.MustCompile(`else\s+if\s+runtime\.GOOS\s*==\s*"darwin"\s*\{\s*trayIcon\s*=\s*trayIconMacOS\s*\}`)
	if !darwinBranch.MatchString(mainSource) {
		t.Error(`main.go must select trayIconMacOS in an else if runtime.GOOS == "darwin" branch`)
	}
}
