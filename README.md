<div align="center">

<p align="center">
  <img src="docs/images/AINexus.svg" alt="AINexus - API Provider, Token Pool and Agent hub for AI coding tools" width="720" />
</p>

[![Pre-release](https://img.shields.io/badge/pre--release-v6.3.6-blue)](https://github.com/jackychanisnotme/ccNexus/releases/tag/v6.3.6)
[![License](https://img.shields.io/badge/License-Commercial%20use%20requires%20authorization-red.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](https://go.dev/)
[![Wails](https://img.shields.io/badge/Wails-v2-blue)](https://wails.io/)

[English](docs/README_EN.md) | [简体中文](README.md)

</div>

AINexus 是面向 Claude Code、Codex CLI、OpenClaw、Hermes Agent 以及 OpenAI/Claude/Gemini 兼容客户端的本地 API Provider、Token Pool 和 Agent 管理中枢。它把端点、模型、API Key、订阅 Token、授权状态、统计、备份和智能体配置统一到一个桌面应用与服务器模式中。

6.3.6 重点增强了在线卡密授权、Codex Token Pool 额度与凭证级用量统计、Claude OAuth Token Pool、AI Agent 工作台、Agent Provider 配置修复、多端点故障转移和跨平台客户包分发。

> [!IMPORTANT]
> 当前发布为 **AINexus v6.3.6 pre-release**。下载地址：
> [https://github.com/jackychanisnotme/ccNexus/releases/tag/v6.3.6](https://github.com/jackychanisnotme/ccNexus/releases/tag/v6.3.6)

> [!NOTE]
> AINexus Pro 使用在线卡密激活。授权服务签发 Ed25519 票据，客户端在最近一次在线校验成功后可离线宽限 30 天。购买或续期卡密请联系微信：`yo22bro`。

## 界面预览

<table>
  <tr>
    <td align="center"><img src="docs/images/CN-Light.png" alt="AINexus 6.3.6 明亮主题" width="400"></td>
    <td align="center"><img src="docs/images/CN-Dark.png" alt="AINexus 6.3.6 暗黑主题" width="400"></td>
  </tr>
</table>

## 快速开始

### 桌面应用

从 [v6.3.6 pre-release](https://github.com/jackychanisnotme/ccNexus/releases/tag/v6.3.6) 下载对应平台：

- **macOS**：下载 `AINexus-v6.3.6-darwin-universal.zip`，解压后将 `AINexus.app` 移入「应用程序」。首次打开如遇系统拦截，可右键选择「打开」。
- **Windows**：下载 `AINexus-v6.3.6-windows-amd64.zip`，解压后运行 `AINexus.exe`。
- **服务器/NAS/Docker**：使用服务器模式或 Docker Compose。

首次启动后在授权窗口输入在线卡密。激活成功后，默认代理地址为：

```text
http://127.0.0.1:3000
```

### 服务器模式

```bash
go run ./cmd/server
```

可用地址：

- API Provider：`http://127.0.0.1:3000`
- Web 管理界面：`http://127.0.0.1:3000/ui/`
- 健康检查：`http://127.0.0.1:3000/health`

服务器模式同样需要在线授权。可通过命令行激活：

```bash
CCNEXUS_LICENSE_PUBLIC_KEY=<public-key> go run ./cmd/server -activate <card-key>
```

### Docker Compose

```bash
cd cmd/server
docker compose up -d --build
docker compose logs -f ainexus
```

Docker 场景如需局域网访问，需要配置：

```yaml
- AINEXUS_LISTEN_MODE=lan
```

默认 Web UI：`http://127.0.0.1:3021/ui/`。更多见 [Docker 部署指南](docs/README_DOCKER.md)。

## 连接客户端

### Claude Code

编辑 `~/.claude/settings.json`：

```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "ainexus",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:3000",
    "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "64000"
  }
}
```

### Codex CLI

在 Codex 配置中使用 Responses API：

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

如果 Codex CLI 仍要求 `~/.codex/auth.json`，可写入占位 key：

```json
{"OPENAI_API_KEY":"ainexus-local"}
```

真正的上游认证由 AINexus 端点或 Token Pool 管理。

## 6.3.6 核心能力

### API Provider 与端点故障转移

- 多端点轮换、请求级 fallback、端点冷却和默认端点热切换。
- 支持按端点类型、可用性、启用状态、客户端 IP 过滤统计和列表。
- 支持强制上游流式，并为非流式客户端聚合 SSE 响应。
- 对限流、额度耗尽、上游 5xx、网络错误、认证失败和空输出做独立分类与冷却。

### 协议转换

| 转换器 | 上游协议 | 典型用途 |
|--------|----------|----------|
| `claude` | Claude / Anthropic | Claude 官方或兼容接口 |
| `openai` | OpenAI Chat Completions | Chat Completions 兼容上游 |
| `openai2` | OpenAI Responses | Codex CLI / Responses API |
| `gemini` | Google Gemini | Gemini 原生接口 |
| `deepseek` | OpenAI Chat 兼容 | DeepSeek |
| `kimi` | OpenAI Chat 兼容 | Kimi / Moonshot |
| `poe` | OpenAI Chat 兼容 | Poe bot |

### Token Pool

- **API Token Pool**：普通 API Token 轮换、启用/禁用、失败隔离和凭证级请求统计。
- **Codex Token Pool**：固定 ChatGPT Codex 上游与 Responses 转换器，支持 Codex 登录凭据、token 刷新、额度刷新、额度快照和凭证级 token 用量统计。
- **Claude OAuth Token Pool（实验）**：支持 Claude Code 订阅 OAuth，允许导入 setup-token 或发现本机 Claude 凭据。
- Token Pool 可以按账号/邮箱覆盖导入、多文件批量导入、查看最后错误、刷新单条凭证、查看单条凭证用量。

### 在线授权

- 客户端输入卡密联网激活，服务器仅保存卡密哈希。
- 服务器用 Ed25519 签发授权票据；客户端嵌入服务器公钥校验票据。
- 最近一次在线校验成功后可离线宽限 30 天。
- 授权后台支持生成卡密、限制设备数、禁用卡密、禁用设备授权、修改设备到期时间、设备备注和审计记录。

### AI Agent 与 Provider 管理

- 桌面端内置 AI Agent 工作台，可保存本地会话和任务上下文。
- Agent Provider 面板可检查 Codex CLI、Claude Code、OpenClaw 等本地配置健康状态。
- 支持生成备份、修复 provider 地址、恢复配置备份和查看修复结果。

### 统计、会话与运维

- 今日、昨日、周、月、历史统计，按端点和客户端 IP 过滤。
- 端点级和凭证级请求数、错误数、输入/输出 token 统计。
- Codex 额度从响应头、SSE 事件和手动刷新中捕获并持久化。
- 内置启动器、终端配置、Codex 会话历史查看、日志面板和更新检查。
- 支持 WebDAV、本地目录和 S3 兼容存储备份配置与统计。

## 运行模式

| 模式 | 入口 | 适合场景 |
|------|------|----------|
| 桌面模式 | `cmd/desktop` | 本机 GUI、托盘、授权激活、端点与 Token Pool 管理 |
| 服务器模式 | `cmd/server` | 服务器、NAS、Docker、团队内共享 API Provider |
| 授权服务 | `cmd/license-server` | 卡密生成、设备激活、续期、禁用与后台运营 |

## 环境变量

### 服务器模式

| 变量 | 说明 | 默认值 |
|------|------|--------|
| `AINEXUS_PORT` | HTTP 监听端口 | `3000` |
| `AINEXUS_LISTEN_MODE` | `local` 仅本机；`lan` 监听所有网卡 | `local` |
| `AINEXUS_LOG_LEVEL` | `0` 调试、`1` 信息、`2` 警告、`3` 错误 | `1` |
| `AINEXUS_DATA_DIR` | 数据目录 | 用户数据目录；容器内为 `/data` |
| `AINEXUS_DB_PATH` | SQLite 数据库路径 | 数据目录下 `ainexus.db` |
| `AINEXUS_BASIC_AUTH_ENABLED` | 是否保护 Web UI 和管理 API | `true` |
| `AINEXUS_BASIC_AUTH_USERNAME` | Basic Auth 用户名 | `admin` |
| `AINEXUS_BASIC_AUTH_PASSWORD` | Basic Auth 密码 | 首次启动随机生成 |

### 在线授权

| 变量 | 说明 |
|------|------|
| `CCNEXUS_LICENSE_SERVER_URL` | 客户端授权服务器地址 |
| `CCNEXUS_LICENSE_PUBLIC_KEY` | 客户端嵌入的授权公钥 |
| `CCNEXUS_LICENSE_PORT` | 授权服务端口 |
| `CCNEXUS_LICENSE_BIND` | 授权服务监听地址 |
| `CCNEXUS_LICENSE_DATA_DIR` | 授权服务数据目录 |
| `CCNEXUS_LICENSE_DB_PATH` | 授权 SQLite 数据库 |
| `CCNEXUS_LICENSE_KEY_PATH` | 授权私钥路径 |
| `CCNEXUS_LICENSE_ADMIN_USERNAME` | 授权后台用户名 |
| `CCNEXUS_LICENSE_ADMIN_PASSWORD` | 授权后台密码 |

> [!WARNING]
> 开启 `AINEXUS_LISTEN_MODE=lan` 或对公网暴露服务时，请设置强密码，并通过防火墙、VPN 或 HTTPS 反向代理限制访问。

## 从源码开发

```bash
# 桌面端
cd cmd/desktop/frontend
npm install
npm run build
cd ..
wails dev

# 服务器
cd ../../cmd/server
go build -ldflags="-s -w" -o ainexus-server .

# 授权服务
cd ../license-server
go build -ldflags="-s -w" -o ccnexus-license .

# 仓库根目录验证
cd ../..
go test ./... -count=1
go vet ./...
```

## 文档

- [详细配置](docs/configuration.md)
- [Docker 部署指南](docs/README_DOCKER.md)
- [在线授权说明](docs/ccnexus-online-license.md)
- [开发指南](docs/development.md)
- [常见问题](docs/FAQ.md)
- [商业交付模板](docs/distribution/README.md)

## 许可证

本项目源码可用于非商业个人、学习、研究与评估用途；任何商业使用都必须先获得版权所有者的书面授权。详见 [LICENSE](LICENSE)。
