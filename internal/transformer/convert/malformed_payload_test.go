package convert

import (
	"testing"

	"github.com/lich0821/ccNexus/internal/transformer"
)

// TestClaudeRespToOpenAIMalformedNoPanic ensures a Claude response whose text
// block lacks a string "text" field does not panic (regression for the
// previously-unchecked type assertion).
func TestClaudeRespToOpenAIMalformedNoPanic(t *testing.T) {
	resp := `{"id":"x","content":[{"type":"text"},{"type":"text","text":null}],"usage":{"input_tokens":1,"output_tokens":1}}`
	if _, err := ClaudeRespToOpenAI([]byte(resp), "m"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestClaudeStreamToOpenAIMalformedNoPanic ensures an input_json_delta without
// a string partial_json does not panic.
func TestClaudeStreamToOpenAIMalformedNoPanic(t *testing.T) {
	ctx := transformer.NewStreamContext()
	event := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"input_json_delta\"}}\n\n")
	if _, err := ClaudeStreamToOpenAI(event, ctx, "m"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	textEvent := []byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\"}}\n\n")
	if _, err := ClaudeStreamToOpenAI(textEvent, ctx, "m"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
