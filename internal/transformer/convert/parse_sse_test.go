package convert

import "testing"

func TestParseSSEDataWithAndWithoutSpace(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		wantEvent string
		wantData  string
	}{
		{"with space", "event: message\ndata: {\"a\":1}", "message", "{\"a\":1}"},
		{"without space", "event:message\ndata:{\"a\":1}", "message", "{\"a\":1}"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev, data := parseSSE([]byte(tc.input))
			if ev != tc.wantEvent {
				t.Errorf("event = %q, want %q", ev, tc.wantEvent)
			}
			if data != tc.wantData {
				t.Errorf("data = %q, want %q", data, tc.wantData)
			}
		})
	}
}
