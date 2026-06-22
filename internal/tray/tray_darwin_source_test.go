package tray

import (
	"os"
	"regexp"
	"testing"
)

func TestDarwinTrayIconUsesTemplateRendering(t *testing.T) {
	source, err := os.ReadFile("tray_darwin.m")
	if err != nil {
		t.Fatalf("read tray_darwin.m: %v", err)
	}

	iconInitialization := regexp.MustCompile(`(?m)^[ \t]*NSImage\s*\*\s*icon\s*=\s*\[\[NSImage\s+alloc\]\s+initWithData:iconData\];[ \t]*$`).FindIndex(source)
	templateRendering := regexp.MustCompile(`(?m)^[ \t]*\[icon\s+setTemplate:YES\];[ \t]*$`).FindIndex(source)
	iconAssignment := regexp.MustCompile(`(?m)^[ \t]*\[self\.statusItem\.button\s+setImage:icon\];[ \t]*$`).FindIndex(source)

	if iconInitialization == nil {
		t.Fatal("tray_darwin.m must initialize icon from iconData")
	}
	if templateRendering == nil {
		t.Fatal("tray_darwin.m must mark icon as an NSImage template")
	}
	if iconAssignment == nil {
		t.Fatal("tray_darwin.m must assign icon to the status item button")
	}
	if templateRendering[0] <= iconInitialization[1] || templateRendering[1] >= iconAssignment[0] {
		t.Error("tray_darwin.m must set template rendering after icon initialization and before assigning the icon")
	}
}
