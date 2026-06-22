# AGENTS.md

This file provides guidance to Codex (Codex.ai/code) when working with code in this repository.

## 项目概述

AINexus 是一个智能 API 端点轮换代理，专为 Codex 和 Codex CLI 设计。

**核心功能：**
- 多端点轮换与自动故障转移
- API 格式转换（Codex ↔ OpenAI ↔ Gemini）
- Codex Token Pool 管理（自动轮换、刷新、失效隔离）
- 实时统计与监控
- WebDAV 云同步
- 在线卡密授权与设备激活
- 授权服务器管理后台

**两种运行模式：**
- **桌面模式**：基于 Wails v2 的跨平台 GUI 应用（`cmd/desktop/`）
- **服务器模式**：无头 HTTP API 代理（`cmd/server/`）
- **授权服务**：独立 HTTP 服务（`cmd/license-server/`），负责卡密生成、联网激活、续期、禁用与设备管理

## 常用开发命令

### 开发与构建
```bash
# 桌面应用开发模式（支持热重载）
cd cmd/desktop && wails dev

# 构建桌面应用（指定平台）
wails build -platform linux/amd64    # Linux
wails build -platform darwin/amd64   # macOS
wails build -platform windows/amd64  # Windows

# 构建服务器
cd cmd/server && go build -ldflags="-s -w" -o ainexus-server .

# 运行服务器
cd cmd/server && go run main.go

# 构建授权服务
cd cmd/license-server && go build -ldflags="-s -w" -o ccnexus-license .

# 运行授权服务
cd cmd/license-server && go run main.go

# 桌面端前端修改后先构建 dist
cd cmd/desktop/frontend && npm install && npm run build
```

### 测试
```bash
# 运行所有测试
go test ./... -count=1

# 如果改了 cmd/desktop/frontend，先生成 dist 再跑 Go 测试
cd cmd/desktop/frontend && npm install && npm run build
cd ../../.. && go test ./... -count=1

# 运行特定目录的测试
cd internal/proxy && go test -v ./...
cd internal/transformer/convert && go test -v ./...
```

### Docker
```bash
# 构建镜像
docker build -f cmd/server/Dockerfile -t ainexus .

# 使用 docker-compose
cd cmd/server && docker-compose up -d
```

### 代码质量
```bash
go fmt ./...    # 格式化代码
go vet ./...    # 静态分析
go mod tidy     # 清理依赖
```

## 核心架构

### 目录结构
```
AINexus/
├── cmd/
│   ├── desktop/          # 桌面应用入口（Wails）
│   │   ├── frontend/     # Vue.js 前端
│   │   └── main.go       # 桌面应用入口
│   ├── server/           # 服务器模式入口
│   │   └── main.go       # 服务器入口
│   └── license-server/   # 在线授权服务入口
│       └── main.go       # 授权后台与激活 API
└── internal/
    ├── proxy/            # HTTP 代理核心
    ├── transformer/      # API 格式转换器
    ├── storage/          # SQLite 数据存储
    ├── onlinelicense/    # 在线授权、卡密、设备激活与票据
    ├── config/           # 配置管理
    ├── webdav/           # WebDAV 同步
    ├── logger/           # 日志系统
    └── tray/             # 系统托盘（桌面模式）
```

### 关键组件

**代理层** (`internal/proxy/proxy.go`)
- 管理多个 API 端点，自动故障转移
- 跟踪当前端点和活动请求
- 使用连接池优化的 HTTP 客户端
- 处理流式和非流式响应

**转换器** (`internal/transformer/`)
- 在不同 API 格式之间转换请求和响应
- 支持流式传输的增量转换
- 处理工具调用和函数调用
- 类型定义：`internal/transformer/types.go`

**存储层** (`internal/storage/sqlite.go`)
- SQLite WAL 模式数据库
- 管理端点、凭证、使用统计、应用配置
- 线程安全操作

**在线授权层** (`internal/onlinelicense/`)
- 授权服务器只保存卡密哈希，不保存明文卡密
- 客户端提交卡密和设备 ID 联网激活
- 服务器用 Ed25519 签发授权票据
- 最近一次在线校验成功后，客户端可离线宽限 30 天
- 不要恢复旧离线注册机、`cmd/licensegen-*`、`internal/license*` 流程

### 关键文件路径
- 数据库：`~/.AINexus/ainexus.db`
- 配置常量：`internal/config/config.go`（第 13-20 行：认证模式和端点 URL）
- 代理路由：`internal/proxy/proxy.go`（第 108-114 行）
- 授权服务入口：`cmd/license-server/main.go`
- 授权核心：`internal/onlinelicense/`
- 授权维护文档：`docs/ccnexus-online-license.md`、`docs/ccnexus-online-license-maintenance.md`

## 端点配置

### 认证模式（`internal/config/config.go`）
- `api_key`：标准 API 密钥认证
- `token_pool`：Token 池（自动轮换）
- `codex_token_pool`：Codex Token Pool（使用 ChatGPT 后端）

### 转换器类型
- `Codex`：Codex API
- `openai`：OpenAI Chat API
- `openai2`：OpenAI Response API
- `gemini`：Google Gemini API

### 端点配置规则
在 `internal/config/config.go` 的 `ApplyEndpointAuthModeRules` 函数中定义：
- Codex Token Pool 自动设置 API URL 和转换器
- Token Pool 模式会清空 APIKey
- URL 标准化处理

## API 端点

代理服务器提供以下端点（`internal/proxy/proxy.go` 第 108-114 行）：
- `/` - 主代理路由（所有 API 请求）
- `/v1/messages/count_tokens` - Token 计数
- `/v1/models` - 模型列表（带缓存）
- `/health` - 健康检查
- `/stats` - 统计数据

## 环境变量

服务器模式支持以下环境变量（`cmd/server/main.go`）：
- `AINEXUS_PORT` - 覆盖默认端口
- `AINEXUS_LOG_LEVEL` - 日志级别
- `AINEXUS_DB_PATH` - 数据库路径
- `AINEXUS_DATA_DIR` - 数据目录
- `AINEXUS_BASIC_AUTH_USERNAME` - Basic Auth 用户名
- `AINEXUS_BASIC_AUTH_PASSWORD` - Basic Auth 密码

在线授权支持以下环境变量（`cmd/license-server/main.go`、`internal/onlinelicense/`）：
- `CCNEXUS_LICENSE_SERVER_URL` - 客户端授权服务器地址
- `CCNEXUS_LICENSE_PUBLIC_KEY` - 客户端嵌入的授权公钥
- `CCNEXUS_LICENSE_PORT` - 授权服务端口
- `CCNEXUS_LICENSE_BIND` - 授权服务绑定地址
- `CCNEXUS_LICENSE_DATA_DIR` - 授权数据目录
- `CCNEXUS_LICENSE_DB_PATH` - 授权 SQLite 数据库路径
- `CCNEXUS_LICENSE_KEY_PATH` - 授权私钥路径
- `CCNEXUS_LICENSE_ADMIN_USERNAME` - 授权后台用户名
- `CCNEXUS_LICENSE_ADMIN_PASSWORD` - 授权后台密码

## 在线授权与共享服务器

当前授权服务部署在共享服务器 `207.57.134.147`：

- 目录：`/var/www/ccnexus-license`
- PM2 进程：`ccnexus-license`
- 端口：`24220`
- 管理后台：`http://207.57.134.147:24220/admin/`

SSH 连接方式（仅从已配置私钥的本机使用）：
```bash
ssh -i ~/.ssh/wenche_ai_deploy -p 24070 root@207.57.134.147
```

共享服务器预检（部署或修改服务前先运行，只读命令优先）：
```bash
hostname
date
df -h
free -h
ss -ltnp
pm2 list
ls -la /var/www
ls -la /etc/nginx/sites-enabled
nginx -t
```

维护规则：
- 修改在线授权前先读 `docs/ccnexus-online-license.md` 和 `docs/ccnexus-online-license-maintenance.md`。
- 私钥 `~/.ssh/wenche_ai_deploy` 只能在本机使用，不要打印、复制、上传、提交、改名或写入脚本。
- 共享服务器已有 `wenche-ai` 和 `flower-logistics`，不要删除、重启、改配置或占用它们的目录、PM2 进程、Nginx 配置、端口。
- 不要占用或改动 `80`、`443`、`24070`、`5432`、`8787`、`24175`。
- 授权服务初期直连 `0.0.0.0:24220`；以后切域名/HTTPS 时，只新增独立 Nginx 配置，不要改现有项目配置。
- 客户 App 必须联网激活；不要重新引入本地注册机或离线卡密方案。

## 依赖

- Go 1.24+
- Wails v2（桌面模式）
- Node.js 18+（前端开发）
- SQLite（modernc.org/sqlite，纯 Go 实现）

## 代码规范

**静态函数命名**：所有静态函数必须使用 `__` 前缀表示内部可见性

```c
// 符合规范
static int __internal_helper_function(int param) {
    return param + 1;
}

// 不符合规范
static int internal_helper_function(int param) {
    return param + 1;
}
```

**变量声明**：所有局部变量必须在函数体开头声明，并在声明时显式初始化

```c
// 符合规范
int function_name(void) {
    int ret = 0;
    int value = 0;
    char buffer[256] = {0};
    char *ptr = NULL;

    /* 可执行语句 */
    ret = do_something();
}

// 不符合规范
int function_name(void) {
    int ret = 0;
    ret = do_something();
    int value = 0;  /* 错误：在可执行语句后声明 */
}
```
