package tray

import (
	"os"
	"strings"
	"testing"
)

func TestDarwinTrayIconUsesTemplateRendering(t *testing.T) {
	source, err := os.ReadFile("tray_darwin.m")
	if err != nil {
		t.Fatalf("read tray_darwin.m: %v", err)
	}

	if !strings.Contains(string(source), "[icon setTemplate:YES]") {
		t.Error("tray_darwin.m must mark the menu bar icon as an NSImage template")
	}
}
