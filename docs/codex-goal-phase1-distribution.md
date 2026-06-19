# AINexus6.1.4 第一阶段分发验证落地方案

> 用途：给 Codex 目标模式和实际执行使用。  
> 基线版本：AINexus v6.1.4。  
> 核心策略：优先建设独立站 + 多平台下载 + 真实付费验证；不优先上 App Store，不先做复杂 SaaS 或多租户后台。  
> 强制交付入口：macOS notarized 独立下载包、Windows 下载入口、Linux/Docker/服务器模式入口。  
> 付费验证对象：配置包、节点订阅、代部署、API 入口。

## 一句话目标

基于 AINexus6.1.4 已有桌面模式、服务器模式、Docker、WebUI、端点管理、协议转换、统计和备份能力，在 30 天内跑通“访问独立站 -> 下载 -> 看教程 -> 购买 -> 交付 -> 跑通一次请求 -> 获得支持”的最小商业闭环。

第一阶段不是建设完整商业平台，而是验证用户是否愿意为 AINexus 相关的交付物付费：

1. 为可直接使用的配置包付费。
2. 为持续更新的节点/端点配置源付费。
3. 为代部署和排障服务付费。
4. 为稳定可用的 API 入口付费。

## 执行原则

- 独立站是主阵地，负责下载、教程、付费、支持和渠道承接。
- macOS 必须提供经过 Developer ID 签名和 Apple notarization 的独立下载包。
- Windows 必须在第一阶段作为明确下载入口，不作为“后续再说”的附属平台。
- Linux、服务器模式和 Docker 作为代部署、API 入口和团队使用的基础。
- 先使用静态站、外部支付链接、人工/半自动交付，避免第一阶段建设复杂 SaaS。
- 付费页只承诺已经能交付的内容，不承诺无限量、永久可用或无法控制成本的能力。
- 不公开宣传 token 倒卖；Token Pool 只作为用户自带合法凭证、本地轮换和企业/个人自有资源管理能力。

## 现有能力复用映射

| 现有能力 | 当前位置 | 第一阶段用途 |
| --- | --- | --- |
| Wails 桌面模式 | `cmd/desktop/` | macOS、Windows、Linux 桌面下载包 |
| 服务器模式 | `cmd/server/` | VPS、NAS、Docker、团队 API 入口 |
| Docker 构建 | `cmd/server/Dockerfile`、`cmd/server/docker-compose.yml` | 代部署标准交付模板 |
| WebUI | `cmd/server/webui/`，运行路径 `/ui/` | 远程端点管理、统计查看、配置维护 |
| 端点管理 API | `/api/endpoints` | 配置包、节点源、代部署初始化 |
| 代理入口 | `/`、`/v1/models`、`/models`、`/api/tags`、`/health`、`/stats` | API 入口、客户端探测、健康检查 |
| 协议转换 | `internal/transformer/` | Codex CLI、Claude Code、OpenAI SDK、Gemini 等教程 |
| Token Pool | `internal/codexauth/`、凭证统计 | 用户自带凭证的轮换、刷新、额度观察 |
| 统计系统 | `internal/proxy/stats.go`、WebUI stats | 付费用户用量观察、售后判断、API 入口成本验证 |
| 备份能力 | WebDAV、本地、S3 兼容备份 | 配置迁移、代部署交付、付费配置包恢复 |
| 更新能力 | `internal/service/update.go`、GitHub Releases | 独立站下载版本、自动更新说明 |

## 第一阶段不投入的范围

- 不优先上 Mac App Store。
- 不做 App Store 审核适配版或沙盒裁剪版。
- 不做完整 SaaS 控制台。
- 不做复杂多租户权限、组织、账单和工单系统。
- 不做全自动节点订阅协议。
- 不做公开 API marketplace。
- 不承诺平台统一托管所有用户密钥。
- 不做大规模分销系统，渠道页只收集合作意向。

## 独立站页面结构

第一阶段独立站可以先做成静态站，使用 GitHub Releases、对象存储或 CDN 承接下载；支付先接外部 checkout、付款二维码或人工确认；交付通过邮件、私有链接、社群或预约表单完成。

### 1. 首页 `/`

目标：让 Codex CLI、Claude Code、OpenAI 兼容客户端、Cursor、Open WebUI 用户在 30 秒内理解 AINexus 的价值。

首屏内容：

- 标题：`AINexus：Codex、Claude Code 与 OpenAI 兼容工具的统一 API Provider`
- 副标题：`本地或服务器部署，一个入口管理多个 AI API 上游，自动故障切换、协议转换、Token Pool、统计和备份。`
- 主按钮：`下载 macOS`、`下载 Windows`
- 次按钮：`查看 Codex CLI 教程`、`预约代部署`

核心模块：

- 统一入口：所有客户端指向一个 base URL。
- 多端点故障切换：减少单个上游不可用对工作流的影响。
- 协议转换：Codex/OpenAI Responses、Claude、OpenAI Chat、Gemini 等互转。
- 本地和服务器双模式：普通用户用桌面版，团队/NAS/VPS 用服务器模式。
- 四类付费交付：配置包、节点订阅、代部署、API 入口。

首页底部必须露出下载、教程、付费和支持入口，不能只做品牌介绍。

### 2. 下载页 `/download`

目标：让用户按系统直接拿到可运行版本。

下载页结构：

| 平台 | 第一阶段交付 | 页面文案重点 |
| --- | --- | --- |
| macOS Apple Silicon | `AINexus-v6.1.4-darwin-arm64.dmg` 或 `.zip` | 已签名、公证、首次打开说明 |
| macOS Intel | `AINexus-v6.1.4-darwin-amd64.dmg` 或 `.zip` | 已签名、公证、首次打开说明 |
| Windows x64 | `AINexus-v6.1.4-windows-amd64.exe` 或 `.zip` | 安装/便携版、防火墙、SmartScreen 说明 |
| Windows ARM64 | `AINexus-v6.1.4-windows-arm64.zip` | 作为次级入口，说明兼容状态 |
| Linux desktop | `AINexus-v6.1.4-linux-amd64.tar.gz` | 桌面依赖说明 |
| Docker/server | Docker image、`docker-compose.yml` | VPS/NAS/团队部署入口 |

下载页必须包含：

- 文件名、版本、发布时间。
- SHA256 校验值。
- macOS 公证状态和首次打开说明。
- Windows 防火墙和端口占用说明。
- 默认监听地址：本机模式 `127.0.0.1:3000`，服务器模式按部署配置。
- 更新方式：GitHub Releases/独立站版本页 + 应用内检查更新。
- 回滚方式：保留上一稳定版本下载。

### 3. 教程页 `/docs`

目标：降低第一次跑通请求的成本。

优先教程：

1. `/docs/codex-cli`：Codex CLI 使用 AINexus Responses API。
2. `/docs/claude-code`：Claude Code 使用 AINexus Claude/Anthropic 兼容入口。
3. `/docs/openai-sdk`：OpenAI SDK 指向 AINexus `base_url`。
4. `/docs/docker`：服务器模式 + Docker + Basic Auth + WebUI。
5. `/docs/endpoints`：端点、认证模式、transformer、模型、reasoning、force stream 的选择。
6. `/docs/backup`：WebDAV、本地、S3 备份和迁移。

每篇教程统一结构：

- 适合谁。
- 需要准备什么。
- AINexus 端点配置。
- 客户端配置片段。
- 测试命令。
- 常见错误和排查。
- 下一步付费入口。

### 4. 付费页 `/pricing`

目标：展示四类付费产品，并用真实订单验证需求。

页面结构：

- 顶部说明：第一阶段为早期验证，部分交付采用人工或半自动方式。
- 四个产品卡片：配置包、节点订阅、代部署、API 入口。
- 每个卡片包含：适合谁、交付什么、价格区间、交付时间、限制说明、购买按钮。
- 付款后表单：邮箱/微信/Telegram、使用平台、目标客户端、是否需要远程协助。
- 退款说明：无法跑通且排查后确认不可用时退款；因用户上游账号/额度/网络限制导致的问题转支持处理。

### 5. 支持页 `/support`

目标：把售后成本显性化，避免每个问题都进入私聊。

支持页包含：

- FAQ：端口占用、401/403、模型不存在、SSE 中断、Windows 防火墙、macOS 打不开、Docker 数据目录。
- 自助诊断：访问 `/health`、查看 `/stats`、打开 WebUI `/ui/`、测试端点。
- 远程协助入口：付费代部署或排障预约。
- 工单入口：收集系统、版本、日志、客户端、上游类型。
- 社群入口：微信群/Telegram/Discord/飞书择一或多选。

### 6. 渠道页 `/partners`

目标：给教程作者、KOL、群主和部署服务商留合作入口，不在第一阶段建设分销系统。

内容：

- 合作对象：教程作者、开发者社群、AI 工具站、代部署服务商。
- 合作形式：下载引流、配置包分成、代部署转介绍、团队客户线索。
- 提交表单：渠道名称、受众、预计触达、联系方式。

## 多平台下载交付流程

### 发布前版本冻结

1. 确认 `cmd/desktop/wails.json` 中 `productVersion` 为 `6.1.4`。
2. 确认 `cmd/desktop/CHANGELOG.json` 包含 `v6.1.4` 变更说明。
3. 从干净 tag 或 release branch 构建，避免临时文件进入分发包。
4. 对齐构建环境：`go.mod` 使用 Go 1.24.0/toolchain 1.24.3，CI 和本地发布机应使用 Go 1.24.x。
5. 构建前运行：

```bash
go test ./... -count=1
cd cmd/desktop/frontend && npm install && npm run build
```

### macOS 交付流程

第一阶段 macOS 必须提供 notarized 独立下载包。优先交付 `.dmg`；如果短期内只交付 `.zip`，也必须保证其中的 `.app` 已完成签名和公证。

构建：

```bash
cd cmd/desktop
wails build -platform darwin/arm64
wails build -platform darwin/amd64
```

签名和公证要求：

- 使用 Developer ID Application 证书签名。
- 启用 hardened runtime。
- 使用 Apple notary service 提交公证。
- 公证通过后 staple ticket。
- 用 Gatekeeper 验证下载包。

建议人工发布流程：

```bash
codesign --deep --force --options runtime --timestamp \
  --sign "Developer ID Application: <Team Name> (<TEAMID>)" \
  cmd/desktop/build/bin/AINexus.app

# DMG 路线：创建并签名 DMG 后提交公证
xcrun notarytool submit AINexus-v6.1.4-darwin-arm64.dmg \
  --keychain-profile "AINEXUS_NOTARY_PROFILE" --wait
xcrun stapler staple AINexus-v6.1.4-darwin-arm64.dmg
spctl --assess --type open --verbose AINexus-v6.1.4-darwin-arm64.dmg

# ZIP 路线：提交用于公证的 zip，通过后 staple .app，再生成最终 zip
xcrun notarytool submit AINexus-v6.1.4-darwin-arm64.zip \
  --keychain-profile "AINEXUS_NOTARY_PROFILE" --wait
xcrun stapler staple cmd/desktop/build/bin/AINexus.app
spctl --assess --type execute --verbose cmd/desktop/build/bin/AINexus.app
ditto -c -k --keepParent cmd/desktop/build/bin/AINexus.app \
  AINexus-v6.1.4-darwin-arm64.zip
```

验收：

- 全新 macOS 用户下载后无需关闭 Gatekeeper 即可打开。
- 首次打开说明清晰，包括右键打开兜底说明。
- `/health` 返回正常。
- Codex CLI 教程中的最小请求能通过。

### Windows 交付流程

Windows 是第一阶段明确交付入口，至少提供 x64 版本。短期可以先提供 `.zip` 便携包；正式对外付费转化页建议补充 `.exe` 安装器和代码签名，降低 SmartScreen 阻断。

构建：

```powershell
cd cmd/desktop
wails build -platform windows/amd64
wails build -platform windows/arm64
```

打包：

- 便携包：`AINexus-v6.1.4-windows-amd64.zip`，内含 `AINexus.exe`、版本说明、首次运行说明。
- 安装器：可用 Inno Setup 或 WiX 打包，放入开始菜单、卸载入口和防火墙说明。
- 签名：使用 SignTool 对 `.exe` 和安装器签名，并加时间戳。

验收：

- Windows 10/11 x64 能启动桌面应用。
- 首次启动遇到防火墙提示时，教程说明用户应允许本机访问。
- 默认端口占用时有排查说明。
- 解压路径包含中文或空格时能正常启动。
- Codex CLI 或 OpenAI SDK 指向本机 AINexus 能跑通一次请求。

### Linux、Docker 和服务器模式交付流程

Linux 桌面版作为补充入口；服务器模式和 Docker 是代部署、API 入口和团队试用的核心。

构建服务器：

```bash
cd cmd/server
go build -ldflags="-s -w" -o ainexus-server .
```

Docker 交付：

```bash
docker build -f cmd/server/Dockerfile -t ainexus:6.1.4 .
cd cmd/server
docker compose up -d
```

服务器交付标准：

- 使用 `AINEXUS_DATA_DIR=/data` 持久化数据。
- 使用 `AINEXUS_DB_PATH=/data/ainexus.db` 固定数据库。
- 生产部署启用 `AINEXUS_BASIC_AUTH_ENABLED=true`。
- 首次随机密码必须交付给用户并提示保存。
- WebUI 入口为 `/ui/`，健康检查为 `/health`。
- 反向代理和 HTTPS 由代部署服务交付，不作为软件本体第一阶段功能。

### 下载站发布和回滚

发布顺序：

1. 构建并验收所有平台包。
2. 生成 SHA256。
3. 上传 GitHub Releases。
4. 同步到独立站下载页或 CDN。
5. 更新 `/download` 的版本号、校验值、发布时间和已知问题。
6. 发布教程页对应版本截图。
7. 保留上一稳定版本下载。

回滚触发：

- macOS Gatekeeper 验证失败。
- Windows 大量无法启动。
- Docker 健康检查失败。
- 关键客户端教程无法跑通。
- 付费用户交付失败率超过 20%。

## 四类付费产品说明

### 1. 配置包

定位：面向“不想研究端点、transformer、模型和客户端配置”的用户。

第一阶段交付形式：

- AINexus 端点配置说明或可导入/可复制 JSON 字段。
- Codex CLI `config.toml` 示例。
- Claude Code `settings.json` 示例。
- OpenAI SDK `base_url` 示例。
- 常见模型与 transformer 映射表。
- 推荐 fallback 顺序、reasoning、force stream 配置。
- 5 个以内常见错误排查。

套餐建议：

| 套餐 | 价格区间 | 交付 |
| --- | --- | --- |
| 入门配置包 | 29-99 元一次性 | Codex CLI + Claude Code 基础配置 |
| 高级配置包 | 99-299 元一次性 | 多客户端、多上游、fallback、Docker 示例 |
| 更新包 | 29-99 元/月或季度 | 端点模板、教程和模型映射持续更新 |

验证指标：

- 下载页到配置包购买转化率。
- 购买后 24 小时内首次跑通率。
- 7 天退款率。
- 用户是否主动询问更多上游、更多客户端或自动更新。

### 2. 节点订阅

定位：面向需要“持续更新端点配置源”的用户，而不是一次性模板。

第一阶段交付形式：

- 私有订阅链接或私有配置文件。
- 端点名称、API URL、transformer、模型、reasoning、fallback 顺序。
- 节点状态说明和更新时间。
- 每周固定更新记录。
- 暂不承诺全自动拉取；先通过邮件、私有链接、社群公告或人工协助更新。

套餐建议：

| 套餐 | 价格区间 | 交付 |
| --- | --- | --- |
| 基础节点源 | 49-99 元/月 | 少量稳定端点配置和更新说明 |
| 高级节点源 | 99-299 元/月 | 更多模型、更细 fallback、更快更新 |
| 团队节点源 | 399 元/月起 | 团队配置模板、私有更新、部署建议 |

验证指标：

- 月订阅转化率。
- 7 天持续使用率。
- 续费意愿。
- 节点不可用投诉率。
- 用户是否要求一键订阅和自动同步。

边界：

- 第一阶段不做完整订阅协议和自动同步客户端。
- 节点源不包含非法账号池或不可解释来源。
- 如涉及第三方上游，必须明确用户自带 key、合法额度或授权来源。

### 3. 代部署

定位：面向 VPS、NAS、软路由、团队服务器和不想自己排障的用户。

第一阶段交付内容：

- 服务器模式或 Docker 部署。
- 数据目录和 SQLite 数据库持久化。
- Basic Auth、随机密码和密码保存说明。
- 反向代理和 HTTPS。
- WebUI 远程访问。
- 至少一个端点配置和测试。
- Codex CLI/Claude Code/OpenAI SDK 接入。
- WebDAV、本地或 S3 兼容备份方案。
- 30 分钟内基础使用培训或录屏。

套餐建议：

| 套餐 | 价格区间 | 交付 |
| --- | --- | --- |
| 标准部署 | 499-999 元一次性 | 单机 Docker/服务器模式 + 一个客户端跑通 |
| 高级部署 | 999-1999 元一次性 | HTTPS、备份、多端点、多个客户端 |
| 月维护 | 199-499 元/月 | 升级、排障、配置调整、备份检查 |

验证指标：

- 咨询到付款转化率。
- 单次交付耗时。
- 售后工单数。
- 7 天内是否稳定使用。
- 是否愿意续费维护。

### 4. API 入口

定位：面向“不想自己准备上游，只想拿到一个可用 base_url 和 api_key”的用户。

第一阶段实现方式：

- 只做小范围白名单，不公开大规模售卖。
- 优先使用单租户或小组隔离部署，避免多租户复杂度。
- 使用 AINexus 服务器模式承接路由、协议转换和统计。
- 在反向代理或网关层发放用户访问凭证。
- 使用 `/stats`、WebUI stats 和上游账单交叉核对成本。

交付内容：

- 用户专属 `base_url`。
- 用户访问凭证。
- 可用模型列表。
- Codex CLI、Claude Code、OpenAI SDK 接入示例。
- 套餐额度、并发、速率和禁止用途说明。
- 超额、滥用、异常请求的处理规则。

套餐建议：

| 套餐 | 价格区间 | 交付 |
| --- | --- | --- |
| 封闭测试包 | 99-299 元/月 | 小额度、低并发、人工支持 |
| Pro API 入口 | 299-999 元/月 | 更高额度、更高可用性、统计报表 |
| 团队入口 | 999 元/月起 | 团队 base_url、部署隔离、服务支持 |

必须补充的运营控制：

- 限流和并发限制。
- 用量配额。
- 异常请求封禁。
- 上游成本记录。
- 用户实名或至少稳定联系方式。

验证指标：

- 首次付费转化率。
- 每用户月收入。
- 单用户上游成本。
- 毛利率。
- 异常请求比例。
- 支持工单/收入比。

## 30 天验证计划

### 时间表

| 时间 | 目标 | 交付物 |
| --- | --- | --- |
| Day 1-3 | 分发资产冻结 | v6.1.4 下载文案、教程大纲、价格页草稿 |
| Day 4-7 | 多平台下载上线 | macOS notarized 包、Windows 包、Docker/服务器部署入口 |
| Day 8-10 | 独立站最小闭环 | 首页、下载页、教程页、付费页、支持页 |
| Day 11-15 | 付费产品上架 | 配置包、节点订阅、代部署、API 入口白名单 |
| Day 16-23 | 收集真实订单 | 每日记录访问、咨询、购买、跑通和退款 |
| Day 24-30 | 复盘和决策 | 指标复盘、下一阶段优先级、是否投入自动化 |

### 交付验收指标

| 类别 | 30 天最低标准 | 理想标准 |
| --- | --- | --- |
| 独立站 | 5 个核心页面上线 | 增加英文页和案例页 |
| macOS | notarized 包可下载并通过 Gatekeeper 验证 | Intel/Apple Silicon 都有 DMG |
| Windows | x64 包可下载并有启动教程 | 安装器 + 代码签名 |
| Docker | `docker compose up -d` 可运行 | 提供反向代理和 HTTPS 模板 |
| 教程 | Codex CLI、Claude Code 至少 2 篇 | 覆盖 OpenAI SDK、Docker、备份 |
| 支持 | FAQ + 一个联系入口 | 工单表单 + 社群 + 远程预约 |
| 付费 | 至少 3 单真实付款 | 20 单以上真实付款 |
| 激活 | 至少 3 个用户跑通一次请求 | 10 个用户 7 天后仍在使用 |

### 商业验证指标

| 指标 | 记录方式 | 继续投入阈值 |
| --- | --- | --- |
| 下载页访问量 | 独立站统计 | 30 天 300+ UV 或来自精准社群 100+ UV |
| 下载转化率 | 下载点击/下载页 UV | 10% 以上 |
| 教程激活率 | 跑通反馈/教程访问 | 20% 以上 |
| 付费转化率 | 付款人数/有效咨询人数 | 10% 以上 |
| 配置包订单 | 支付记录 | 5 单以上 |
| 节点订阅留存 | 7 天后仍使用或询问续费 | 3 人以上 |
| 代部署订单 | 支付记录 | 1 单以上 |
| API 入口订单 | 白名单付费用户 | 1-3 个高质量用户 |
| 退款率 | 退款/订单 | 15% 以下 |
| 售后成本 | 每单支持时长 | 配置包小于 30 分钟，代部署小于 3 小时 |
| 毛利 | 收入 - 上游和交付成本 | API 入口必须为正毛利 |

### 每日记录模板

```text
日期：
站点 UV：
下载点击：macOS / Windows / Docker
教程访问：Codex CLI / Claude Code / Docker
咨询数：
订单：配置包 / 节点订阅 / 代部署 / API 入口
收入：
退款：
跑通请求人数：
主要问题：
当天动作：
```

### Go / No-Go 决策

继续投入自动化的条件：

- 30 天内至少 20 单真实付款，或至少 3 个高意向团队/代部署客户。
- 至少一个产品显示重复购买或续费信号。
- 售后问题集中在可通过教程、配置包或自动化解决的环节。
- API 入口毛利为正，且异常请求可控。

暂停或调整的条件：

- 只有下载，没有咨询和付款。
- 退款率超过 25%。
- 用户主要需求不是 AINexus 能力，而是不可控的账号资源。
- API 入口成本无法核算或滥用明显。
- 代部署交付时间过长，无法形成标准流程。

## 后续迭代建议

第一阶段验证通过后，按真实收入信号决定下一步。

### 如果配置包卖得好

- 增加端点配置导入/导出格式。
- 做一键应用配置包。
- 在 WebUI 和桌面端增加配置模板入口。
- 给 Codex CLI、Claude Code、OpenAI SDK 做更完整向导。

### 如果节点订阅卖得好

- 设计签名订阅源格式。
- 增加远程订阅拉取、校验和差异更新。
- 增加节点状态页。
- 增加订阅到期提醒和更新日志。

### 如果代部署卖得好

- 固化 Docker Compose、Nginx/Caddy、HTTPS、备份模板。
- 增加部署检查脚本。
- 增加服务器模式健康诊断页。
- 建立远程维护套餐和升级流程。

### 如果 API 入口卖得好

- 优先建设配额、限流、成本统计和封禁能力。
- 再考虑用户控制台、API key 管理和账单。
- 保持单租户/小组隔离，确认毛利后再做多租户。
- 增加审计日志和异常请求告警。

### App Store 的后置策略

App Store 不作为第一阶段目标。若独立站验证通过，可以考虑做一个 App Store Lite 版本，只承担品牌信任和搜索获客：

- 不承载节点订阅和 API 入口售卖。
- 不暴露敏感代理或账号池宣传。
- 功能裁剪到本地配置、教程和导流。
- 真实付费仍回到独立站、配置服务和部署服务。

## 第一阶段最终交付清单

- [ ] 独立站：首页、下载页、教程页、付费页、支持页、渠道页。
- [ ] macOS notarized 下载包：Apple Silicon + Intel。
- [ ] Windows 下载包：至少 x64，明确首屏入口。
- [ ] Linux/Docker/服务器模式下载和部署入口。
- [ ] SHA256 校验和上一稳定版本回滚入口。
- [ ] Codex CLI 教程。
- [ ] Claude Code 教程。
- [ ] Docker 服务器模式教程。
- [ ] 配置包 SKU 和首版交付文件。
- [ ] 节点订阅首版私有交付方式。
- [ ] 代部署服务说明、价格、边界和交付清单。
- [ ] API 入口白名单套餐、限制和风控说明。
- [ ] 支付和交付流程。
- [ ] FAQ、支持入口和每日数据记录表。
- [ ] 30 天复盘标准和下一阶段决策。

## 参考

- Apple 官方文档：[Notarizing macOS software before distribution](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)。用于确认独立分发 macOS 软件需要 Developer ID、notary service、notarytool、stapler 等流程。
- Microsoft 官方文档：[SignTool](https://learn.microsoft.com/en-us/windows/win32/seccrypto/signtool)。用于确认 Windows 可通过 SignTool 对文件签名、验证签名并加时间戳。
