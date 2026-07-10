# AINexus 在线卡密授权维护指南

这份文档用于在 AINexus 的 `master` 分支上持续维护在线卡密激活功能。

## 功能范围

- `cmd/license-server` 提供授权服务器和管理后台
- `internal/onlinelicense` 提供卡密、激活、票据、离线宽限逻辑
- `cmd/desktop` 在启动时检查授权并弹出激活窗
- `cmd/server` 在无授权时仅启动授权相关接口
- `cmd/server/webui` 提供网页版授权状态与激活入口

不要再回退到本地注册机、离线卡密生成器或旧版 `internal/license` 方案。

## 关键文件

- [cmd/license-server/main.go](/Users/pc/Documents/New project 2/cmd/license-server/main.go)
- [internal/onlinelicense/service.go](/Users/pc/Documents/New project 2/internal/onlinelicense/service.go)
- [internal/onlinelicense/client_license.go](/Users/pc/Documents/New project 2/internal/onlinelicense/client_license.go)
- [cmd/server/main.go](/Users/pc/Documents/New project 2/cmd/server/main.go)
- [cmd/desktop/app.go](/Users/pc/Documents/New project 2/cmd/desktop/app.go)
- [cmd/desktop/frontend/src/modules/settings.js](/Users/pc/Documents/New project 2/cmd/desktop/frontend/src/modules/settings.js)
- [cmd/server/webui/ui/js/components/settings.js](/Users/pc/Documents/New project 2/cmd/server/webui/ui/js/components/settings.js)

## 本地构建

授权服务器：

```bash
go build ./cmd/license-server
```

桌面端客户包：

```bash
cd cmd/desktop/frontend
npm install
npm run build
CCNEXUS_LICENSE_PUBLIC_KEY=<server-public-key> go build ./cmd/desktop
```

服务器端：

```bash
go build ./cmd/server
```

## 服务器部署

当前共享服务器约定：

- 项目目录：`/var/www/ccnexus-license`
- PM2 进程：`ccnexus-license`
- 端口：`24220`
- HTTPS 入口：`https://license.wenche.xyz`
- 绑定：`127.0.0.1:24220`
- 公网入口：仅允许独立 Nginx HTTPS 入口

必需环境变量：

```bash
CCNEXUS_LICENSE_ADMIN_USERNAME=admin
CCNEXUS_LICENSE_ADMIN_PASSWORD=<strong-password>
```

常用环境变量：

```bash
CCNEXUS_LICENSE_PORT=24220
CCNEXUS_LICENSE_BIND=127.0.0.1
CCNEXUS_LICENSE_DATA_DIR=/var/www/ccnexus-license/shared
CCNEXUS_LICENSE_DB_PATH=/var/www/ccnexus-license/shared/license.db
CCNEXUS_LICENSE_KEY_PATH=/var/www/ccnexus-license/shared/private_key.txt
CCNEXUS_LICENSE_REMOTE_SECRET_REVEAL_ENABLED=false
CCNEXUS_LICENSE_SERVER_URL=https://license.wenche.xyz
CCNEXUS_LICENSE_SERVER_URLS=https://license.wenche.xyz
```

客户端默认只访问 HTTPS 域名。`http://127.0.0.1` 和 `http://localhost` 仅用于本机开发；其他明文 HTTP 地址只有在显式设置 `CCNEXUS_LICENSE_ALLOW_INSECURE_HTTP=true` 时才会接受，客户环境禁止启用。

远程密钥回传必须同时满足：

```bash
CCNEXUS_LICENSE_REMOTE_SECRET_REVEAL_ENABLED=true
AINEXUS_ALLOW_REMOTE_SECRET_REVEAL=true
```

默认保持两项关闭。远程命令必须带 Ed25519 签名，客户端必须验证签名、过期时间和 nonce 防重放；服务端会在投递前为升级前遗留的完整 queued 命令补签。

## 维护流程

1. 先确认 `ccnexus-license` 进程在线。
2. 检查 `24220` 是否只监听 `127.0.0.1`。
3. 生成卡密时只保存哈希，不要把明文写回日志、数据库或配置。
4. App 侧必须使用在线激活，不要把旧离线卡密流程加回来。
5. 修改桌面端时，确保 `CCNEXUS_LICENSE_PUBLIC_KEY` 仍正确嵌入。
6. 修改授权逻辑后，至少跑一次：

```bash
go test ./... -count=1
go vet ./...
```

部署时必须新建 release 目录，停掉 `ccnexus-license` 后备份 `license.db`、`license.db-wal`、`license.db-shm`，保留上一版 release 和旧 Nginx 配置。只重启 `ccnexus-license`，不要操作共享服务器上的其他 PM2 进程。

客户端签名校验和 HTTPS-only 逻辑只有在客户安装新版本后才生效。服务端部署完成后仍需安排客户端升级，不能把服务端加固视为已替换存量客户端。

## 常见问题

### App 一打开就要激活

说明当前设备还没有有效票据。请在管理后台生成卡密，然后在 App 中粘贴激活。

### 浏览器打开 `/admin/` 空响应

先检查授权服务是否在跑：

```bash
pm2 show ccnexus-license
ss -ltnp | grep 24220
```

### 激活失败

优先检查：

- 卡密是否输入完整
- 服务器公钥是否和客户包一致
- 授权服务是否能访问数据库
- 该卡是否已禁用或设备数是否超限

### 客户端离线还能用多久

最近一次在线校验成功后，默认可离线 30 天。超过后必须重新联网校验。

## 回到 `master` 后的注意事项

- 合并时优先保留 `internal/onlinelicense` 的独立边界。
- 不要把授权逻辑散落到代理、统计、更新器里。
- 不要恢复 `cmd/licensegen-*`、`internal/license*` 或本地注册机脚本。
- 改动 WebUI 或桌面端时，先确认文案、按钮和激活接口仍能对上。
- 如果要更换域名或 HTTPS，只改授权服务器地址和前端配置，不改授权数据模型。
