package service

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/config"
)

func TestAgentProviderInspectorReportsHealthyCodexOpenClawAndHermes(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3456)
	provider := NewAgentProviderServiceWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})
	apply := provider.Apply(AgentProviderRequest{
		Targets:       []string{"codex", "openclaw", "hermes"},
		CreateMissing: true,
	})
	assertTargetStatus(t, apply.Results, "codex", "success")
	assertTargetStatus(t, apply.Results, "openclaw", "success")
	assertTargetStatus(t, apply.Results, "hermes", "success")

	inspector := NewAgentProviderInspectorWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})
	status := inspector.Inspect(AgentProviderInspectRequest{Targets: []string{"codex", "openclaw", "hermes"}})

	assertInspectHealthy(t, status.Targets, "codex")
	assertInspectHealthy(t, status.Targets, "openclaw")
	assertInspectHealthy(t, status.Targets, "hermes")
	if status.TargetURL != "http://127.0.0.1:3456" {
		t.Fatalf("TargetURL=%q", status.TargetURL)
	}
}

func TestAgentProviderInspectorReportsMissingAndBrokenConfigs(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(4567)
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), `model_provider = "Other"`)
	writeFile(t, filepath.Join(home, ".openclaw", "openclaw.json"), `{not json`)
	writeFile(t, filepath.Join(home, ".hermes", "config.yaml"), `model: [`)

	inspector := NewAgentProviderInspectorWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})
	status := inspector.Inspect(AgentProviderInspectRequest{Targets: []string{"codex", "openclaw", "hermes"}})

	codex := inspectTarget(t, status.Targets, "codex")
	if codex.Healthy || len(codex.Problems) == 0 {
		t.Fatalf("expected unhealthy codex with problems, got %#v", codex)
	}
	openclaw := inspectTarget(t, status.Targets, "openclaw")
	if openclaw.Status != "parse_failed" || !strings.Contains(strings.Join(openclaw.Problems, "\n"), "parse") {
		t.Fatalf("expected parse_failed openclaw, got %#v", openclaw)
	}
	hermes := inspectTarget(t, status.Targets, "hermes")
	if hermes.Status != "parse_failed" {
		t.Fatalf("expected parse_failed hermes, got %#v", hermes)
	}
}

func TestAgentProviderInspectorOpenClawRootBaseURLIsHealthy(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3000)
	writeFile(t, filepath.Join(home, ".openclaw", "openclaw.json"), `{
		"models": {
			"mode": "merge",
			"providers": {
				"AINexus": {
					"baseUrl": "http://127.0.0.1:3000",
					"apiKey": "ainexus-local",
					"api": "openai-completions",
					"models": [{"id":"gpt-5","name":"gpt-5"}]
				}
			}
		},
		"agents": {
			"defaults": {
				"model": {"primary": "gpt-5"}
			}
		}
	}`)
	inspector := NewAgentProviderInspectorWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	status := inspector.Inspect(AgentProviderInspectRequest{Targets: []string{"openclaw"}})

	assertInspectHealthy(t, status.Targets, "openclaw")
}

func TestAgentProviderInspectorCodexUsesV1BaseURL(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3000)
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), `model_provider = "AINexus"
model = "gpt-5"

[model_providers.AINexus]
name = "AINexus"
base_url = "http://127.0.0.1:3000/v1"
wire_api = "responses"
experimental_bearer_token = "ainexus-local"
`)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"), `{"OPENAI_API_KEY":"ainexus-local"}`)

	inspector := NewAgentProviderInspectorWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	status := inspector.Inspect(AgentProviderInspectRequest{Targets: []string{"codex"}})

	assertInspectHealthy(t, status.Targets, "codex")
}

func TestAgentProviderInspectorCodexCustomProviderRootBaseURLIsHealthy(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3000)
	writeFile(t, filepath.Join(home, ".codex", "config.toml"), `model = "gpt-5.5"
model_provider = "openai-custom"

[model_providers.openai-custom]
name = "OpenAI Custom"
base_url = "http://127.0.0.1:3000"
supports_websockets = false
requires_openai_auth = false
`)
	writeFile(t, filepath.Join(home, ".codex", "auth.json"), `{"OPENAI_API_KEY":"ainexus-local"}`)

	inspector := NewAgentProviderInspectorWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	status := inspector.Inspect(AgentProviderInspectRequest{Targets: []string{"codex"}})

	assertInspectHealthy(t, status.Targets, "codex")
}

func TestAgentProviderInspectorHermesUsesRootModelURLAndV1ProviderURL(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3000)
	writeFile(t, filepath.Join(home, ".hermes", "config.yaml"), `model:
  provider: AINexus
  base_url: http://127.0.0.1:3000
  default: gpt-5
custom_providers:
  - name: AINexus
    base_url: http://127.0.0.1:3000/v1
    api_key: ainexus-local
    model: gpt-5
`)

	inspector := NewAgentProviderInspectorWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	status := inspector.Inspect(AgentProviderInspectRequest{Targets: []string{"hermes"}})

	assertInspectHealthy(t, status.Targets, "hermes")
}

func TestAgentProviderInspectorHermesCustomProviderRootModelURLIsHealthy(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3000)
	writeFile(t, filepath.Join(home, ".hermes", "config.yaml"), `model:
  default: gpt-5.5
  provider: custom
  base_url: http://127.0.0.1:3000
  api_key: ainexus-local
providers:
  - provider: custom-hk
    model: gpt-5.5
    base_url: https://example.invalid/v1
`)

	inspector := NewAgentProviderInspectorWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	status := inspector.Inspect(AgentProviderInspectRequest{Targets: []string{"hermes"}})

	assertInspectHealthy(t, status.Targets, "hermes")
}

func TestAgentProviderInspectorClaudeCodeRootBaseURLIsHealthy(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.UpdatePort(3000)
	writeFile(t, filepath.Join(home, ".claude", "settings.json"), `{
		"env": {
			"ANTHROPIC_BASE_URL": "http://127.0.0.1:3000",
			"ANTHROPIC_API_KEY": "ainexus-local"
		}
	}`)

	inspector := NewAgentProviderInspectorWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	status := inspector.Inspect(AgentProviderInspectRequest{Targets: []string{"claude"}})

	assertInspectHealthy(t, status.Targets, "claude")
}

func TestAgentProviderInspectorMissingConfigsAreNotHealthy(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig()
	inspector := NewAgentProviderInspectorWithOptions(cfg, AgentProviderOptions{
		HomeDir: home,
		DataDir: filepath.Join(home, ".AINexus"),
	})

	status := inspector.Inspect(AgentProviderInspectRequest{Targets: []string{"codex", "openclaw", "hermes"}})
	for _, target := range []string{"codex", "openclaw", "hermes"} {
		item := inspectTarget(t, status.Targets, target)
		if item.Detected || item.Healthy || item.Status != "missing" {
			t.Fatalf("%s expected missing unhealthy, got %#v", target, item)
		}
	}
}

func assertInspectHealthy(t *testing.T, results []AgentProviderInspectTarget, target string) {
	t.Helper()
	item := inspectTarget(t, results, target)
	if !item.Detected || !item.Healthy || item.Status != "healthy" || len(item.Problems) != 0 {
		t.Fatalf("%s expected healthy, got %#v", target, item)
	}
}

func inspectTarget(t *testing.T, results []AgentProviderInspectTarget, target string) AgentProviderInspectTarget {
	t.Helper()
	for _, result := range results {
		if result.Target == target {
			return result
		}
	}
	t.Fatalf("target %s not found in %#v", target, results)
	return AgentProviderInspectTarget{}
}
