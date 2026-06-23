<div align="center">

<p align="center">
  <img src="images/AINexus.svg" alt="AINexus - API Provider, Token Pool and Agent hub for AI coding tools" width="720" />
</p>

[![Pre-release](https://img.shields.io/badge/pre--release-v6.3.6-blue)](https://github.com/jackychanisnotme/ccNexus/releases/tag/v6.3.6)
[![License](https://img.shields.io/badge/License-Commercial%20use%20requires%20authorization-red.svg)](../LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](https://go.dev/)
[![Wails](https://img.shields.io/badge/Wails-v2-blue)](https://wails.io/)

[English](README_EN.md) | [简体中文](../README.md)

</div>

AINexus is a local API Provider, Token Pool, and Agent management hub for Claude Code, Codex CLI, OpenClaw, Hermes Agent, and OpenAI/Claude/Gemini-compatible clients. It brings endpoints, models, API keys, subscription tokens, license state, usage statistics, backup, and agent configuration into one desktop app and server runtime.

Version 6.3.6 focuses on online card-key activation, Codex Token Pool quota and per-credential usage visibility, Claude OAuth Token Pool, the AI Agent workspace, Agent Provider configuration repair, multi-endpoint failover, and cross-platform customer packages.

> [!IMPORTANT]
> The current release is **AINexus v6.3.6 pre-release**. Download it here:
> [https://github.com/jackychanisnotme/ccNexus/releases/tag/v6.3.6](https://github.com/jackychanisnotme/ccNexus/releases/tag/v6.3.6)

> [!NOTE]
> AINexus Pro uses online card-key activation. The license server issues Ed25519 tickets, and the client keeps a 30-day offline grace period after the latest successful online validation. For purchase or renewal, contact WeChat: `yo22bro`.

## Preview

<table>
  <tr>
    <td align="center"><img src="images/EN-Light.png" alt="AINexus 6.3.6 light theme" width="400"></td>
    <td align="center"><img src="images/EN-Dark.png" alt="AINexus 6.3.6 dark theme" width="400"></td>
  </tr>
</table>

## Quick Start

### Desktop App

Download the matching package from the [v6.3.6 pre-release](https://github.com/jackychanisnotme/ccNexus/releases/tag/v6.3.6):

- **macOS**: download `AINexus-v6.3.6-darwin-universal.zip`, extract it, and move `AINexus.app` to Applications. If macOS blocks the first launch, use right-click -> Open.
- **Windows**: download `AINexus-v6.3.6-windows-amd64.zip`, extract it, and run `AINexus.exe`.
- **Server/NAS/Docker**: use server mode or Docker Compose.

Enter your online card key on first launch. After activation, the default local proxy address is:

```text
http://127.0.0.1:3000
```

### Server Mode

```bash
go run ./cmd/server
```

Available endpoints:

- API Provider: `http://127.0.0.1:3000`
- Web management UI: `http://127.0.0.1:3000/ui/`
- Health check: `http://127.0.0.1:3000/health`

Server mode also requires online authorization. You can activate it from the command line:

```bash
CCNEXUS_LICENSE_PUBLIC_KEY=<public-key> go run ./cmd/server -activate <card-key>
```

### Docker Compose

```bash
cd cmd/server
docker compose up -d --build
docker compose logs -f ainexus
```

For LAN access in Docker, configure:

```yaml
- AINEXUS_LISTEN_MODE=lan
```

The default Compose file maps host port `3021` to container port `3000`:

- Web management UI: `http://127.0.0.1:3021/ui/`
- Health check: `http://127.0.0.1:3021/health`

Data is stored in `cmd/server/ainexus/`. Read the generated first-launch Basic Auth password from the container logs.

## Configure Clients

### Claude Code

Edit `~/.claude/settings.json`:

```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "ainexus",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:3000",
    "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "64000"
  }
}
```

Some upstream models do not support 64k output. Adjust or remove `CLAUDE_CODE_MAX_OUTPUT_TOKENS` according to the selected model.

### Codex CLI

Use the Responses API provider in Codex:

```toml
model_provider = "AINexus"
model = "gpt-5-codex"
preferred_auth_method = "apikey"

[model_providers.AINexus]
name = "AINexus"
base_url = "http://127.0.0.1:3000/v1"
wire_api = "responses"
experimental_bearer_token = "ainexus-local"
```

If your Codex CLI version still requires `~/.codex/auth.json`, add a placeholder key:

```json
{"OPENAI_API_KEY":"ainexus-local"}
```

The client-side key only satisfies the client configuration requirement. AINexus manages the actual upstream credentials through endpoints and token pools.

## Core Capabilities

| Area | What AINexus 6.3.6 provides |
|------|------------------------------|
| Local API Provider | One local endpoint for Claude Code, Codex CLI, OpenClaw, Hermes Agent, OpenAI-compatible clients, and Gemini-compatible workflows |
| Endpoint failover | Rotation, cooldown, request-local fallback, error classification, and recovery for unstable upstreams |
| Protocol conversion | Claude, OpenAI Chat Completions, OpenAI Responses, Gemini, DeepSeek, Kimi/Moonshot, and Codex-oriented flows |
| Codex Token Pool | Import subscription token JSON, rotate credentials, refresh tokens, isolate invalid tokens, display quota snapshots, and track per-credential usage |
| Claude OAuth Token Pool | Manage Claude OAuth credentials separately from API-key endpoints and route Claude-compatible requests through the pool |
| AI Agent workspace | Central place for agent providers, model choices, provider repair, and AI coding tool configuration |
| Monitoring | Live requests, endpoint state, request IDs, classified failures, token usage, and credential-level statistics |
| Backup and sync | WebDAV, local backup, S3-compatible storage, and restore workflows |
| Online licensing | Network card-key activation, Ed25519-signed license tickets, device authorization, disable/renew support, and 30-day offline grace |

## Add Endpoints

Common authentication modes:

| Mode | Use case |
|------|----------|
| `api_key` | Standard upstream API key |
| `token_pool` | Generic token pool rotation |
| `codex_token_pool` | Codex subscription token pool with refresh, quota, and isolation support |

Common transformers:

| Transformer | Upstream protocol | Typical use |
|-------------|-------------------|-------------|
| `Codex` | Codex-oriented flow | Codex subscription and Codex-specific routing |
| `openai` | OpenAI Chat Completions | Chat-compatible providers |
| `openai2` | OpenAI Responses | Codex CLI and Responses-compatible providers |
| `gemini` | Google Gemini | Native Gemini APIs |
| `deepseek` | OpenAI Chat-compatible | DeepSeek-compatible providers |
| `kimi` | OpenAI Chat-compatible | Kimi / Moonshot-compatible providers |

To use a Codex Token Pool:

1. Select `Codex Token Pool` as the authentication mode.
2. Import token JSON records containing `access_token` and `refresh_token`.
3. AINexus configures the Codex upstream and the Responses-compatible transformer, then manages rotation, refresh, quota snapshots, and invalid-token isolation.

## Runtime Modes

| Mode | Entry | Best for |
|------|-------|----------|
| Desktop | `cmd/desktop` | Local GUI, tray operation, visual management, and customer desktop usage |
| Server | `cmd/server` | Servers, NAS, Docker, and web-based management |
| License Server | `cmd/license-server` | Online card-key generation, activation, device management, renewal, and disable operations |

Server mode supports:

| Environment variable | Description | Default |
|----------------------|-------------|---------|
| `AINEXUS_PORT` | HTTP listening port | `3000` |
| `AINEXUS_LISTEN_MODE` | `local` for loopback only; `lan` for all interfaces | `local` |
| `AINEXUS_LOG_LEVEL` | `0` debug, `1` info, `2` warning, `3` error | `1` |
| `AINEXUS_DATA_DIR` | Data directory | User data directory; `/data` in Docker |
| `AINEXUS_DB_PATH` | SQLite database path | `ainexus.db` under the data directory |
| `AINEXUS_BASIC_AUTH_ENABLED` | Protect the Web UI and management API | `true` |
| `AINEXUS_BASIC_AUTH_USERNAME` | Basic Auth username | `admin` |
| `AINEXUS_BASIC_AUTH_PASSWORD` | Basic Auth password | Randomly generated on first launch |

Online licensing supports:

| Environment variable | Description |
|----------------------|-------------|
| `CCNEXUS_LICENSE_SERVER_URL` | Client license server URL |
| `CCNEXUS_LICENSE_PUBLIC_KEY` | Ed25519 public key embedded in client builds |
| `CCNEXUS_LICENSE_PORT` | License server port |
| `CCNEXUS_LICENSE_BIND` | License server bind address |
| `CCNEXUS_LICENSE_DATA_DIR` | License server data directory |
| `CCNEXUS_LICENSE_DB_PATH` | License server SQLite database path |
| `CCNEXUS_LICENSE_KEY_PATH` | License private key path on the license server |
| `CCNEXUS_LICENSE_ADMIN_USERNAME` | License admin username |
| `CCNEXUS_LICENSE_ADMIN_PASSWORD` | License admin password |

## Upgrade Compatibility

AINexus is already distributed to real customers, so upgrade safety has priority over feature velocity:

- Existing license tickets, card redemptions, offline grace, configuration files, token pools, and SQLite data must remain readable.
- Default ports, license server URLs, endpoint authentication modes, and transformer semantics should stay stable unless migration logic is provided.
- Database migrations should be idempotent and preserve rollback options.
- Future server-side statistics should only upload necessary aggregate data such as device ID, version, endpoint ID, model, request count, token usage, error class, and time window.
- Remote endpoint policies should keep a local fallback; when the server is unavailable, the customer's existing local configuration continues to work.
- API keys, access tokens, refresh tokens, prompts, and responses should not be uploaded in plaintext for operations features.

## Develop from Source

```bash
# Desktop frontend
cd cmd/desktop/frontend
npm install
npm run build

# Desktop app
cd ..
wails dev

# Server
cd ../../cmd/server
go build -ldflags="-s -w" -o ainexus-server .

# License server
cd ../license-server
go build -ldflags="-s -w" -o ccnexus-license .

# Tests
cd ../..
go test ./... -count=1
```
