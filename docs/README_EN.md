<div align="center">

<p align="center">
  <img src="images/AINexus.svg" alt="Claude Code, Codex CLI, Hermes Agent, and OpenClaw API Provider Switching Hub" width="720" />
</p>

[![Build Status](https://github.com/jackychanisnotme/AINexus/actions/workflows/build.yml/badge.svg)](https://github.com/jackychanisnotme/AINexus/actions)
[![Latest Release](https://img.shields.io/github/v/release/jackychanisnotme/AINexus?label=release)](https://github.com/jackychanisnotme/AINexus/releases/latest)
[![License: Commercial use requires authorization](https://img.shields.io/badge/License-Commercial%20use%20requires%20authorization-red.svg)](../LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](https://go.dev/)
[![Wails](https://img.shields.io/badge/Wails-v2-blue)](https://wails.io/)

[English](README_EN.md) | [简体中文](../README.md)

</div>

AINexus is a local API provider and resource management hub for Claude Code, Codex CLI, Hermes Agent, OpenClaw, and other AI development tools. It manages endpoints, models, API keys, token pools, quotas, statistics, and backups while providing hot switching and automatic failover across upstream providers, accounts, and models.

> [!IMPORTANT]
> This repository maintains **AINexus Optimized**, with a focus on Codex CLI, OpenAI Responses API, Claude Code, concurrent multi-endpoint workloads, and robust upstream error recovery.
>
> [Download the latest release](https://github.com/jackychanisnotme/AINexus/releases/latest)

## Quick Start

### Desktop App

Download the package for your platform from [Releases](https://github.com/jackychanisnotme/AINexus/releases/latest):

- **macOS**: Extract the `.zip`, move `AINexus.app` to Applications, and use right-click → Open on first launch if needed
- **Windows**: Extract `windows-amd64.zip`, then run `AINexus.exe`
- **Linux**: Build from source, or use server mode/Docker

After launch, click "Add Endpoint" and enter the API URL, key, authentication mode, transformer, and model. The default proxy address is `http://127.0.0.1:3000`.

### Server Mode

Go 1.24+ is required:

```bash
go run ./cmd/server
```

After startup:

- API provider: `http://127.0.0.1:3000`
- Web management UI: `http://127.0.0.1:3000/ui/`
- Health check: `http://127.0.0.1:3000/health`

Server mode enables Basic Auth by default with username `admin`. If no password is configured on first launch, AINexus generates one and prints it to the log once.

### Docker Compose

Before first launch, make sure the `environment` section in `cmd/server/docker-compose.yml` contains:

```yaml
- AINEXUS_LISTEN_MODE=lan
```

The container must listen on all interfaces for Docker's published host port to reach it. Then run:

```bash
cd cmd/server
docker compose up -d --build
docker compose logs -f ainexus
```

The default Compose file maps host port `3021` to container port `3000`:

- Web management UI: `http://127.0.0.1:3021/ui/`
- Health check: `http://127.0.0.1:3021/health`

Data is stored in `cmd/server/ainexus/`. Read the generated first-launch password from the container logs. See the [Docker Deployment Guide](README_DOCKER.md) for details.

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

Some models do not support 64k output. Adjust or remove `CLAUDE_CODE_MAX_OUTPUT_TOKENS` according to the upstream model.

### Codex CLI

Use the Responses API in the Codex configuration:

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

If your Codex CLI version still requires `~/.codex/auth.json`, add a placeholder API key:

```json
{"OPENAI_API_KEY":"ainexus-local"}
```

The client-side API key only satisfies the client's configuration requirement. AINexus manages the actual upstream credentials through endpoints or token pools.

## Add Endpoints

Common transformers:

| Transformer | Upstream protocol | Typical use |
|-------------|-------------------|-------------|
| `claude` | Claude / Anthropic | Official or compatible Claude APIs |
| `openai` | OpenAI Chat Completions | OpenAI Chat-compatible upstreams |
| `openai2` | OpenAI Responses | Codex CLI and Responses-compatible upstreams |
| `gemini` | Google Gemini | Native Gemini APIs |
| `deepseek` | OpenAI Chat-compatible | DeepSeek |
| `kimi` | OpenAI Chat-compatible | Kimi / Moonshot |

Transformers other than `claude` normally require a target model.

To use a Codex Token Pool:

1. Select `Codex Token Pool` as the authentication mode
2. Import token JSON records containing `access_token` and `refresh_token`
3. AINexus configures the Codex upstream and `openai2` transformer, then manages rotation, refresh, quota snapshots, and invalid-token isolation

## Core Capabilities

- **One local API provider** for multiple AI clients
- **Endpoint rotation and failover** with request-local fallback that avoids cross-request state pollution
- **Protocol conversion** across Claude, OpenAI Chat, OpenAI Responses, Gemini, DeepSeek, and Kimi/Moonshot
- **Token pool management** with credential rotation, 401 refresh, invalid-token isolation, quotas, and usage statistics
- **Reasoning and streaming controls** with endpoint-level effort, thinking disablement, forced upstream streaming, and SSE heartbeat
- **Live monitoring** for requests, classified errors, endpoint runtime state, request IDs, and per-credential usage
- **Model and compatibility APIs** at `/v1/models`, `/models`, `/api/tags`, `/version`, `/props`, `/health`, and `/stats`
- **Backup and sync** through WebDAV, local backups, and S3-compatible storage

<table>
  <tr>
    <td align="center"><img src="images/EN-Light.png" alt="Light Theme" width="400"></td>
    <td align="center"><img src="images/EN-Dark.png" alt="Dark Theme" width="400"></td>
  </tr>
</table>

## Runtime Modes

| Mode | Entry | Best for |
|------|-------|----------|
| Desktop | `cmd/desktop` | Local GUI, tray operation, visual management |
| Server | `cmd/server` | Servers, NAS, Docker, and web management |

Server mode supports:

| Environment variable | Description | Default |
|----------------------|-------------|---------|
| `AINEXUS_PORT` | HTTP listening port | `3000` |
| `AINEXUS_LISTEN_MODE` | `local` for loopback only; `lan` for all interfaces | `local`; published Docker ports require `lan` |
| `AINEXUS_LOG_LEVEL` | `0` debug, `1` info, `2` warning, `3` error | `1` |
| `AINEXUS_DATA_DIR` | Data directory | User data directory; `/data` in the container |
| `AINEXUS_DB_PATH` | SQLite database path | `ainexus.db` under the data directory |
| `AINEXUS_BASIC_AUTH_ENABLED` | Protect the Web UI and management API | `true` |
| `AINEXUS_BASIC_AUTH_USERNAME` | Basic Auth username | `admin` |
| `AINEXUS_BASIC_AUTH_PASSWORD` | Basic Auth password | Randomly generated on first launch |

> [!WARNING]
> When using `AINEXUS_LISTEN_MODE=lan` or exposing the service publicly, set a strong password and restrict access with a firewall or HTTPS reverse proxy. Basic Auth protects the Web UI and management API, but it is not a replacement for a proper network boundary.

## Differences from the Original Project

AINexus Optimized keeps the unified proxy design of [lich0821/AINexus](https://github.com/lich0821/AINexus) and adds safeguards for long-running, concurrent client workloads:

- Request-local fallback and endpoint cooldowns to reduce cross-request interference
- More precise classification for quota, rate limit, 5xx, network, and authentication failures
- SSE heartbeat, forced upstream streaming, and empty-output detection
- Request IDs, retry reasons, endpoint runtime state, and per-credential statistics

The original design remains a good reference for lightweight local rotation. Optimized is intended for shared providers, token pools, and more complex recovery requirements.

## Develop from Source

```bash
# Desktop development with hot reload
cd cmd/desktop/frontend
npm install
cd ..
wails dev

# Build the server
cd ../../cmd/server
go build -ldflags="-s -w" -o ainexus-server .

# Run all tests from the repository root
cd ../..
go test ./... -count=1
```

See the [Development Guide](development_en.md) for complete prerequisites and cross-platform build commands.

## Documentation

- [Configuration Guide](configuration_en.md)
- [Docker Deployment Guide](README_DOCKER.md)
- [Development Guide](development_en.md)
- [FAQ](FAQ_en.md)
- [Distribution Site Development](../site/README.md)
- [Commercial Delivery Templates](distribution/README.md)

## License

This project is no longer licensed under the MIT License. The source code may be used for non-commercial personal, educational, research, and evaluation purposes; any commercial use requires prior written authorization from the copyright holder. See [LICENSE](../LICENSE).
