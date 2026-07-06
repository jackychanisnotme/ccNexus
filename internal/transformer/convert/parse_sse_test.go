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

func TestParseSSEConcatenatesMultipleDataLines(t *testing.T) {
	// A single event whose JSON payload was split across multiple data: lines.
	input := "event: message\ndata: {\"a\":1,\ndata: \"b\":\"long\"}"
	ev, data := parseSSE([]byte(input))
	if ev != "message" {
		t.Fatalf("event = %q, want %q", ev, "message")
	}
	want := "{\"a\":1,\n\"b\":\"long\"}"
	if data != want {
		t.Fatalf("data = %q, want %q", data, want)
	}
}

func TestParseSSEKeepsDonePassthrough(t *testing.T) {
	_, data := parseSSE([]byte("data: [DONE]"))
	if data != "[DONE]" {
		t.Fatalf("data = %q, want [DONE]", data)
	}
}
func TestFilterNonResponsesStreamEventDropsChatCompletionChunk(t *testing.T) {
	chatChunk := []byte("data: {\"id\":\"chatcmpl-x\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n")
	if got := FilterNonResponsesStreamEvent(chatChunk); got != nil {
		t.Fatalf("expected chat.completion.chunk to be filtered, got %q", got)
	}
}

func TestFilterNonResponsesStreamEventKeepsResponsesEvent(t *testing.T) {
	created := []byte("data: {\"type\":\"response.created\",\"sequence_number\":0,\"response\":{}}\n\n")
	if got := FilterNonResponsesStreamEvent(created); string(got) != string(created) {
		t.Fatalf("expected response.created to pass through unchanged")
	}
}

func TestFilterNonResponsesStreamEventKeepsCompletedAndErrorEvents(t *testing.T) {
	for _, event := range [][]byte{
		[]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"),
		[]byte("data: {\"type\":\"error\",\"error\":{\"message\":\"bad request\"}}\n\n"),
	} {
		if got := FilterNonResponsesStreamEvent(event); string(got) != string(event) {
			t.Fatalf("expected typed event to pass through unchanged, got %q", got)
		}
	}
}

func TestFilterNonResponsesStreamEventDropsCodexRateLimits(t *testing.T) {
	rateLimits := []byte("data: {\"type\":\"codex.rate_limits\",\"plan_type\":\"plus\",\"rate_limits\":{\"primary\":{\"used_percent\":5}}}\n\n")
	if got := FilterNonResponsesStreamEvent(rateLimits); got != nil {
		t.Fatalf("expected codex.rate_limits to be filtered, got %q", got)
	}
}

func TestFilterNonResponsesStreamEventKeepsDone(t *testing.T) {
	done := []byte("data: [DONE]\n\n")
	if got := FilterNonResponsesStreamEvent(done); string(got) != string(done) {
		t.Fatalf("expected [DONE] to pass through")
	}
}
