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

## 当前产品阶段与最高优先级

AINexus 已经开始正式分发给客户使用。后续仍会持续增加桌面端、服务器模式和授权服务器功能，例如服务器端查看客户端 Token 消耗、远程管理客户端端点开关/顺序、运营后台统计等。

**最高优先级：任何新增功能、重构、数据库迁移、部署或远程管理能力，都必须首先保证不影响存量客户继续使用。**

这条优先级高于功能推进速度。实现前必须主动检查：
- 已安装客户是否能继续启动并通过授权校验。
- 旧版客户端签发过的在线票据、离线宽限、卡密兑换记录是否仍可校验。
- 现有 `~/.AINexus/ainexus.db`、服务器模式数据目录、Docker volume、授权服务器 SQLite 数据是否能无损升级。
- 旧配置字段、端点认证模式、转换器名称、Token Pool 数据结构是否仍兼容。
- 新增远程功能是否默认关闭，或至少不会在客户不知情时改变本地端点开关、顺序、凭证、模型或代理行为。
- 升级失败是否有回滚路径；服务器部署必须保留上一版 release 和数据库备份。

**分发包发布约束：严禁上传当前工作区源码。**
以后任何 GitHub Release / pre-release 只能基于 `392adb5e3278b1660bcbd1d58aa244f3cf4b3eab` 对应源码构建并上传安装包；不得把当前工作区的源码、配置、未提交改动或私有服务器信息打包上传。

## 存量客户兼容性规则

面向客户分发后的变更必须遵守：

- **默认无破坏**：默认配置、默认端口、默认授权服务器地址、默认代理行为不能随意改变。必须改变时，提供迁移逻辑和明确文档。
- **数据迁移向前兼容**：SQLite schema 只能做可重复、可幂等的迁移；不要删除旧字段或旧表。需要重命名/拆表时，先保留旧读路径。
- **API 向后兼容**：客户端或已发布版本可能调用的 HTTP 路径、JSON 字段、状态码语义不能直接删除。新增字段要可选，旧客户端忽略后仍能工作。
- **授权稳定**：不要改变 Ed25519 票据签名格式、授权服务器公钥处理、30 天离线宽限语义，除非同时保留旧票据验证。
- **远程管控安全**：服务器端查看 Token 消耗、远程管理端点开关/顺序等能力必须有鉴权、审计日志、限流和最小权限；默认不能暴露客户 API Key、refresh token、access token 明文。
- **可回滚**：服务器侧变更必须使用 release 目录部署，保留上一版二进制和数据库备份；本地数据迁移必须允许旧数据继续被新版本读取。
- **测试优先**：涉及授权、配置、Token Pool、远程管理、数据库迁移、客户升级路径时，必须添加回归测试。至少覆盖旧数据读取、新字段缺省、升级后行为不变。

## 未来服务器运营功能设计边界

计划中的服务器端运营能力应按以下边界设计：

- 客户端上报 Token 消耗时，只上报必要的统计聚合数据，例如设备 ID、版本、端点 ID、模型、请求量、Token 用量、错误分类和时间窗口；避免上传 prompt、response、API Key、Token 明文。
- 远程管理客户端端点开关/顺序时，必须保留本地兜底：服务器不可用时客户现有本地配置继续生效。
- 远程策略应有版本号、签名或校验字段、更新时间、来源和审计记录，避免乱序覆盖客户本地最新配置。
- 客户端应能区分“本地用户修改”和“服务器策略下发”，冲突时默认保护客户当前可用配置。
- 所有远程写操作要有后台历史记录，便于追踪是谁、何时、改了哪台设备或哪类策略。

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
