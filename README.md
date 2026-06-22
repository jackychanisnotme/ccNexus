<div align="center">

<p align="center">
  <img src="docs/images/AINexus.svg" alt="Claude Code、Codex CLI、Hermes Agent 与 OpenClaw API Provider 热切换中枢" width="720" />
</p>

[![构建状态](https://github.com/jackychanisnotme/AINexus/actions/workflows/build.yml/badge.svg)](https://github.com/jackychanisnotme/AINexus/actions)
[![最新版本](https://img.shields.io/github/v/release/jackychanisnotme/AINexus?label=release)](https://github.com/jackychanisnotme/AINexus/releases/latest)
[![许可证: 商用需授权](https://img.shields.io/badge/License-Commercial%20use%20requires%20authorization-red.svg)](LICENSE)
[![Go 版本](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](https://go.dev/)
[![Wails](https://img.shields.io/badge/Wails-v2-blue)](https://wails.io/)

[English](docs/README_EN.md) | [简体中文](README.md)

</div>

AINexus 是面向 Claude Code、Codex CLI、Hermes Agent、OpenClaw 等 AI 开发工具的本地 API Provider 与资源管理中枢。它统一管理端点、模型、API Key、Token Pool、额度、统计和备份，并在多个上游、账号和模型之间提供热切换与自动故障转移。

> [!IMPORTANT]
> 当前仓库维护 **AINexus Optimized**，重点增强 Codex CLI、OpenAI Responses API、Claude Code、多端点并发和复杂上游错误恢复。
>
> [下载最新版本](https://github.com/jackychanisnotme/AINexus/releases/latest)

## 快速开始

### 桌面应用

从 [Releases](https://github.com/jackychanisnotme/AINexus/releases/latest) 下载对应平台的安装包：

- **macOS**：解压 `.zip`，将 `AINexus.app` 移入「应用程序」；首次运行可右键选择「打开」
- **Windows**：解压 `windows-amd64.zip`，运行 `AINexus.exe`
- **Linux**：建议从源码构建，或使用服务器模式/Docker

启动后点击「添加端点」，填写 API 地址、密钥、认证方式、转换器和模型。默认代理地址为 `http://127.0.0.1:3000`。

### 服务器模式

要求 Go 1.24+：

```bash
go run ./cmd/server
```

启动后访问：

- API Provider：`http://127.0.0.1:3000`
- Web 管理界面：`http://127.0.0.1:3000/ui/`
- 健康检查：`http://127.0.0.1:3000/health`

服务器模式默认启用 Basic Auth，用户名为 `admin`。首次启动且未配置密码时，程序会生成随机密码并在日志中显示一次。

### Docker Compose

首次启动前，确认 `cmd/server/docker-compose.yml` 的 `environment` 包含：

```yaml
- AINEXUS_LISTEN_MODE=lan
```

容器需要监听所有网卡，Docker 的宿主机端口映射才能访问服务。然后执行：

```bash
cd cmd/server
docker compose up -d --build
docker compose logs -f ainexus
```

默认将宿主机 `3021` 端口映射到容器的 `3000` 端口：

- Web 管理界面：`http://127.0.0.1:3021/ui/`
- 健康检查：`http://127.0.0.1:3021/health`

数据保存在 `cmd/server/ainexus/`。首次启动密码可从容器日志中查看。更多说明见 [Docker 部署指南](docs/README_DOCKER.md)。

## 配置客户端

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

部分模型不支持 64k 输出，可按上游能力调整或移除 `CLAUDE_CODE_MAX_OUTPUT_TOKENS`。

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

如果你的 Codex CLI 版本仍要求 `~/.codex/auth.json`，可写入占位 API Key：

```json
{"OPENAI_API_KEY":"ainexus-local"}
```

客户端侧 API Key 只用于满足客户端配置要求；真正的上游认证由 AINexus 端点或 Token Pool 管理。

## 添加端点

常用转换器：

| 转换器 | 上游协议 | 典型场景 |
|--------|----------|----------|
| `claude` | Claude / Anthropic | Claude 官方或兼容接口 |
| `openai` | OpenAI Chat Completions | OpenAI Chat 兼容上游 |
| `openai2` | OpenAI Responses | Codex CLI、Responses 兼容上游 |
| `gemini` | Google Gemini | Gemini 原生接口 |
| `deepseek` | OpenAI Chat 兼容 | DeepSeek |
| `kimi` | OpenAI Chat 兼容 | Kimi / Moonshot |

除 `claude` 外的转换器通常需要填写目标模型。

使用 Codex Token Pool 时：

1. 认证方式选择 `Codex Token Pool`
2. 在 Token Pool 页面导入包含 `access_token` 和 `refresh_token` 的 token JSON
3. AINexus 自动设置 Codex 上游地址与 `openai2` 转换器，并处理轮换、刷新、额度快照和失效隔离

## 核心能力

- **统一 API Provider**：多个 AI 客户端接入同一个本地地址
- **多端点轮换与故障转移**：单次请求内 fallback，避免并发请求污染全局默认端点
- **协议转换**：支持 Claude、OpenAI Chat、OpenAI Responses、Gemini、DeepSeek、Kimi/Moonshot
- **Token Pool 管理**：凭证轮换、401 刷新、失效隔离、额度与用量统计
- **推理与流式控制**：端点级 reasoning 强度、关闭 thinking、上游强制流式和 SSE heartbeat
- **实时监控**：请求统计、错误分类、端点运行态、Request ID 和凭证级用量
- **模型与兼容接口**：提供 `/v1/models`、`/models`、`/api/tags`、`/version`、`/props`、`/health`、`/stats`
- **备份同步**：支持 WebDAV、本地备份和 S3 兼容存储

<table>
  <tr>
    <td align="center"><img src="docs/images/CN-Light.png" alt="明亮主题" width="400"></td>
    <td align="center"><img src="docs/images/CN-Dark.png" alt="暗黑主题" width="400"></td>
  </tr>
</table>

## 运行模式

| 模式 | 入口 | 适合场景 |
|------|------|----------|
| 桌面模式 | `cmd/desktop` | 本机 GUI、托盘运行、可视化管理 |
| 服务器模式 | `cmd/server` | 服务器、NAS、Docker、Web 管理 |

服务器模式支持：

| 环境变量 | 说明 | 默认值 |
|----------|------|--------|
| `AINEXUS_PORT` | HTTP 监听端口 | `3000` |
| `AINEXUS_LISTEN_MODE` | `local` 仅本机；`lan` 监听所有网卡 | `local`；Docker 端口映射需设为 `lan` |
| `AINEXUS_LOG_LEVEL` | `0` 调试、`1` 信息、`2` 警告、`3` 错误 | `1` |
| `AINEXUS_DATA_DIR` | 数据目录 | 用户数据目录；容器内为 `/data` |
| `AINEXUS_DB_PATH` | SQLite 数据库路径 | 数据目录下的 `ainexus.db` |
| `AINEXUS_BASIC_AUTH_ENABLED` | 是否保护 Web UI 和管理 API | `true` |
| `AINEXUS_BASIC_AUTH_USERNAME` | Basic Auth 用户名 | `admin` |
| `AINEXUS_BASIC_AUTH_PASSWORD` | Basic Auth 密码 | 首次启动随机生成 |

> [!WARNING]
> 使用 `AINEXUS_LISTEN_MODE=lan` 或对公网暴露服务时，请设置强密码，并通过防火墙或 HTTPS 反向代理限制访问。Basic Auth 保护 Web UI 和管理 API，不应替代完整的网络边界保护。

## 与初代版本的差异

AINexus Optimized 延续了 [lich0821/AINexus](https://github.com/lich0821/AINexus) 的统一代理入口设计，并面向长期运行和多客户端并发加强：

- 请求级 fallback 与端点冷却，减少不同请求之间的状态干扰
- 更细的额度耗尽、限流、5xx、网络和认证错误分类
- SSE heartbeat、强制上游流式和空输出检测
- Request ID、重试原因、端点运行态及凭证级统计

轻量、本地、简单轮换场景可以参考初代设计；需要共享 Provider、Token Pool 和复杂错误恢复时，Optimized 版本提供更完整的运行保障。

## 从源码开发

```bash
# 桌面开发（热重载）
cd cmd/desktop/frontend
npm install
cd ..
wails dev

# 构建服务器
cd ../../cmd/server
go build -ldflags="-s -w" -o ainexus-server .

# 运行全部测试（仓库根目录）
cd ../..
go test ./... -count=1
```

完整依赖和跨平台构建命令见 [开发指南](docs/development.md)。

## 文档

- [详细配置](docs/configuration.md)
- [Docker 部署指南](docs/README_DOCKER.md)
- [开发指南](docs/development.md)
- [常见问题](docs/FAQ.md)
- [独立站开发说明](site/README.md)
- [商业交付模板](docs/distribution/README.md)

## 许可证

本项目不再采用 MIT 许可证。源码可用于非商业个人、学习、研究与评估用途；任何商业使用都必须先获得版权所有者的书面授权。详见 [LICENSE](LICENSE)。
