package claudeoauth

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSetupTokenAcceptsRawTokenAndEnvAssignment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "raw token",
			input: "claude-raw-token",
			want:  "claude-raw-token",
		},
		{
			name:  "claude setup-token env assignment",
			input: "export CLAUDE_CODE_OAUTH_TOKEN='claude-setup-token'",
			want:  "claude-setup-token",
		},
		{
			name:  "anthropic auth token env assignment",
			input: "ANTHROPIC_AUTH_TOKEN=anthropic-auth-token",
			want:  "anthropic-auth-token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSetupToken(tt.input)
			if err != nil {
				t.Fatalf("ParseSetupToken returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected token %q, got %q", tt.want, got)
			}
		})
	}
}

func TestDiscoverReturnsMaskedPreviewsWithoutRawTokens(t *testing.T) {
	homeDir := filepath.Join(string(filepath.Separator), "home", "test")
	settingsPath := filepath.Join(homeDir, ".claude", "settings.json")
	fileToken := "file-claude-oauth-token"
	envToken := "env-claude-oauth-token"

	candidates, err := Discover(DiscoverOptions{
		HomeDir: homeDir,
		Env: map[string]string{
			"CLAUDE_CODE_OAUTH_TOKEN": envToken,
		},
		Files: []string{settingsPath},
		ReadFile: func(path string) ([]byte, error) {
			if path != settingsPath {
				t.Fatalf("unexpected file path: %s", path)
			}
			return []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"` + fileToken + `"}}`), nil
		},
	})
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected two candidates, got %d", len(candidates))
	}

	previews := Preview(candidates)
	if len(previews) != 2 {
		t.Fatalf("expected two previews, got %d", len(previews))
	}
	for _, preview := range previews {
		rendered := preview.ID + preview.Source + preview.Label + preview.MaskedToken
		if strings.Contains(rendered, envToken) || strings.Contains(rendered, fileToken) {
			t.Fatalf("preview leaked a raw token: %#v", preview)
		}
		if !strings.Contains(preview.MaskedToken, "…") {
			t.Fatalf("expected masked token to contain ellipsis, got %q", preview.MaskedToken)
		}
	}
}
