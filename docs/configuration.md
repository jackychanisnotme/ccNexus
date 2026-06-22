# 详细配置

## 应用默认值

| 设置项 | 说明 | 默认值 |
|--------|------|--------|
| 代理端口 | 本地 API Provider 监听端口 | `3000` |
| 监听模式 | `local` 仅本机；`lan` 监听所有网卡 | `local` |
| 日志级别 | `0` 调试、`1` 信息、`2` 警告、`3` 错误 | `1` |
| Basic Auth | 保护服务器 Web UI 与管理 API | 开启 |
| Basic Auth 用户名 | 登录用户名 | `admin` |
| 界面语言 | 中文 / English | `zh-CN` |
| 窗口关闭行为 | 关闭 / 最小化到托盘 / 每次询问 | 每次询问 |

服务器模式首次启动且未设置 Basic Auth 密码时，会生成随机密码并在日志中显示一次。

## 端点配置

### 认证模式

| 认证模式 | 说明 |
|----------|------|
| `api_key` | 标准 API Key 认证 |
| `token_pool` | 通用 Token Pool，自动轮换凭证 |
| `codex_token_pool` | Codex Token Pool，自动使用 ChatGPT Codex 后端 |
| `claude_oauth_token_pool` | Claude OAuth Token Pool |

### 转换器

| 转换器 | 说明 | 模型要求 |
|--------|------|----------|
| `claude` | Claude / Anthropic API | 可留空或覆盖模型 |
| `openai` | OpenAI Chat Completions API | 必填 |
| `openai2` | OpenAI Responses API | 必填 |
| `gemini` | Google Gemini API | 必填 |
| `deepseek` | DeepSeek OpenAI Chat 兼容 API | 必填 |
| `kimi` | Kimi/Moonshot OpenAI Chat 兼容 API | 必填 |

### 配置示例

Claude：

```json
{
  "name": "Claude Official",
  "apiUrl": "https://api.anthropic.com",
  "apiKey": "sk-ant-api03-xxx",
  "enabled": true,
  "transformer": "claude"
}
```

OpenAI Chat：

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

OpenAI Responses：

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

Gemini：

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

DeepSeek：

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

Kimi：

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

模型名称会随上游变化，也可以使用界面中的模型拉取功能查询当前可用列表。

## Codex Token Pool

选择 `Codex Token Pool` 后，AINexus 会：

- 固定使用 Codex 后端地址和 `openai2` 转换器
- 在凭证之间自动轮换
- 在 401 后尝试刷新 token
- 隔离失效凭证
- 记录凭证级请求、错误、Token 用量和额度快照

导入记录应包含 `access_token`，需要自动刷新时还应包含 `refresh_token`。

## 服务器环境变量

| 环境变量 | 说明 | 默认值 |
|----------|------|--------|
| `AINEXUS_PORT` | HTTP 监听端口 | `3000` |
| `AINEXUS_LISTEN_MODE` | `local` 或 `lan` | `local` |
| `AINEXUS_LOG_LEVEL` | `0` 到 `3` | `1` |
| `AINEXUS_DATA_DIR` | 数据目录 | `~/.AINexus`；容器内为 `/data` |
| `AINEXUS_DB_PATH` | SQLite 数据库路径 | `<数据目录>/ainexus.db` |
| `AINEXUS_BASIC_AUTH_ENABLED` | 启用 Web UI/管理 API 登录 | `true` |
| `AINEXUS_BASIC_AUTH_USERNAME` | 登录用户名 | `admin` |
| `AINEXUS_BASIC_AUTH_PASSWORD` | 登录密码 | 首次启动随机生成 |

旧的 `CCNEXUS_*` 环境变量仍作为兼容回退，但新部署应使用 `AINEXUS_*`。

## 网络与安全

- `local` 模式监听 `127.0.0.1`
- `lan` 模式监听 `0.0.0.0`
- Web UI 和 `/api/` 管理接口受 Basic Auth 保护
- 代理接口与健康检查不应直接暴露到不可信公网

远程部署建议使用强密码、防火墙和 HTTPS 反向代理。

## 数据与备份

- 默认数据目录：`~/.AINexus/`
- 默认数据库：`~/.AINexus/ainexus.db`
- Docker 默认数据目录：`/data`

AINexus 支持本地备份、WebDAV 和 S3 兼容存储。WebDAV 配置需填写服务器地址、用户名和密码，并先执行连接测试。
