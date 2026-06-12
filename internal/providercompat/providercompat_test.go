package providercompat

import (
	"encoding/json"
	"testing"
)

func TestNormalizeTargetPathForVersionedBaseURL(t *testing.T) {
	got := NormalizeTargetPathForBaseURL("https://api.moonshot.ai/v1", "/v1/chat/completions")
	if got != "/chat/completions" {
		t.Fatalf("expected /chat/completions, got %s", got)
	}
}

func TestNormalizeTargetPathForFullURL(t *testing.T) {
	got := NormalizeTargetPathForBaseURL("https://api.example.com/v1/chat/completions", "/v1/chat/completions")
	if got != "" {
		t.Fatalf("expected empty target path for full URL, got %s", got)
	}
}

func TestBuildOpenAIModelURLCandidatesDeepSeek(t *testing.T) {
	got, err := BuildOpenAIModelURLCandidates("https://api.deepseek.com/anthropic", "deepseek")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{
		"https://api.deepseek.com/models",
		"https://api.deepseek.com/anthropic/v1/models",
		"https://api.deepseek.com/anthropic/models",
		"https://api.deepseek.com/v1/models",
	}
	for _, expected := range want {
		if !contains(got, expected) {
			t.Fatalf("expected candidates to contain %s, got %#v", expected, got)
		}
	}
}

func TestBuildOpenAIModelURLCandidatesDeepSeekCustomGateway(t *testing.T) {
	got, err := BuildOpenAIModelURLCandidates("https://gateway.example.com", "deepseek")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0] != "https://gateway.example.com/v1/models" {
		t.Fatalf("expected custom DeepSeek gateway to prefer /v1/models, got %#v", got)
	}
	if got[1] != "https://gateway.example.com/models" {
		t.Fatalf("expected /models fallback, got %#v", got)
	}
}

func TestBuildOpenAIModelURLCandidatesVersionedBase(t *testing.T) {
	got, err := BuildOpenAIModelURLCandidates("https://api.moonshot.ai/v1", "kimi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got[0] != "https://api.moonshot.ai/v1/models" {
		t.Fatalf("expected first candidate to avoid duplicated v1, got %#v", got)
	}
}

func TestOpenAIChatTargetPathDeepSeekCustomGateway(t *testing.T) {
	got := OpenAIChatTargetPath("deepseek", "https://gateway.example.com")
	if got != "/v1/chat/completions" {
		t.Fatalf("expected /v1/chat/completions for custom DeepSeek gateway, got %s", got)
	}
}

func TestOpenAIChatTargetPathDeepSeekOfficial(t *testing.T) {
	got := OpenAIChatTargetPath("deepseek", "https://api.deepseek.com")
	if got != "/chat/completions" {
		t.Fatalf("expected /chat/completions for official DeepSeek API, got %s", got)
	}
}

func TestPoeIsOpenAIChatCompatible(t *testing.T) {
	if got := NormalizeTransformer("poe_chat"); got != "poe" {
		t.Fatalf("expected poe_chat alias to normalize to poe, got %q", got)
	}
	if !IsOpenAIChatTransformer("poe") {
		t.Fatal("expected poe transformer to be OpenAI Chat compatible")
	}
	if got := DefaultModel("poe"); got != "claude-fable-5" {
		t.Fatalf("expected Poe default model claude-fable-5, got %q", got)
	}
	if got := Owner("poe"); got != "poe" {
		t.Fatalf("expected Poe owner, got %q", got)
	}
	if got := ProviderKind("openai", "https://api.poe.com/v1"); got != "poe" {
		t.Fatalf("expected api.poe.com OpenAI endpoint to be detected as poe, got %q", got)
	}
}

func TestAdaptOpenAIChatPayloadForDeepSeek(t *testing.T) {
	raw := []byte(`{"model":"deepseek-chat","max_completion_tokens":8,"reasoning":{"effort":"medium"}}`)
	out := AdaptOpenAIChatPayload(raw, "deepseek", "https://api.deepseek.com", "")

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, ok := payload["max_completion_tokens"]; ok {
		t.Fatalf("did not expect max_completion_tokens, got %#v", payload)
	}
	if payload["max_tokens"].(float64) != 8 {
		t.Fatalf("expected max_tokens=8, got %#v", payload["max_tokens"])
	}
	if payload["reasoning_effort"] != "high" {
		t.Fatalf("expected reasoning_effort=high, got %#v", payload["reasoning_effort"])
	}
	thinking, ok := payload["thinking"].(map[string]interface{})
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("expected thinking.type=enabled, got %#v", payload["thinking"])
	}
	if _, ok := payload["reasoning"]; ok {
		t.Fatalf("did not expect reasoning object, got %#v", payload["reasoning"])
	}
}

func TestAdaptOpenAIChatPayloadForPoeOutputEffort(t *testing.T) {
	raw := []byte(`{"model":"claude-fable-5","messages":[],"max_completion_tokens":8,"reasoning":{"effort":"xhigh"}}`)
	out := AdaptOpenAIChatPayload(raw, "poe", "https://api.poe.com/v1", "")

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, ok := payload["max_completion_tokens"]; ok {
		t.Fatalf("did not expect max_completion_tokens, got %#v", payload)
	}
	if payload["max_tokens"].(float64) != 8 {
		t.Fatalf("expected max_tokens=8, got %#v", payload["max_tokens"])
	}
	if payload["output_effort"] != "max" {
		t.Fatalf("expected output_effort=max, got %#v", payload["output_effort"])
	}
	if _, ok := payload["reasoning"]; ok {
		t.Fatalf("did not expect reasoning object for Poe, got %#v", payload["reasoning"])
	}
	if _, ok := payload["reasoning_effort"]; ok {
		t.Fatalf("did not expect reasoning_effort for Poe, got %#v", payload["reasoning_effort"])
	}
}

func TestAdaptOpenAIChatPayloadForPoePreservesExplicitOutputEffort(t *testing.T) {
	raw := []byte(`{"model":"claude-fable-5","messages":[],"output_effort":"max"}`)
	out := AdaptOpenAIChatPayload(raw, "openai", "https://api.poe.com/v1", "low")

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if payload["output_effort"] != "max" {
		t.Fatalf("expected explicit output_effort to be preserved, got %#v", payload["output_effort"])
	}
}

func TestAdaptOpenAIChatPayloadForDeepSeekDefaultLeavesThinkingUnset(t *testing.T) {
	raw := []byte(`{"model":"deepseek-chat","messages":[]}`)
	out := AdaptOpenAIChatPayload(raw, "deepseek", "https://api.deepseek.com", "")

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, ok := payload["reasoning_effort"]; ok {
		t.Fatalf("did not expect reasoning_effort for provider default, got %#v", payload["reasoning_effort"])
	}
	if _, ok := payload["thinking"]; ok {
		t.Fatalf("did not expect thinking for provider default, got %#v", payload["thinking"])
	}
}

func TestAdaptOpenAIChatPayloadForDeepSeekThinkingOff(t *testing.T) {
	raw := []byte(`{"model":"deepseek-chat","messages":[],"reasoning_effort":"max"}`)
	out := AdaptOpenAIChatPayload(raw, "deepseek", "https://api.deepseek.com", "off")

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if _, ok := payload["reasoning_effort"]; ok {
		t.Fatalf("did not expect reasoning_effort when thinking is off, got %#v", payload["reasoning_effort"])
	}
	thinking, ok := payload["thinking"].(map[string]interface{})
	if !ok || thinking["type"] != "disabled" {
		t.Fatalf("expected thinking.type=disabled, got %#v", payload["thinking"])
	}
}

func TestAdaptOpenAIChatPayloadForDeepSeekXHighMapsToMax(t *testing.T) {
	raw := []byte(`{"model":"deepseek-chat","messages":[]}`)
	out := AdaptOpenAIChatPayload(raw, "deepseek", "https://api.deepseek.com", "xhigh")

	var payload map[string]interface{}
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal failed: %v", err)
	}
	if payload["reasoning_effort"] != "max" {
		t.Fatalf("expected reasoning_effort=max, got %#v", payload["reasoning_effort"])
	}
	thinking, ok := payload["thinking"].(map[string]interface{})
	if !ok || thinking["type"] != "enabled" {
		t.Fatalf("expected thinking.type=enabled, got %#v", payload["thinking"])
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
