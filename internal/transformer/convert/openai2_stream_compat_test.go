package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/lich0821/ccNexus/internal/transformer"
)

func TestClaudeStreamToOpenAI2EmitsSDKCompatibleTextStream(t *testing.T) {
	ctx := transformer.NewStreamContext()
	chunks := []string{
		`event: message_start
data: {"type":"message_start","message":{"id":"msg_1","usage":{"input_tokens":7}}}`,
		`event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hello"}}`,
		`event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		`event: content_block_stop
data: {"type":"content_block_stop","index":0}`,
		`event: message_delta
data: {"type":"message_delta","usage":{"output_tokens":3}}`,
		`event: message_stop
data: {"type":"message_stop"}`,
	}

	var raw strings.Builder
	for _, chunk := range chunks {
		out, err := ClaudeStreamToOpenAI2([]byte(chunk), ctx)
		if err != nil {
			t.Fatalf("ClaudeStreamToOpenAI2 failed: %v", err)
		}
		raw.Write(out)
	}

	assertOpenAI2TextStreamCompatible(t, parseOpenAI2StreamEvents(t, raw.String()), "hello world")
}

func TestOpenAIStreamToOpenAI2EmitsSDKCompatibleTextStream(t *testing.T) {
	ctx := transformer.NewStreamContext()
	chunks := []string{
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{"content":" world"},"finish_reason":null}]}`,
		`data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"gpt-5.5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10}}`,
	}

	var raw strings.Builder
	for _, chunk := range chunks {
		out, err := OpenAIStreamToOpenAI2([]byte(chunk), ctx)
		if err != nil {
			t.Fatalf("OpenAIStreamToOpenAI2 failed: %v", err)
		}
		raw.Write(out)
	}

	assertOpenAI2TextStreamCompatible(t, parseOpenAI2StreamEvents(t, raw.String()), "hello world")
}

func TestOpenAIStreamToOpenAI2SuppressesReasoningOnlyChunksForSDKCompatibility(t *testing.T) {
	ctx := transformer.NewStreamContext()
	reasoning := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"reasoning_content":"think"},"finish_reason":null}]}`
	out, err := OpenAIStreamToOpenAI2([]byte(reasoning), ctx)
	if err != nil {
		t.Fatalf("OpenAIStreamToOpenAI2 reasoning chunk failed: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("expected reasoning-only chunk not to emit downstream Responses events, got %s", string(out))
	}

	var raw strings.Builder
	text := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":null}]}`
	out, err = OpenAIStreamToOpenAI2([]byte(text), ctx)
	if err != nil {
		t.Fatalf("OpenAIStreamToOpenAI2 text chunk failed: %v", err)
	}
	raw.Write(out)
	finish := `data: {"id":"chatcmpl_1","object":"chat.completion.chunk","model":"deepseek-v4-pro","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`
	out, err = OpenAIStreamToOpenAI2([]byte(finish), ctx)
	if err != nil {
		t.Fatalf("OpenAIStreamToOpenAI2 finish chunk failed: %v", err)
	}
	raw.Write(out)

	events := parseOpenAI2StreamEvents(t, raw.String())
	assertOpenAI2TextStreamCompatible(t, events, "hello")
	for _, event := range events {
		eventType, _ := event["type"].(string)
		if strings.Contains(eventType, "reasoning") {
			t.Fatalf("did not expect reasoning event in SDK-compatible downstream stream: %#v", event)
		}
	}
}

func TestGeminiStreamToOpenAI2EmitsSDKCompatibleTextStream(t *testing.T) {
	ctx := transformer.NewStreamContext()
	chunk := `data: {"candidates":[{"content":{"parts":[{"text":"gemini ok"}],"role":"model"},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}}`

	out, err := GeminiStreamToOpenAI2([]byte(chunk), ctx)
	if err != nil {
		t.Fatalf("GeminiStreamToOpenAI2 failed: %v", err)
	}

	assertOpenAI2TextStreamCompatible(t, parseOpenAI2StreamEvents(t, string(out)), "gemini ok")
}

func parseOpenAI2StreamEvents(t *testing.T, raw string) []map[string]interface{} {
	t.Helper()

	blocks := strings.Split(raw, "\n\n")
	events := make([]map[string]interface{}, 0, len(blocks))
	for _, block := range blocks {
		for _, line := range strings.Split(block, "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if payload == "" || payload == "[DONE]" {
				continue
			}
			var event map[string]interface{}
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				t.Fatalf("failed to decode stream event %q: %v", payload, err)
			}
			events = append(events, event)
		}
	}
	return events
}

func assertOpenAI2TextStreamCompatible(t *testing.T, events []map[string]interface{}, wantText string) {
	t.Helper()

	if len(events) == 0 {
		t.Fatal("expected stream events")
	}
	for _, event := range events {
		if _, ok := event["sequence_number"]; !ok {
			t.Fatalf("event missing sequence_number: %#v", event)
		}
	}

	created := firstOpenAI2Event(events, "response.created")
	response := mustMap(t, created["response"], "created.response")
	output := mustSlice(t, response["output"], "created.response.output")
	if len(output) != 0 {
		t.Fatalf("expected response.created output to start empty, got %#v", output)
	}

	delta := firstOpenAI2Event(events, "response.output_text.delta")
	if delta["item_id"] == "" {
		t.Fatalf("delta missing item_id: %#v", delta)
	}
	mustSlice(t, delta["logprobs"], "delta.logprobs")

	done := firstOpenAI2Event(events, "response.output_text.done")
	if done["text"] != wantText {
		t.Fatalf("expected output_text.done text %q, got %#v", wantText, done["text"])
	}
	if done["item_id"] == "" {
		t.Fatalf("output_text.done missing item_id: %#v", done)
	}
	mustSlice(t, done["logprobs"], "done.logprobs")

	partDone := firstOpenAI2Event(events, "response.content_part.done")
	part := mustMap(t, partDone["part"], "content_part.done.part")
	if part["text"] != wantText {
		t.Fatalf("expected content_part.done text %q, got %#v", wantText, part["text"])
	}

	itemDone := firstOpenAI2Event(events, "response.output_item.done")
	item := mustMap(t, itemDone["item"], "output_item.done.item")
	assertOpenAI2MessageItemText(t, item, wantText)

	completed := firstOpenAI2Event(events, "response.completed")
	completedResponse := mustMap(t, completed["response"], "completed.response")
	completedOutput := mustSlice(t, completedResponse["output"], "completed.response.output")
	if len(completedOutput) == 0 {
		t.Fatalf("expected completed response output")
	}
	assertOpenAI2MessageItemText(t, mustMap(t, completedOutput[0], "completed.response.output[0]"), wantText)
}

func firstOpenAI2Event(events []map[string]interface{}, eventType string) map[string]interface{} {
	for _, event := range events {
		if event["type"] == eventType {
			return event
		}
	}
	panic("missing event type " + eventType)
}

func assertOpenAI2MessageItemText(t *testing.T, item map[string]interface{}, wantText string) {
	t.Helper()
	if item["type"] != "message" {
		t.Fatalf("expected message item, got %#v", item["type"])
	}
	content := mustSlice(t, item["content"], "message.content")
	if len(content) == 0 {
		t.Fatalf("expected message content")
	}
	part := mustMap(t, content[0], "message.content[0]")
	if part["text"] != wantText {
		t.Fatalf("expected message text %q, got %#v", wantText, part["text"])
	}
}

func mustMap(t *testing.T, value interface{}, name string) map[string]interface{} {
	t.Helper()
	m, ok := value.(map[string]interface{})
	if !ok {
		t.Fatalf("%s should be an object, got %T %#v", name, value, value)
	}
	return m
}

func mustSlice(t *testing.T, value interface{}, name string) []interface{} {
	t.Helper()
	s, ok := value.([]interface{})
	if !ok {
		t.Fatalf("%s should be an array, got %T %#v", name, value, value)
	}
	return s
}
