package service

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
)

func TestAgentProviderApplyBacksUpAndRestoresDetectedConfigs(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3456)
	svc := NewAgentProviderServiceWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{"permissions":{"allow_file_access":true},"env":{"ANTHROPIC_BASE_URL":"https://api.anthropic.com","ANTHROPIC_API_KEY":"old-key"}}`)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"), `{"OPENAI_API_KEY":"old-key"}`)
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), "model = \"gpt-4\"\nbase_url = \"https://api.openai.com/v1\"\n")
	writeFile(t, filepath.Join(home, ".gemini", ".env"), "GEMINI_MODEL=gemini-pro\nGEMINI_API_KEY=old-key\n")
	writeFile(t, filepath.Join(home, ".config", "opencode", "opencode.json"), `{"$schema":"https://opencode.ai/config.json","theme":"dark"}`)
	writeFile(t, filepath.Join(home, ".openclaw", "openclaw.json"), `{models:{mode:"merge",providers:{existing:{baseUrl:"https://old.example",apiKey:"old"}}},agents:{defaults:{model:{primary:"existing/old"}}}}`)
	writeFile(t, filepath.Join(home, ".hermes", "config.yaml"), "agent:\n  max_turns: 50\nmodel:\n  provider: old\n  base_url: https://old.example\n")

	result := svc.Apply(AgentProviderRequest{
		Targets: []string{"claude", "codex", "gemini", "opencode", "openclaw", "hermes"},
	})
	if result.BackupID == "" {
		t.Fatal("expected backup id")
	}
	assertTargetStatus(t, result.Results, "claude", "success")
	assertTargetStatus(t, result.Results, "codex", "success")
	assertTargetStatus(t, result.Results, "gemini", "success")
	assertTargetStatus(t, result.Results, "opencode", "success")
	assertTargetStatus(t, result.Results, "openclaw", "success")
	assertTargetStatus(t, result.Results, "hermes", "success")

	claude := readFile(t, filepath.Join(home, ".claude", "settings.json"))
	if !strings.Contains(claude, `"ANTHROPIC_BASE_URL": "http://127.0.0.1:3456"`) || !strings.Contains(claude, `"allow_file_access": true`) {
		t.Fatalf("claude config not updated/preserved:\n%s", claude)
	}
	codex := readFile(t, filepath.Join(home, ".codex", "config.toml"))
	if !strings.Contains(codex, `model_provider = "AINexus"`) || !strings.Contains(codex, `base_url = "http://127.0.0.1:3456/v1"`) {
		t.Fatalf("codex config missing AINexus provider:\n%s", codex)
	}
	gemini := readFile(t, filepath.Join(home, ".gemini", ".env"))
	if !strings.Contains(gemini, "GOOGLE_GEMINI_BASE_URL=http://127.0.0.1:3456") || !strings.Contains(gemini, "GEMINI_MODEL=gemini-pro") {
		t.Fatalf("gemini env not updated/preserved:\n%s", gemini)
	}
	opencode := readJSON(t, filepath.Join(home, ".config", "opencode", "opencode.json"))
	if got := opencode["theme"]; got != "dark" {
		t.Fatalf("expected opencode theme preserved, got %#v", got)
	}
	openclaw := readJSON(t, filepath.Join(home, ".openclaw", "openclaw.json"))
	openclawProvider, ok := openclaw["models"].(map[string]any)["providers"].(map[string]any)["AINexus"].(map[string]any)
	if !ok {
		t.Fatalf("expected openclaw AINexus provider, got %#v", openclaw)
	}
	if got := openclawProvider["baseUrl"]; got != "http://127.0.0.1:3456" {
		t.Fatalf("expected openclaw root baseUrl, got %#v", got)
	}
	hermes := readFile(t, filepath.Join(home, ".hermes", "config.yaml"))
	if !strings.Contains(hermes, "provider: AINexus") || !strings.Contains(hermes, "base_url: http://127.0.0.1:3456") {
		t.Fatalf("hermes yaml not updated:\n%s", hermes)
	}

	manifestPath := filepath.Join(home, ".AINexus", "agent-provider-backups", result.BackupID, "manifest.json")
	manifest := readFile(t, manifestPath)
	if !strings.Contains(manifest, `"target": "claude"`) || !strings.Contains(manifest, `"target": "codex"`) {
		t.Fatalf("manifest missing target backups:\n%s", manifest)
	}

	restore := svc.Restore(AgentProviderRestoreRequest{
		BackupID: result.BackupID,
		Targets:  []string{"claude", "codex", "gemini", "opencode", "openclaw", "hermes"},
	})
	assertTargetStatus(t, restore.Results, "claude", "restored")
	assertTargetStatus(t, restore.Results, "codex", "restored")
	if got := readFile(t, filepath.Join(home, ".claude", "settings.json")); !strings.Contains(got, "https://api.anthropic.com") {
		t.Fatalf("claude config was not restored:\n%s", got)
	}
	if got := readFile(t, filepath.Join(home, ".codex", "config.toml")); !strings.Contains(got, `base_url = "https://api.openai.com/v1"`) {
		t.Fatalf("codex config was not restored:\n%s", got)
	}
	if got := readFile(t, filepath.Join(home, ".codex", "auth.json")); got != `{"OPENAI_API_KEY":"old-key"}` {
		t.Fatalf("codex auth was not restored:\n%s", got)
	}
}

func TestAgentProviderCodexApplyPreservesExistingConfig(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3456)
	svc := NewAgentProviderServiceWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	writeFile(t, filepath.Join(home, ".codex", "config.toml"), `# keep codex comment
model = "gpt-4.1"
approval_policy = "on-request"
sandbox_mode = "workspace-write"

[profiles.work]
model = "gpt-4.1"

[mcp_servers.fetch]
command = "uvx"
args = ["mcp-server-fetch"]
`)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"), `{
  "OPENAI_API_KEY": "old-key",
  "tokens": {"access_token": "oauth-token"},
  "last_refresh": "2026-01-01T00:00:00Z"
}`)

	result := svc.Apply(AgentProviderRequest{Targets: []string{"codex"}})
	assertTargetStatus(t, result.Results, "codex", "success")

	codex := readFile(t, filepath.Join(home, ".codex", "config.toml"))
	for _, want := range []string{
		"# keep codex comment",
		`model = "gpt-4.1"`,
		`approval_policy = "on-request"`,
		`sandbox_mode = "workspace-write"`,
		"[profiles.work]",
		"[mcp_servers.fetch]",
		`model_provider = "AINexus"`,
		`[model_providers.AINexus]`,
		`base_url = "http://127.0.0.1:3456/v1"`,
	} {
		if !strings.Contains(codex, want) {
			t.Fatalf("codex config lost %q:\n%s", want, codex)
		}
	}

	auth := readFile(t, filepath.Join(home, ".codex", "auth.json"))
	for _, want := range []string{`"OPENAI_API_KEY": "ainexus-local"`, `"access_token": "oauth-token"`, `"last_refresh": "2026-01-01T00:00:00Z"`} {
		if !strings.Contains(auth, want) {
			t.Fatalf("codex auth lost %q:\n%s", want, auth)
		}
	}
}

func TestAgentProviderSkipsMissingConfigsByDefault(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	svc := NewAgentProviderServiceWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	result := svc.Apply(AgentProviderRequest{Targets: []string{"claude", "codex"}})
	assertTargetStatus(t, result.Results, "claude", "skipped")
	assertTargetStatus(t, result.Results, "codex", "skipped")
	if result.BackupID != "" {
		t.Fatalf("expected no backup for all-skipped apply, got %q", result.BackupID)
	}
}

func TestAgentProviderClaudeDesktopUsesAINexusProfileAndLegacyFallback(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	svc := NewAgentProviderServiceWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	wantNew := filepath.Join(home, "Library", "Application Support", "Claude-3p", "configLibrary", "ainexus.json")
	if got := svc.claudeDesktopProfilePath(); got != wantNew {
		t.Fatalf("claudeDesktopProfilePath() = %q, want %q", got, wantNew)
	}

	legacy := filepath.Join(home, "Library", "Application Support", "Claude-3p", "configLibrary", "ccnexus.json")
	writeFile(t, legacy, `{"name":"ccNexus"}`)
	if got := svc.claudeDesktopProfilePath(); got != legacy {
		t.Fatalf("claudeDesktopProfilePath() = %q, want legacy %q", got, legacy)
	}
}

func TestAgentProviderApplyCreatesAINexusClaudeDesktopProfile(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3456)
	svc := NewAgentProviderServiceWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	result := svc.Apply(AgentProviderRequest{Targets: []string{"claude_desktop"}, CreateMissing: true})
	assertTargetStatus(t, result.Results, "claude_desktop", "success")

	path := filepath.Join(home, "Library", "Application Support", "Claude-3p", "configLibrary", "ainexus.json")
	if got := readFile(t, path); !strings.Contains(got, `"name": "AINexus"`) {
		t.Fatalf("expected AINexus profile, got:\n%s", got)
	}
}

func TestAgentProviderRestoreKeepsBackupWhenApplyPartiallyFails(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	cfg := config.DefaultConfig()
	svc := NewAgentProviderServiceWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: dataDir,
	})

	configPath := filepath.Join(home, ".codex", "config.toml")
	authPath := filepath.Join(home, ".codex", "auth.json")
	writeFile(t, configPath, "model = \"gpt-4\"\n")
	writeFile(t, authPath, `{"OPENAI_API_KEY":"old-key"}`)
	if err := os.Chmod(filepath.Dir(configPath), 0555); err != nil {
		t.Fatalf("chmod codex dir readonly: %v", err)
	}
	defer func() {
		_ = os.Chmod(filepath.Dir(configPath), 0755)
	}()

	result := svc.Apply(AgentProviderRequest{Targets: []string{"codex"}})
	assertTargetStatus(t, result.Results, "codex", "failed")
	if result.BackupID == "" {
		t.Fatalf("expected backup id for failed partial apply, got %#v", result)
	}

	if err := os.Chmod(filepath.Dir(configPath), 0755); err != nil {
		t.Fatalf("restore codex dir permissions: %v", err)
	}
	restore := svc.Restore(AgentProviderRestoreRequest{BackupID: result.BackupID, Targets: []string{"codex"}})
	assertTargetStatus(t, restore.Results, "codex", "restored")
	if got := readFile(t, configPath); got != "model = \"gpt-4\"\n" {
		t.Fatalf("codex config was not restored:\n%s", got)
	}
	if got := readFile(t, authPath); got != `{"OPENAI_API_KEY":"old-key"}` {
		t.Fatalf("codex auth was not restored:\n%s", got)
	}
}

func TestAgentProviderInvalidJSONFailsWithoutWriting(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	svc := NewAgentProviderServiceWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})
	path := filepath.Join(home, ".claude", "settings.json")
	writeFile(t, path, `{"env":`)

	result := svc.Apply(AgentProviderRequest{Targets: []string{"claude"}})
	assertTargetStatus(t, result.Results, "claude", "failed")
	if got := readFile(t, path); got != `{"env":` {
		t.Fatalf("invalid config should not be overwritten, got %q", got)
	}
}

func TestNormalizeAgentTargetIDAliases(t *testing.T) {
	tests := map[string]string{
		"claude-code":  "claude",
		"gemini_cli":   "gemini",
		"opencode_cli": "opencode",
		"openclaw_cli": "openclaw",
	}
	for input, want := range tests {
		if got := normalizeAgentTargetID(input); got != want {
			t.Fatalf("normalizeAgentTargetID(%q) = %q, want %q", input, got, want)
		}
	}
}

func assertTargetStatus(t *testing.T, results []AgentProviderTargetResult, target, status string) {
	t.Helper()
	for _, result := range results {
		if result.Target == target {
			if result.Status != status {
				t.Fatalf("target %s status=%s want %s result=%#v", target, result.Status, status, result)
			}
			return
		}
	}
	t.Fatalf("target %s not found in results %#v", target, results)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	var data map[string]any
	if err := json.Unmarshal([]byte(readFile(t, path)), &data); err != nil {
		t.Fatalf("decode json %s: %v", path, err)
	}
	return data
}
