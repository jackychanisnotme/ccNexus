package claudeoauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	EnvClaudeCodeOAuthToken = "CLAUDE_CODE_OAUTH_TOKEN"
	EnvAnthropicAuthToken   = "ANTHROPIC_AUTH_TOKEN"
)

type Candidate struct {
	ID          string
	Source      string
	Label       string
	Token       string
	MaskedToken string
}

type PreviewItem struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Label       string `json:"label"`
	MaskedToken string `json:"maskedToken"`
}

type DiscoverOptions struct {
	HomeDir  string
	Env      map[string]string
	Files    []string
	ReadFile func(path string) ([]byte, error)
}

func ParseSetupToken(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", fmt.Errorf("Claude OAuth token is required")
	}

	for _, line := range strings.Split(trimmed, "\n") {
		token := parseTokenAssignment(line)
		if token != "" {
			return token, nil
		}
	}

	if strings.ContainsAny(trimmed, "\r\n\t ") {
		return "", fmt.Errorf("Claude OAuth token must be a raw token or CLAUDE_CODE_OAUTH_TOKEN assignment")
	}
	return unquote(trimmed), nil
}

func Discover(options DiscoverOptions) ([]Candidate, error) {
	homeDir := strings.TrimSpace(options.HomeDir)
	if homeDir == "" {
		if userHome, err := os.UserHomeDir(); err == nil {
			homeDir = userHome
		}
	}

	env := options.Env
	if env == nil {
		env = readProcessEnv()
	}
	readFile := options.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	files := options.Files
	if files == nil {
		files = knownCredentialFiles(homeDir)
	}

	candidates := make([]Candidate, 0)
	seenTokens := make(map[string]bool)
	addCandidate := func(source, label, token string) {
		token = strings.TrimSpace(token)
		if token == "" || seenTokens[token] {
			return
		}
		seenTokens[token] = true
		candidates = append(candidates, newCandidate(source, label, token))
	}

	envKeys := []string{EnvClaudeCodeOAuthToken, EnvAnthropicAuthToken}
	for _, key := range envKeys {
		addCandidate("env:"+key, key, env[key])
	}

	for _, filePath := range files {
		raw, err := readFile(filePath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			continue
		}
		for _, token := range tokensFromJSON(raw) {
			addCandidate("file:"+filePath, filepath.Base(filePath), token)
		}
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Source == candidates[j].Source {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].Source < candidates[j].Source
	})
	return candidates, nil
}

func Preview(candidates []Candidate) []PreviewItem {
	previews := make([]PreviewItem, 0, len(candidates))
	for _, candidate := range candidates {
		previews = append(previews, PreviewItem{
			ID:          candidate.ID,
			Source:      candidate.Source,
			Label:       candidate.Label,
			MaskedToken: candidate.MaskedToken,
		})
	}
	return previews
}

func knownCredentialFiles(homeDir string) []string {
	if strings.TrimSpace(homeDir) == "" {
		return nil
	}
	return []string{
		filepath.Join(homeDir, ".claude.json"),
		filepath.Join(homeDir, ".claude", "settings.json"),
		filepath.Join(homeDir, ".claude", "credentials.json"),
		filepath.Join(homeDir, ".config", "claude", "settings.json"),
		filepath.Join(homeDir, ".config", "claude", "credentials.json"),
	}
}

func readProcessEnv() map[string]string {
	env := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			env[key] = value
		}
	}
	return env
}

func parseTokenAssignment(line string) string {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "export ")
	key, value, ok := strings.Cut(trimmed, "=")
	if !ok {
		return ""
	}
	key = strings.TrimSpace(key)
	if key != EnvClaudeCodeOAuthToken && key != EnvAnthropicAuthToken {
		return ""
	}
	return unquote(value)
}

func tokensFromJSON(raw []byte) []string {
	var value interface{}
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	tokens := make([]string, 0)
	collectTokens(value, &tokens)
	return tokens
}

func collectTokens(value interface{}, tokens *[]string) {
	switch typed := value.(type) {
	case map[string]interface{}:
		for key, child := range typed {
			if stringValue, ok := child.(string); ok && isTokenKey(key) {
				*tokens = append(*tokens, stringValue)
				continue
			}
			collectTokens(child, tokens)
		}
	case []interface{}:
		for _, child := range typed {
			collectTokens(child, tokens)
		}
	}
}

func isTokenKey(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case strings.ToLower(EnvClaudeCodeOAuthToken),
		strings.ToLower(EnvAnthropicAuthToken),
		"access_token",
		"accesstoken",
		"oauth_token",
		"oauthtoken":
		return true
	default:
		return false
	}
}

func newCandidate(source, label, token string) Candidate {
	sum := sha256.Sum256([]byte(source + "\x00" + token))
	id := hex.EncodeToString(sum[:8])
	return Candidate{
		ID:          id,
		Source:      source,
		Label:       strings.TrimSpace(label),
		Token:       strings.TrimSpace(token),
		MaskedToken: MaskToken(token),
	}
}

func MaskToken(token string) string {
	trimmed := strings.TrimSpace(token)
	runes := []rune(trimmed)
	if len(runes) <= 8 {
		if len(runes) <= 2 {
			return "…"
		}
		return string(runes[:1]) + "…" + string(runes[len(runes)-1:])
	}
	return string(runes[:4]) + "…" + string(runes[len(runes)-4:])
}

func unquote(value string) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) >= 2 {
		first := trimmed[0]
		last := trimmed[len(trimmed)-1]
		if (first == '\'' && last == '\'') || (first == '"' && last == '"') {
			return strings.TrimSpace(trimmed[1 : len(trimmed)-1])
		}
	}
	return trimmed
}
