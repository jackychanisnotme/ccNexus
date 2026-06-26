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

func scriptFromHTML(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, "<script>")
	end := strings.LastIndex(html, "</script>")
	if start < 0 || end < 0 || end <= start {
		t.Fatalf("script tag not found")
	}
	return html[start+len("<script>") : end]
}
