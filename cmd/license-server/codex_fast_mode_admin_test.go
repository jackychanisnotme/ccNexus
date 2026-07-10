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
		"id=\"remoteEndpointCodexFastMode\"",
		"const codexFastMode=remoteEndpointCodexFastMode.checked",
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
		"id=\"remoteEndpointDialog\"",
		"id=\"remoteEndpointModel\"",
		"id=\"remoteEndpointThinking\"",
		"上游默认</option>",
		"<option value=\"off\">关闭</option>",
		"<option value=\"low\">Low</option>",
		"<option value=\"medium\">Medium</option>",
		"<option value=\"high\">High</option>",
		"<option value=\"xhigh\">XHigh / Max</option>",
		"submitRemoteEndpoint(event)",
		"openRemoteEndpointEditor(",
		"Object.prototype.hasOwnProperty.call(ep,'thinking')",
		"endpoints:thinking:v2",
		"value=\"__keep__\"",
		"thinking!=='__keep__'",
		"payload.thinking=thinking",
		"payload.model=model",
		"payload.apiKey=apiKey",
		"旧客户端不支持远程清空模型",
		"thinkingDisplay(state,ep)",
		"<th>模型</th>",
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

func TestAdminRemoteDeviceWorkspaceInteraction(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	body := string(source)
	for _, want := range []string{
		"id=\"deviceWorkspaceBackdrop\"",
		"id=\"deviceWorkspace\"",
		"class=\"device-workspace\"",
		"data-workspace-tab=\"endpoints\"",
		"data-workspace-tab=\"tokens\"",
		"data-workspace-tab=\"errors\"",
		"data-workspace-tab=\"commands\"",
		"data-workspace-tab=\"licenses\"",
		"openDeviceWorkspace(",
		"closeDeviceWorkspace()",
		"showWorkspaceTab(",
		"loadWorkspaceRemote(",
		"loadWorkspaceTelemetry(",
		"renderWorkspaceEndpoints(",
		"renderWorkspaceTokenPools(",
		"renderWorkspaceCommands(",
		"renderWorkspaceLicenses(",
		"workspaceOnlineState(",
		"id=\"workspaceTaskBar\"",
		"id=\"remoteSortDialog\"",
		"id=\"remoteConfirmDialog\"",
		"id=\"remoteSecretDialog\"",
		"id=\"remoteTokenDialog\"",
		"trackRemoteCommand(",
		"resumeWorkspaceCommands(",
		"hasPendingRemoteTarget(",
		"hasPendingRemoteTarget('endpoint',pendingTarget,0)",
		"remoteCommandStatusName(",
		"summary.changedFields",
		"前 5 分钟内上线即可执行",
		"状态暂不可用",
		"remoteSecretCountdown",
		"window.isSecureContext",
		"width:min(1100px,calc(100vw - 40px))",
		"@media(max-width:720px)",
		"width:100vw",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("admin remote device workspace missing %q", want)
		}
	}
	if strings.Contains(body, "onclick=\"toggleDevice(") {
		t.Fatal("device list must open the workspace instead of inline detail rows")
	}
	if strings.Contains(body, "await pollRemoteCommand(device.deviceId,command.id,null)") {
		t.Fatal("remote writes must not synchronously block on command completion")
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
		"loadWorkspaceTelemetry(",
		"renderEndpointErrorTelemetryTable(",
		"端点错误遥测",
		"近24小时",
		"近7天",
		"暂无端点错误遥测",
		"workspaceTelemetry.summary24h",
		"workspaceTelemetry.summary7d",
		"row.apiHost||'-'",
		"esc(row.sample||'-')",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("admin remote endpoint telemetry UI missing %q", want)
		}
	}
}
