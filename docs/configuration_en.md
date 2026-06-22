# Configuration Guide

## Application Defaults

| Setting | Description | Default |
|---------|-------------|---------|
| Proxy port | Local API provider listening port | `3000` |
| Listen mode | `local` for loopback only; `lan` for all interfaces | `local` |
| Log level | `0` debug, `1` info, `2` warning, `3` error | `1` |
| Basic Auth | Protects the server Web UI and management API | Enabled |
| Basic Auth username | Login username | `admin` |
| Language | Chinese / English | `zh-CN` |
| Window close behavior | Close / minimize to tray / ask | Ask |

When server mode starts without a Basic Auth password, AINexus generates a random password and prints it to the log once.

## Endpoint Configuration

### Authentication Modes

| Authentication mode | Description |
|---------------------|-------------|
| `api_key` | Standard API key authentication |
| `token_pool` | Generic token pool with credential rotation |
| `codex_token_pool` | Codex Token Pool using the ChatGPT Codex backend |
| `claude_oauth_token_pool` | Claude OAuth Token Pool |

### Transformers

| Transformer | Description | Model requirement |
|-------------|-------------|-------------------|
| `claude` | Claude / Anthropic API | Optional or used as an override |
| `openai` | OpenAI Chat Completions API | Required |
| `openai2` | OpenAI Responses API | Required |
| `gemini` | Google Gemini API | Required |
| `deepseek` | DeepSeek OpenAI Chat-compatible API | Required |
| `kimi` | Kimi/Moonshot OpenAI Chat-compatible API | Required |

### Configuration Examples

Claude:

```json
{
  "name": "Claude Official",
  "apiUrl": "https://api.anthropic.com",
  "apiKey": "sk-ant-api03-xxx",
  "enabled": true,
  "transformer": "claude"
}
```

OpenAI Chat:

```json
{
  "name": "OpenAI",
  "apiUrl": "https://api.openai.com",
  "apiKey": "sk-xxx",
  "enabled": true,
  "transformer": "openai",
  "model": "gpt-4.1"
}
```

OpenAI Responses:

```json
{
  "name": "OpenAI Responses",
  "apiUrl": "https://api.openai.com",
  "apiKey": "sk-xxx",
  "enabled": true,
  "transformer": "openai2",
  "model": "gpt-5-codex"
}
```

Gemini:

```json
{
  "name": "Gemini",
  "apiUrl": "https://generativelanguage.googleapis.com",
  "apiKey": "AIza-xxx",
  "enabled": true,
  "transformer": "gemini",
  "model": "gemini-2.5-pro"
}
```

DeepSeek:

```json
{
  "name": "DeepSeek",
  "apiUrl": "https://api.deepseek.com",
  "apiKey": "sk-xxx",
  "enabled": true,
  "transformer": "deepseek",
  "model": "deepseek-chat"
}
```

Kimi:

```json
{
  "name": "Kimi",
  "apiUrl": "https://api.moonshot.ai/v1",
  "apiKey": "sk-xxx",
  "enabled": true,
  "transformer": "kimi",
  "model": "kimi-k2"
}
```

Model IDs change over time. Use the model-fetch control in the UI to query the current upstream list.

## Codex Token Pool

When `Codex Token Pool` is selected, AINexus:

- Locks the Codex backend URL and `openai2` transformer
- Rotates credentials automatically
- Attempts token refresh after a 401
- Isolates invalid credentials
- Records per-credential requests, errors, token usage, and quota snapshots

Imported records must include `access_token`. Include `refresh_token` when automatic refresh is required.

## Server Environment Variables

| Environment variable | Description | Default |
|----------------------|-------------|---------|
| `AINEXUS_PORT` | HTTP listening port | `3000` |
| `AINEXUS_LISTEN_MODE` | `local` or `lan` | `local` |
| `AINEXUS_LOG_LEVEL` | `0` through `3` | `1` |
| `AINEXUS_DATA_DIR` | Data directory | `~/.AINexus`; `/data` in Docker |
| `AINEXUS_DB_PATH` | SQLite database path | `<data directory>/ainexus.db` |
| `AINEXUS_BASIC_AUTH_ENABLED` | Enable Web UI/management API login | `true` |
| `AINEXUS_BASIC_AUTH_USERNAME` | Login username | `admin` |
| `AINEXUS_BASIC_AUTH_PASSWORD` | Login password | Randomly generated on first launch |

Legacy `CCNEXUS_*` variables remain as compatibility fallbacks, but new deployments should use `AINEXUS_*`.

## Network and Security

- `local` mode listens on `127.0.0.1`
- `lan` mode listens on `0.0.0.0`
- The Web UI and `/api/` management endpoints are protected by Basic Auth
- Proxy and health endpoints should not be exposed directly to an untrusted public network

Use a strong password, firewall rules, and an HTTPS reverse proxy for remote deployments.

## Data and Backups

- Default data directory: `~/.AINexus/`
- Default database: `~/.AINexus/ainexus.db`
- Default Docker data directory: `/data`

AINexus supports local backups, WebDAV, and S3-compatible storage. For WebDAV, configure the server URL, username, and password, then test the connection before backup or restore.
