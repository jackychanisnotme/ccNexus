<div align="center">

<p align="center">
  <img src="images/ccNexus.svg" alt="Claude Code, Codex CLI, and Hermes Agent API Resource Management Hub" width="720" />
</p>

[![Build Status](https://github.com/jackychanisnotme/ccNexus/actions/workflows/build.yml/badge.svg)](https://github.com/jackychanisnotme/ccNexus/actions)
[![Latest Release](https://img.shields.io/github/v/release/jackychanisnotme/ccNexus?label=release)](https://github.com/jackychanisnotme/ccNexus/releases/latest)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](https://go.dev/)
[![Wails](https://img.shields.io/badge/Wails-v2-blue)](https://wails.io/)

[English](README_EN.md) | [简体中文](../README.md)

</div>

ccNexus is more than a smart endpoint rotation proxy for Claude Code, Codex CLI, and Hermes Agent. It is an API resource management system for AI development workflows, bringing endpoints, models, API keys, Codex Token Pools, quota snapshots, usage statistics, and backups into one local control plane.

> [!IMPORTANT]
> This fork maintains the Optimized line, with extra compatibility for Codex CLI, Claude Code, Hermes Agent, OpenAI Responses API, DeepSeek, and Kimi/Moonshot.
>
> Latest release: [`ccNexus Optimized`](https://github.com/jackychanisnotme/ccNexus/releases/latest)

## Features

- **One Local Gateway**: Connect Claude Code, Codex CLI, Hermes Agent, OpenAI Chat/Responses-compatible clients, and model tools to one local base URL
- **API Resource Management**: Manage endpoints, models, API keys, Token Pools, quota snapshots, usage statistics, and backup data in one place
- **Endpoint Rotation and Failover**: Rotate across enabled endpoints and skip failing upstreams automatically
- **Protocol Conversion**: Convert between Claude, OpenAI Chat, OpenAI Responses, Gemini, DeepSeek, and Kimi/Moonshot formats
- **Codex Token Pool**: Bulk import `access_token/refresh_token`, rotate credentials, refresh after 401s, isolate invalid tokens, and target the ChatGPT Codex backend automatically
- **Credential Usage and Rate Insights**: Capture Codex quota snapshots and show per-credential requests, errors, token usage, and recent activity
- **Endpoint-Level Reasoning Control**: Set `low` / `medium` / `high` / `xhigh` reasoning effort, or explicitly disable upstream thinking where supported
- **Forced Streaming Upstream Mode**: Use streaming upstream requests for providers that reject non-streaming calls while aggregating output for non-streaming clients
- **Model and Compatibility APIs**: Serve `/v1/models`, `/models`, `/api/tags`, `/version`, `/props`, `/health`, and `/stats` for client discovery and monitoring
- **Live Statistics**: Event-driven usage updates with today/yesterday/week/month views
- **Desktop and Server Modes**: Use the Wails desktop app locally, or run `cmd/server` headlessly on a server, NAS, or Docker host
- **Backup and Sync**: Support WebDAV, local backups, and S3-compatible storage

## Client Compatibility

| Client | Recommended Entry | Status |
|--------|-------------------|--------|
| Claude Code | Claude / Anthropic-compatible gateway | Stable |
| Codex CLI | OpenAI Responses API, preferably with the `openai2` transformer | Stable |
| Hermes Agent | Claude or OpenAI-compatible gateway, depending on the client protocol | Stable |
| OpenClaw | Claude or OpenAI-compatible gateway | Experimental; robustness is limited in some streaming, tool-call, and retry scenarios |

<table>
  <tr>
    <td align="center"><img src="images/EN-Light.png" alt="Light Theme" width="400"></td>
    <td align="center"><img src="images/EN-Dark.png" alt="Dark Theme" width="400"></td>
  </tr>
</table>

## Quick Start

### 1. Download and Install

[Download the latest release from this fork](https://github.com/jackychanisnotme/ccNexus/releases/latest)

- **macOS**: Extract the `.zip`, move `ccNexus.app` to Applications, then right-click → Open for the first run
- **Windows**: Download `windows-amd64.zip`, extract it, then run `ccNexus.exe`
- **Linux**: Build from source, or use server mode/Docker
- **Server mode**: `cd cmd/server && go run main.go`

### 2. Add Endpoints

Click "Add Endpoint", then fill in the API URL, key, auth mode, transformer, and target model.

Common transformers:
- `claude`: Claude / Anthropic-compatible APIs
- `openai`: OpenAI Chat Completions-compatible APIs
- `openai2`: OpenAI Responses API, recommended for Codex CLI
- `gemini`: Google Gemini
- `deepseek`: DeepSeek Chat-compatible APIs
- `kimi`: Kimi / Moonshot-compatible APIs

For Codex Token Pool mode:
- Set auth mode to `Codex Token Pool`
- Import token JSON records in the Token Pool page (`access_token` + `refresh_token`)
- ccNexus will lock the upstream URL and `openai2` transformer, then handle token rotation, 401-triggered refresh, quota snapshots, and lifecycle statuses

Optional enhancements:
- Enable endpoint reasoning and select the effort level for providers that support it
- Enable forced streaming when an upstream only accepts streaming requests
- Use the model fetch button next to the model field to pull upstream model IDs

### 3. Configure Clients

#### Claude Code
`~/.claude/settings.json`
```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "anything, not important",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:3000",
    "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "64000", // Some models may not support 64k
  }
  // Other settings
}

```

#### Codex CLI
Responses API is recommended:
```toml
model_provider = "ccNexus"
model = "gpt-5-codex"
preferred_auth_method = "apikey"

[model_providers.ccNexus]
name = "ccNexus"
base_url = "http://localhost:3000/v1"
wire_api = "responses"  # or "chat"

# Other settings
```

`~/.codex/auth.json` can be ignored because ccNexus handles endpoint or Token Pool authentication.

## Runtime Modes

| Mode | Entry | Best For |
|------|-------|----------|
| Desktop | `cmd/desktop` | Local GUI, tray app, visual endpoint and Token Pool management |
| Server | `cmd/server` | Remote servers, NAS, Docker, and headless HTTP proxy usage |

Server mode supports `CCNEXUS_PORT`, `CCNEXUS_LOG_LEVEL`, `CCNEXUS_DB_PATH`, `CCNEXUS_DATA_DIR`, `CCNEXUS_BASIC_AUTH_USERNAME`, and `CCNEXUS_BASIC_AUTH_PASSWORD`.

## Documentation

- [Configuration Guide](configuration_en.md)
- [Development Guide](development_en.md)
- [FAQ](FAQ_en.md)

## License

[MIT](LICENSE)
