package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminHTMLScriptsParse(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to parse inline admin scripts")
	}
	for name, html := range map[string]string{
		"login": loginHTML,
		"admin": adminHTML,
	} {
		script := scriptFromHTML(t, html)
		path := filepath.Join(t.TempDir(), name+".js")
		if err := os.WriteFile(path, []byte(script), 0600); err != nil {
			t.Fatalf("write script: %v", err)
		}
		cmd := exec.Command(nodePath, "--check", path)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%s inline script does not parse: %v\n%s", name, err, output)
		}
	}
}

func TestAdminHTMLHasModuleNavigation(t *testing.T) {
	for _, item := range []struct {
		id    string
		label string
	}{
		{"generate", "生成卡密"},
		{"cards", "卡密"},
		{"accounts", "后台账号"},
		{"devices", "设备授权"},
		{"history", "历史记录"},
	} {
		button := `data-page-target="` + item.id + `"`
		if !strings.Contains(adminHTML, button) {
			t.Fatalf("admin page missing navigation button %s for %s", button, item.label)
		}
		section := `data-page="` + item.id + `"`
		if !strings.Contains(adminHTML, section) {
			t.Fatalf("admin page missing module section %s for %s", section, item.label)
		}
	}
	if !strings.Contains(adminHTML, "<th>归属</th><th>设备ID</th>") {
		t.Fatalf("devices table must include owner column before device id")
	}
}

func scriptFromHTML(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, "<script>")
	end := strings.LastIndex(html, "</script>")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("script tag not found")
	}
	return html[start+len("<script>") : end]
}
