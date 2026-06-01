package terminal

import "testing"

func TestEscapeAppleScriptString(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`plain`, `plain`},
		{`a"b`, `a\"b`},
		{`a\b`, `a\\b`},
		{`\"`, `\\\"`},
	}
	for _, tc := range cases {
		if got := escapeAppleScriptString(tc.in); got != tc.want {
			t.Errorf("escapeAppleScriptString(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
