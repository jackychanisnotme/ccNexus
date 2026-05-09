<div align="center">

<p align="center">
  <img src="docs/images/ccNexus.svg" alt="Claude Code、Codex CLI 与 Hermes Agent API 资源管理中枢" width="720" />
</p>

[![构建状态](https://github.com/jackychanisnotme/ccNexus/actions/workflows/build.yml/badge.svg)](https://github.com/jackychanisnotme/ccNexus/actions)
[![最新版本](https://img.shields.io/github/v/release/jackychanisnotme/ccNexus?label=release)](https://github.com/jackychanisnotme/ccNexus/releases/latest)
[![许可证: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)
[![Go 版本](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](https://go.dev/)
[![Wails](https://img.shields.io/badge/Wails-v2-blue)](https://wails.io/)

[English](docs/README_EN.md) | [简体中文](README.md)

</div>

ccNexus 不只是 Claude Code、Codex CLI 与 Hermes Agent 的智能端点轮换代理，也是一套面向 AI 开发工作流的 API 资源管理系统。它把端点、模型、密钥、Codex Token Pool、额度、统计和备份统一管理起来，再对外提供一个稳定的本地 API 入口。

> [!IMPORTANT]
> 当前仓库维护 Optimized 版本，重点增强 Codex CLI、Claude Code、Hermes Agent、OpenAI Responses API、DeepSeek、Kimi 等兼容场景。
>
> 最新发布：[`ccNexus Optimized`](https://github.com/jackychanisnotme/ccNexus/releases/latest)

## 功能特性

- **统一代理入口**：Claude Code、Codex CLI、Hermes Agent、OpenAI Chat/Responses 兼容客户端都可以接入同一个本地地址
- **API 资源管理**：集中管理端点、模型、API Key、Token Pool、额度快照、用量统计和备份数据
- **多端点轮换与故障转移**：按顺序轮换可用端点，失败自动跳过并切换，降低单个上游异常对工作流的影响
- **多协议格式转换**：支持 Claude、OpenAI Chat、OpenAI Responses、Gemini、DeepSeek、Kimi/Moonshot 等格式互转
- **Codex Token Pool**：批量导入 `access_token/refresh_token`，自动轮换、401 后刷新、失效隔离，并固定适配 ChatGPT Codex 后端
- **Token Pool 额度与用量统计**：捕获 Codex 额度快照，按单条凭证展示请求数、错误数、Token 用量和最近使用状态
- **端点级推理控制**：为支持的端点配置 `low` / `medium` / `high` / `xhigh` 推理强度，也可显式关闭上游 thinking
- **上游强制流式兼容**：当上游拒绝非流式请求时，可强制使用流式上游并为非流式客户端聚合结果
- **模型聚合与兼容接口**：提供 `/v1/models`、`/models`、`/api/tags`、`/version`、`/props`、`/health`、`/stats` 等接口，便于客户端探测和监控
- **实时统计与可视化**：事件驱动更新，支持今日/昨日/本周/本月快速切换，并可按端点、凭证维度查看
- **桌面端 + 服务器端**：Wails 桌面应用适合本机使用，`cmd/server` 无头模式适合服务器、NAS 或 Docker 部署
- **备份同步**：支持 WebDAV、本地备份和 S3 兼容存储，便于多设备迁移配置与统计数据

## 客户端兼容状态

| 客户端 | 推荐接入方式 | 当前状态 |
|--------|--------------|----------|
| Claude Code | Claude / Anthropic 兼容入口 | 稳定支持 |
| Codex CLI | OpenAI Responses API，推荐 `openai2` 转换器 | 稳定支持 |
| Hermes Agent | 按其客户端协议选择 Claude 或 OpenAI 兼容入口 | 稳定支持 |
| OpenClaw | 可尝试 Claude 或 OpenAI 兼容入口 | 实验性支持，部分流式、工具调用和异常重试场景鲁棒性一般 |

<table>
  <tr>
    <td align="center"><img src="docs/images/CN-Light.png" alt="明亮主题" width="400"></td>
    <td align="center"><img src="docs/images/CN-Dark.png" alt="暗黑主题" width="400"></td>
  </tr>
</table>

## 快速开始

### 1. 下载安装

[下载当前 fork 最新版本](https://github.com/jackychanisnotme/ccNexus/releases/latest)

- **macOS**：下载 `.zip` 后解压，将 `ccNexus.app` 移动到「应用程序」，首次运行右键点击 → 打开
- **Windows**：下载 `windows-amd64.zip` 后解压，运行 `ccNexus.exe`
- **Linux**：可从源码构建，或使用服务器模式/Docker 部署
- **服务器模式**：`cd cmd/server && go run main.go`

### 2. 添加端点

点击「添加端点」，填写 API 地址、密钥、认证方式、转换器和目标模型。

常用转换器：
- `claude`：Claude / Anthropic 兼容接口
- `openai`：OpenAI Chat Completions 兼容接口
- `openai2`：OpenAI Responses API，推荐给 Codex CLI
- `gemini`：Google Gemini
- `deepseek`：DeepSeek Chat 兼容接口
- `kimi`：Kimi / Moonshot 兼容接口

如需使用 Codex Token Pool：
- 认证方式选择 `Codex Token Pool`
- 在 Token Pool 页面导入一批 token JSON（支持 `access_token` + `refresh_token`）
- 系统会自动设置上游地址与 `openai2` 转换器，并处理 token 轮换、401 后刷新、额度快照和状态管理

可选增强：
- 对支持 reasoning 的端点启用「推理」，选择推理强度
- 上游只接受流式时，启用「上游强制流式」
- 点击模型选择旁的拉取按钮，快速获取上游模型列表

### 3. 配置客户端

#### Claude Code
`~/.claude/settings.json`
```json
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "随便写，不重要",
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:3000",
    "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "64000", // 有些模型可能不支持 64k
  }
  // 其他配置
}

```

#### Codex CLI
推荐使用 Responses API：
```toml
model_provider = "ccNexus"
model = "gpt-5-codex"
preferred_auth_method = "apikey"

[model_providers.ccNexus]
name = "ccNexus"
base_url = "http://localhost:3000/v1"
wire_api = "responses"  # 或 "chat"

# 其他配置
```

`~/.codex/auth.json` 可以忽略，认证由 ccNexus 端点或 Token Pool 负责。

## 运行模式

| 模式 | 入口 | 适合场景 |
|------|------|----------|
| 桌面模式 | `cmd/desktop` | 本机 GUI、托盘运行、可视化端点和 Token Pool 管理 |
| 服务器模式 | `cmd/server` | 远程服务器、NAS、Docker、无头 API 代理 |

服务器模式支持 `CCNEXUS_PORT`、`CCNEXUS_LOG_LEVEL`、`CCNEXUS_DB_PATH`、`CCNEXUS_DATA_DIR`、`CCNEXUS_BASIC_AUTH_USERNAME`、`CCNEXUS_BASIC_AUTH_PASSWORD` 等环境变量。

## 文档

- [详细配置](docs/configuration.md)
- [开发指南](docs/development.md)
- [常见问题](docs/FAQ.md)

## 许可证

[MIT](LICENSE)
