package main

import (
	"os"
	"strings"
	"testing"
)

func TestAdminRemoteEndpointCodexFastModeControls(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(source)
	for _, want := range []string{
		"ep.codexFastMode?'开启':'关闭'",
		"remoteSetCodexFastMode(",
		"codexFastMode:enabled",
		"id=\"remoteCreateCodexFastMode\"",
		"codexFastMode:remoteCreateCodexFastMode.checked",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("admin remote endpoint UI missing %q", want)
		}
	}
	if strings.Contains(body, "1.5倍") || strings.Contains(body, "1.5x") {
		t.Fatalf("admin copy must not hard-code a 1.5x fast-mode multiplier")
	}
}

func TestAdminRemoteEndpointModelAndThinkingControls(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(source)
	for _, want := range []string{
		"id=\"remoteCreateEndpointDialog\"",
		"id=\"remoteEditEndpointDialog\"",
		"id=\"remoteCreateModel\"",
		"id=\"remoteCreateThinking\"",
		"<option value=\"\">上游默认</option>",
		"<option value=\"off\">关闭</option>",
		"<option value=\"low\">Low</option>",
		"<option value=\"medium\">Medium</option>",
		"<option value=\"high\">High</option>",
		"<option value=\"xhigh\">XHigh / Max</option>",
		"submitRemoteCreateEndpoint(event)",
		"submitRemoteEndpointEdit(event)",
		"remoteOpenEndpointEditor(",
		"Object.prototype.hasOwnProperty.call(ep,'thinking')",
		"endpoints:thinking:v2",
		"value=\"__keep__\"",
		"if(thinking!=='__keep__')",
		"payload.thinking=thinking",
		"payload.model=model",
		"旧客户端不支持远程清空模型",
		"thinkingDisplay(state,ep)",
		"<th>模型</th><th>推理</th>",
		"max-width:calc(100vw - 340px)",
		"contain:inline-size",
		"overflow-x:hidden",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("admin remote model/thinking UI missing %q", want)
		}
	}
	if strings.Contains(body, "const name=prompt('端点名称')") {
		t.Fatal("remote endpoint creation must not use the legacy prompt chain")
	}
}

func TestAdminRemoteEndpointErrorTelemetrySection(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(source)
	for _, want := range []string{
		"/api/admin/telemetry/endpoint-errors/summary?deviceId=",
		"renderEndpointErrorTelemetry(",
		"renderEndpointErrorTelemetryTable(",
		"端点错误遥测",
		"近24小时",
		"近7天",
		"暂无端点错误遥测",
		"endpointErrorSummary24h",
		"endpointErrorSummary7d",
		"row.apiHost||'-'",
		"esc(row.sample||'-')",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("admin remote endpoint telemetry UI missing %q", want)
		}
	}
}
