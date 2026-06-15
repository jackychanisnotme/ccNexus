package main

import (
	"encoding/json"
	"testing"
)

func TestAppAgentBindingsReturnJSONErrorsBeforeStartup(t *testing.T) {
	app := NewApp(nil)

	raw := app.RunAgent(`{"task":""}`)
	var result map[string]any
	if err := json.Unmarshal([]byte(raw), &result); err != nil {
		t.Fatalf("invalid json: %v raw=%s", err, raw)
	}
	if result["success"] != false {
		t.Fatalf("expected failure before startup, got %#v", result)
	}
}
