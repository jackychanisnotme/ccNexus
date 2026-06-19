package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lich0821/ccNexus/internal/config"
	"gopkg.in/yaml.v3"
)

type AgentProviderInspectRequest struct {
	Targets []string `json:"targets,omitempty"`
}

type AgentProviderInspectStatus struct {
	TargetURL string                       `json:"targetUrl"`
	Targets   []AgentProviderInspectTarget `json:"targets"`
}

type AgentProviderInspectTarget struct {
	Target       string   `json:"target"`
	Label        string   `json:"label"`
	Path         string   `json:"path"`
	Detected     bool     `json:"detected"`
	Healthy      bool     `json:"healthy"`
	Status       string   `json:"status"`
	Problems     []string `json:"problems,omitempty"`
	SuggestedFix string   `json:"suggestedFix,omitempty"`
}

type AgentProviderInspector struct {
	provider *AgentProviderService
}

func NewAgentProviderInspector(cfg *config.Config) *AgentProviderInspector {
	return &AgentProviderInspector{provider: NewAgentProviderService(cfg)}
}

func NewAgentProviderInspectorWithOptions(cfg *config.Config, options AgentProviderOptions) *AgentProviderInspector {
	return &AgentProviderInspector{provider: NewAgentProviderServiceWithOptions(cfg, options)}
}

func (i *AgentProviderInspector) Inspect(req AgentProviderInspectRequest) AgentProviderInspectStatus {
	status := AgentProviderInspectStatus{}
	if i == nil || i.provider == nil {
		status.Targets = append(status.Targets, unsupportedInspectTarget(""))
		return status
	}
	status.TargetURL = i.provider.targetURL()
	targets := req.Targets
	if len(targets) == 0 {
		targets = []string{"codex", "openclaw", "hermes"}
	}
	for _, target := range targets {
		switch normalizeAgentTargetID(target) {
		case "claude":
			status.Targets = append(status.Targets, i.inspectClaudeCode())
		case "codex":
			status.Targets = append(status.Targets, i.inspectCodex())
		case "openclaw":
			status.Targets = append(status.Targets, i.inspectOpenClaw())
		case "hermes":
			status.Targets = append(status.Targets, i.inspectHermes())
		default:
			status.Targets = append(status.Targets, unsupportedInspectTarget(target))
		}
	}
	return status
}

func (i *AgentProviderInspector) InspectJSON(targetsJSON string) string {
	var req AgentProviderInspectRequest
	if strings.TrimSpace(targetsJSON) != "" {
		if err := json.Unmarshal([]byte(targetsJSON), &req); err != nil {
			data, _ := json.Marshal(AgentProviderInspectStatus{Targets: []AgentProviderInspectTarget{{
				Status:       "failed",
				Problems:     []string{fmt.Sprintf("invalid request: %v", err)},
				SuggestedFix: "Send a JSON object with an optional targets array.",
			}}})
			return string(data)
		}
	}
	data, _ := json.Marshal(i.Inspect(req))
	return string(data)
}

func (i *AgentProviderInspector) inspectClaudeCode() AgentProviderInspectTarget {
	path := filepath.Join(i.provider.homeDir, ".claude", "settings.json")
	target := newInspectTarget("claude", "Claude", path)
	if !fileExists(path) {
		return missingInspectTarget(target)
	}
	target.Detected = true
	root := map[string]any{}
	if err := readJSONFile(path, &root, map[string]any{}); err != nil {
		target.Status = "parse_failed"
		target.Problems = append(target.Problems, "parse settings.json: "+err.Error())
		target.SuggestedFix = "Repair Claude config through AINexus."
		return target
	}
	env, _ := root["env"].(map[string]any)
	if env == nil {
		target.Problems = append(target.Problems, "missing env section")
	} else {
		addProblemIfMissing(&target, agentBaseURLMatches(fmt.Sprint(env["ANTHROPIC_BASE_URL"]), i.provider.targetURL()), "ANTHROPIC_BASE_URL does not point to AINexus")
	}
	finalizeInspectTarget(&target)
	return target
}

func (i *AgentProviderInspector) inspectCodex() AgentProviderInspectTarget {
	configPath := filepath.Join(i.provider.homeDir, ".codex", "config.toml")
	authPath := filepath.Join(i.provider.homeDir, ".codex", "auth.json")
	target := newInspectTarget("codex", "Codex", configPath)
	if !fileExists(configPath) {
		return missingInspectTarget(target)
	}
	target.Detected = true
	data, err := os.ReadFile(configPath)
	if err != nil {
		return failedInspectTarget(target, err)
	}
	text := string(data)
	codexConfig := parseSimpleTOML(text)
	addProblemIfMissing(&target, codexConfig.activeProviderBaseURLMatches(i.provider.targetURL()), "active model provider base_url does not point to AINexus")

	auth := map[string]any{}
	if !fileExists(authPath) {
		target.Problems = append(target.Problems, "missing auth.json")
	} else if err := readJSONFile(authPath, &auth, map[string]any{}); err != nil {
		target.Status = "parse_failed"
		target.Problems = append(target.Problems, "parse auth.json: "+err.Error())
		target.SuggestedFix = "Repair Codex config through AINexus."
		return target
	} else if strings.TrimSpace(fmt.Sprint(auth["OPENAI_API_KEY"])) == "" {
		target.Problems = append(target.Problems, "missing OPENAI_API_KEY")
	}
	finalizeInspectTarget(&target)
	return target
}

func (i *AgentProviderInspector) inspectOpenClaw() AgentProviderInspectTarget {
	path := filepath.Join(i.provider.homeDir, ".openclaw", "openclaw.json")
	target := newInspectTarget("openclaw", "OpenClaw", path)
	if !fileExists(path) {
		return missingInspectTarget(target)
	}
	target.Detected = true
	root, err := readLooseJSONFile(path)
	if err != nil {
		target.Status = "parse_failed"
		target.Problems = append(target.Problems, "parse openclaw.json: "+err.Error())
		target.SuggestedFix = "Repair OpenClaw config through AINexus."
		return target
	}
	models, _ := root["models"].(map[string]any)
	providers, _ := models["providers"].(map[string]any)
	provider, _ := providers["AINexus"].(map[string]any)
	if provider == nil {
		target.Problems = append(target.Problems, "missing models.providers.AINexus")
	} else {
		addProblemIfMissing(&target, agentBaseURLMatches(fmt.Sprint(provider["baseUrl"]), i.provider.targetURL()), "baseUrl does not point to AINexus")
		addProblemIfMissing(&target, strings.TrimSpace(fmt.Sprint(provider["apiKey"])) != "", "missing apiKey")
	}
	finalizeInspectTarget(&target)
	return target
}

func (i *AgentProviderInspector) inspectHermes() AgentProviderInspectTarget {
	path := filepath.Join(i.provider.homeDir, ".hermes", "config.yaml")
	target := newInspectTarget("hermes", "Hermes", path)
	if !fileExists(path) {
		return missingInspectTarget(target)
	}
	target.Detected = true
	data, err := os.ReadFile(path)
	if err != nil {
		return failedInspectTarget(target, err)
	}
	root := map[string]any{}
	if strings.TrimSpace(string(data)) != "" {
		if err := yaml.Unmarshal(data, &root); err != nil {
			target.Status = "parse_failed"
			target.Problems = append(target.Problems, "parse config.yaml: "+err.Error())
			target.SuggestedFix = "Repair Hermes config through AINexus."
			return target
		}
	}
	model, _ := root["model"].(map[string]any)
	if model == nil {
		target.Problems = append(target.Problems, "missing model section")
	} else {
		addProblemIfMissing(&target, agentBaseURLMatches(fmt.Sprint(model["base_url"]), i.provider.targetURL()), "model.base_url does not point to AINexus")
	}
	finalizeInspectTarget(&target)
	return target
}

func newInspectTarget(id, label, path string) AgentProviderInspectTarget {
	return AgentProviderInspectTarget{Target: id, Label: label, Path: path}
}

func missingInspectTarget(target AgentProviderInspectTarget) AgentProviderInspectTarget {
	target.Status = "missing"
	target.SuggestedFix = "Create or repair this config through AINexus."
	return target
}

func failedInspectTarget(target AgentProviderInspectTarget, err error) AgentProviderInspectTarget {
	target.Detected = true
	target.Status = "failed"
	target.Problems = []string{err.Error()}
	target.SuggestedFix = "Repair this config through AINexus."
	return target
}

func unsupportedInspectTarget(target string) AgentProviderInspectTarget {
	return AgentProviderInspectTarget{
		Target:       strings.TrimSpace(target),
		Label:        strings.TrimSpace(target),
		Status:       "unsupported",
		Problems:     []string{"unsupported target"},
		SuggestedFix: "Select Codex, OpenClaw, or Hermes.",
	}
}

func addProblemIfMissing(target *AgentProviderInspectTarget, ok bool, problem string) {
	if !ok {
		target.Problems = append(target.Problems, problem)
	}
}

func finalizeInspectTarget(target *AgentProviderInspectTarget) {
	if len(target.Problems) == 0 {
		target.Healthy = true
		target.Status = "healthy"
		return
	}
	if target.Status == "" {
		target.Status = "unhealthy"
	}
	if target.SuggestedFix == "" {
		target.SuggestedFix = "Repair this config through AINexus."
	}
}

func hermesProvider(existing any, name string) map[string]any {
	arr, ok := existing.([]any)
	if !ok {
		return nil
	}
	for _, item := range arr {
		provider, ok := item.(map[string]any)
		if ok && fmt.Sprint(provider["name"]) == name {
			return provider
		}
	}
	return nil
}

type simpleTOMLConfig struct {
	root     map[string]string
	section map[string]map[string]string
}

func parseSimpleTOML(text string) simpleTOMLConfig {
	result := simpleTOMLConfig{
		root:     map[string]string{},
		section: map[string]map[string]string{},
	}
	current := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.Contains(line, "]") {
			current = strings.TrimSpace(line[1:strings.Index(line, "]")])
			if _, ok := result.section[current]; !ok {
				result.section[current] = map[string]string{}
			}
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if index := strings.Index(value, " #"); index >= 0 {
			value = strings.TrimSpace(value[:index])
		}
		value = strings.Trim(value, `"'`)
		if current == "" {
			result.root[key] = value
			continue
		}
		result.section[current][key] = value
	}
	return result
}

func (c simpleTOMLConfig) activeProviderBaseURLMatches(targetURL string) bool {
	providerName := strings.TrimSpace(c.root["model_provider"])
	if providerName != "" {
		provider := c.section["model_providers."+providerName]
		if provider == nil {
			return false
		}
		return agentBaseURLMatches(provider["base_url"], targetURL)
	}
	for _, section := range c.section {
		if agentBaseURLMatches(section["base_url"], targetURL) {
			return true
		}
	}
	return agentBaseURLMatches(c.root["base_url"], targetURL)
}

func agentBaseURLMatches(actual, targetURL string) bool {
	actual = strings.TrimRight(strings.TrimSpace(actual), "/")
	targetURL = strings.TrimRight(strings.TrimSpace(targetURL), "/")
	return actual != "" && (actual == targetURL || actual == targetURL+"/v1")
}
