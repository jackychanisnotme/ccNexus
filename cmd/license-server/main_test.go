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
		{"ai", "AI 分析"},
		{"reports", "客户报告"},
		{"ai-settings", "AI 设置"},
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
	if !strings.Contains(adminHTML, "批量可视") {
		t.Fatalf("devices page missing bulk visibility toggle")
	}
	if !strings.Contains(adminHTML, "toggleDevicePrivacy(") {
		t.Fatalf("devices page missing per-row privacy toggle")
	}
	if !strings.Contains(adminHTML, "toggleAllDevicePrivacy(") {
		t.Fatalf("devices page missing bulk privacy toggle handler")
	}
	if !strings.Contains(adminHTML, "privateValue(") {
		t.Fatalf("devices page missing masking helper")
	}
	if !strings.Contains(adminHTML, "**") {
		t.Fatalf("devices page missing default masked placeholder")
	}
	for _, marker := range []string{
		"/api/admin/ai/settings",
		"/api/admin/ai/suppliers/summary",
		"/api/admin/ai/reports/generate",
		"https://ainexus.wenche.xyz/ui/",
		"AI 运维概览",
		"AI 高级设置",
		"今日结论",
		"建议处理",
		"查看并打印",
		"更多格式",
		`data-permission="ai:analysis:view"`,
		`data-permission="ai:reports:view"`,
	} {
		if !strings.Contains(adminHTML, marker) {
			t.Fatalf("admin page missing AI operations marker %s", marker)
		}
	}
}

func TestAdminAIPureHelpers(t *testing.T) {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is required to test admin AI helpers")
	}
	script := scriptFromHTML(t, adminHTML)
	helpers := []string{
		javascriptFunction(t, script, "aiClassificationName"),
		javascriptFunction(t, script, "aiSeverityName"),
		javascriptFunction(t, script, "aiActionableFinding"),
		javascriptFunction(t, script, "aggregateAIFindings"),
		javascriptFunction(t, script, "aiDashboardState"),
	}
	testScript := strings.Join(helpers, "\n") + `
const assert = require("node:assert/strict");
assert.equal(aiClassificationName("supplier_issue"), "疑似供应商问题");
assert.equal(aiSeverityName("high"), "优先处理");

const grouped = aggregateAIFindings([
  {classification:"customer_network", ownerAccountId:2, apiHost:"b.example", severity:"medium", count:2, createdAt:"2026-07-13T01:00:00Z"},
  {classification:"supplier_issue", ownerAccountId:1, apiHost:"a.example", severity:"high", count:3, createdAt:"2026-07-13T02:00:00Z"},
  {classification:"supplier_issue", ownerAccountId:1, apiHost:"a.example", severity:"medium", count:4, createdAt:"2026-07-13T03:00:00Z"},
]);
assert.equal(grouped.length, 2);
assert.equal(grouped[0].classification, "supplier_issue");
assert.equal(grouped[0].count, 7);
assert.equal(grouped[0].severity, "high");

const configured = {enabled:true, model:"analysis-model"};
assert.equal(aiDashboardState({enabled:false, model:""}, [], [], []).key, "unconfigured");
assert.equal(aiDashboardState(configured, [{apiHost:"a"}], [], [{status:"running"}]).key, "running");
assert.equal(aiDashboardState(configured, [], [], []).key, "collecting");
assert.equal(aiDashboardState(configured, [{apiHost:"a"}], [{classification:"supplier_issue", ownerAccountId:2, apiHost:"a"}], []).key, "attention");
assert.equal(aiDashboardState(configured, [{apiHost:"a"}], [{classification:"unknown", ownerAccountId:2, apiHost:"a"}], []).key, "unknown");
assert.equal(aiDashboardState(configured, [{apiHost:"a"}], [], []).key, "normal");
assert.equal(aiDashboardState(configured, [{apiHost:"a"}], [], [{status:"skipped_no_ai_provider"}]).key, "unavailable");
`
	path := filepath.Join(t.TempDir(), "admin-ai-helpers.js")
	if err := os.WriteFile(path, []byte(testScript), 0600); err != nil {
		t.Fatalf("write AI helper test: %v", err)
	}
	cmd := exec.Command(nodePath, path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("admin AI helper test failed: %v\n%s", err, output)
	}
}

func TestAdminHTMLHasInvestorReadyShell(t *testing.T) {
	for _, expected := range []string{
		`class="admin-shell"`,
		`class="sidebar"`,
		`id="overview-cards"`,
		`renderOverview()`,
		`class="generate-grid"`,
	} {
		if !strings.Contains(adminHTML, expected) {
			t.Fatalf("admin page missing redesigned shell marker %s", expected)
		}
	}
}

func TestLoginHTMLHasPolishedLoginCard(t *testing.T) {
	for _, expected := range []string{
		`class="login-page"`,
		`class="login-card"`,
		`id="login-form"`,
		`id="username"`,
		`id="password"`,
	} {
		if !strings.Contains(loginHTML, expected) {
			t.Fatalf("login page missing polished login marker %s", expected)
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

func javascriptFunction(t *testing.T, script, name string) string {
	t.Helper()
	start := strings.Index(script, "function "+name+"(")
	if start < 0 {
		t.Fatalf("javascript function %s not found", name)
	}
	brace := strings.Index(script[start:], "{")
	if brace < 0 {
		t.Fatalf("javascript function %s opening brace not found", name)
	}
	brace += start
	depth := 0
	quote := byte(0)
	escaped := false
	for index := brace; index < len(script); index++ {
		char := script[index]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == quote {
				quote = 0
			}
			continue
		}
		if char == '\'' || char == '"' || char == '`' {
			quote = char
			continue
		}
		switch char {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return script[start : index+1]
			}
		}
	}
	t.Fatalf("javascript function %s closing brace not found", name)
	return ""
}
