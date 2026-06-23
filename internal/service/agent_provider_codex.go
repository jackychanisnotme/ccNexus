package service

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	toml "github.com/pelletier/go-toml/v2"
)

func applyCodex(s *AgentProviderService, targetURL string, createMissing bool) (string, string, string) {
	configPath := filepath.Join(s.homeDir, ".codex", "config.toml")
	authPath := filepath.Join(s.homeDir, ".codex", "auth.json")
	if !fileExists(configPath) && !fileExists(authPath) && !createMissing {
		return configPath, "skipped", "configuration file not found"
	}

	configText := ""
	if fileExists(configPath) {
		data, err := os.ReadFile(configPath)
		if err != nil {
			return configPath, "failed", err.Error()
		}
		configText = string(data)
	}
	nextConfig, err := patchCodexConfigTOML(configText, targetURL)
	if err != nil {
		return configPath, "failed", err.Error()
	}

	auth := map[string]any{}
	if fileExists(authPath) {
		if err := readJSONFile(authPath, &auth, map[string]any{}); err != nil {
			return authPath, "failed", err.Error()
		}
	}
	auth["OPENAI_API_KEY"] = agentProviderPlaceholderKey

	if err := atomicWriteFile(configPath, []byte(nextConfig), 0644); err != nil {
		return configPath, "failed", err.Error()
	}
	if err := writeJSONFile(authPath, auth); err != nil {
		return authPath, "failed", err.Error()
	}
	return configPath, "success", "updated"
}

func patchCodexConfigTOML(input, targetURL string) (string, error) {
	text := input
	if strings.TrimSpace(text) != "" {
		if err := toml.Unmarshal([]byte(text), &map[string]any{}); err != nil {
			return "", err
		}
	}
	text = ensureTextTrailingNewline(text)
	text = upsertRootTOMLString(text, "model_provider", "AINexus")
	if !tomlRootHasKey(text, "model") {
		text = upsertRootTOMLString(text, "model", "gpt-5")
	}
	text = upsertTOMLSection(text, "model_providers.AINexus", map[string]string{
		"name":                      "AINexus",
		"base_url":                  strings.TrimRight(targetURL, "/") + "/v1",
		"wire_api":                  "responses",
		"experimental_bearer_token": agentProviderPlaceholderKey,
	})
	if err := toml.Unmarshal([]byte(text), &map[string]any{}); err != nil {
		return "", err
	}
	return text, nil
}

func ensureTextTrailingNewline(text string) string {
	if text == "" || strings.HasSuffix(text, "\n") {
		return text
	}
	return text + "\n"
}

func splitTextLinesPreserve(text string) []string {
	if text == "" {
		return nil
	}
	var lines []string
	for len(text) > 0 {
		index := strings.IndexByte(text, '\n')
		if index < 0 {
			lines = append(lines, text)
			break
		}
		lines = append(lines, text[:index+1])
		text = text[index+1:]
	}
	return lines
}

func textLineEnding(line string) string {
	switch {
	case strings.HasSuffix(line, "\r\n"):
		return "\r\n"
	case strings.HasSuffix(line, "\n"):
		return "\n"
	default:
		return ""
	}
}

func upsertRootTOMLString(text, key, value string) string {
	lines := splitTextLinesPreserve(text)
	keyRE := regexp.MustCompile(`^(\s*` + regexp.QuoteMeta(key) + `\s*=\s*)(.*?)(\s*(#.*)?)$`)
	insertAt := len(lines)
	for index, line := range lines {
		trimmedLine := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimSpace(trimmedLine)
		if strings.HasPrefix(trimmed, "[") {
			insertAt = index
			break
		}
		if keyRE.MatchString(trimmedLine) {
			lines[index] = keyRE.ReplaceAllString(trimmedLine, fmt.Sprintf(`${1}"%s"$3`, escapeTOMLString(value))) + textLineEnding(line)
			return strings.Join(lines, "")
		}
	}
	insert := fmt.Sprintf("%s = %q\n", key, value)
	lines = append(lines[:insertAt], append([]string{insert}, lines[insertAt:]...)...)
	return strings.Join(lines, "")
}

func tomlRootHasKey(text, key string) bool {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			return false
		}
		name, _, ok := strings.Cut(trimmed, "=")
		if ok && strings.TrimSpace(name) == key {
			return true
		}
	}
	return false
}

func upsertTOMLSection(text, section string, values map[string]string) string {
	lines := splitTextLinesPreserve(text)
	start, end := findTOMLSection(lines, section)
	if start >= 0 {
		sectionLines := append([]string{}, lines[start:end]...)
		for _, key := range []string{"name", "base_url", "wire_api", "experimental_bearer_token"} {
			sectionLines = upsertTOMLLine(sectionLines, key, values[key])
		}
		lines = append(append(lines[:start], sectionLines...), lines[end:]...)
		return strings.Join(lines, "")
	}
	if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
		lines = append(lines, "\n")
	}
	sectionLines := []string{fmt.Sprintf("[%s]\n", section)}
	for _, key := range []string{"name", "base_url", "wire_api", "experimental_bearer_token"} {
		sectionLines = append(sectionLines, fmt.Sprintf("%s = %q\n", key, values[key]))
	}
	lines = append(lines, sectionLines...)
	return strings.Join(lines, "")
}

func upsertTOMLLine(lines []string, key, value string) []string {
	keyRE := regexp.MustCompile(`^(\s*` + regexp.QuoteMeta(key) + `\s*=\s*)(.*?)(\s*(#.*)?)$`)
	for index, line := range lines {
		trimmed := strings.TrimRight(line, "\r\n")
		if keyRE.MatchString(trimmed) {
			lines[index] = keyRE.ReplaceAllString(trimmed, fmt.Sprintf(`${1}"%s"$3`, escapeTOMLString(value))) + textLineEnding(line)
			return lines
		}
	}
	return append(lines, fmt.Sprintf("%s = %q\n", key, value))
}

func findTOMLSection(lines []string, section string) (int, int) {
	sectionHeader := "[" + section + "]"
	start := -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.Contains(trimmed, "]") {
			header := trimmed[:strings.Index(trimmed, "]")+1]
			if start >= 0 {
				return start, index
			}
			if header == sectionHeader {
				start = index
			}
		}
	}
	if start >= 0 {
		return start, len(lines)
	}
	return -1, -1
}

func escapeTOMLString(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `"`, `\"`)
	return value
}
