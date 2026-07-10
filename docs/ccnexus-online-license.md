# AINexus 在线卡密授权

## 授权服务器

独立授权服务入口：

```bash
go run ./cmd/license-server
```

默认配置：

- 端口：`24220`
- 绑定地址：`0.0.0.0`
- 管理后台：`/admin/`
- 客户激活接口：`/api/license/activate`
- 客户刷新接口：`/api/license/refresh`

必填环境变量：

```bash
export CCNEXUS_LICENSE_ADMIN_USERNAME=admin
export CCNEXUS_LICENSE_ADMIN_PASSWORD='<strong-password>'
```

可选环境变量：

```bash
export CCNEXUS_LICENSE_PORT=24220
export CCNEXUS_LICENSE_BIND=127.0.0.1
export CCNEXUS_LICENSE_DATA_DIR=/var/www/ccnexus-license/shared
export CCNEXUS_LICENSE_DB_PATH=/var/www/ccnexus-license/shared/license.db
export CCNEXUS_LICENSE_KEY_PATH=/var/www/ccnexus-license/shared/private_key.txt
export CCNEXUS_LICENSE_REMOTE_SECRET_REVEAL_ENABLED=false
```

首次启动会自动生成 Ed25519 私钥，并在同目录写出 `public_key.txt`，同时在日志输出客户包需要嵌入的公钥。私钥只放服务器，不要提交到仓库。

## 共享服务器部署约定

在 `207.57.134.147` 上使用独立命名空间：

- 项目目录：`/var/www/ccnexus-license`
- PM2 进程：`ccnexus-license`
- 服务端口：`24220`
- HTTPS 入口：`https://license.wenche.xyz`
- 服务绑定：`127.0.0.1:24220`

不要修改 `wenche-ai` 或 `flower-logistics` 的目录、PM2 进程、Nginx 配置。授权服务只允许通过独立 Nginx HTTPS 入口访问，禁止恢复公网 IP + 明文 HTTP 备用链路。

## 客户 App

客户 App 使用在线激活：

```bash
CCNEXUS_LICENSE_SERVER_URL=https://license.wenche.xyz
CCNEXUS_LICENSE_PUBLIC_KEY=<server-public-key>
```

新版客户端默认只使用 HTTPS 域名。如需显式指定多个地址，所有公网地址都必须使用 HTTPS：

```bash
CCNEXUS_LICENSE_SERVER_URLS=https://license.wenche.xyz
```

仅本机开发时允许 `http://127.0.0.1` 或 `http://localhost`。其他明文 HTTP 地址默认被拒绝；临时调试必须显式设置 `CCNEXUS_LICENSE_ALLOW_INSECURE_HTTP=true`，不得用于客户环境。

桌面 Pro 构建脚本会把公钥嵌入：

```text
github.com/lich0821/ccNexus/internal/onlinelicense.AppPublicKey
```

构建脚本优先读取：

```bash
CCNEXUS_LICENSE_PUBLIC_KEY
CCNEXUS_LICENSE_PUBLIC_KEY_FILE
~/.ccnexus-license/public_key.txt
```

如果未设置 `CCNEXUS_LICENSE_SERVER_URL` 或 `CCNEXUS_LICENSE_SERVER_URLS`，默认激活服务地址顺序为：

```text
https://license.wenche.xyz
```

## 授权规则

- 卡密由服务器生成，只返回明文一次，数据库只保存哈希。
- 每张卡密可配置允许设备数，默认 1 台。
- 同设备续期：未过期从当前到期时间继续累加，已过期从当前时间开始。
- App 激活后缓存服务器签名票据。
- 远程命令由同一 Ed25519 授权私钥签名，客户端校验设备、nonce、命令类型、密文、创建时间和过期时间，并持久化 nonce 防重放。
- `secret.reveal` 服务端和客户端默认关闭；只有同时设置 `CCNEXUS_LICENSE_REMOTE_SECRET_REVEAL_ENABLED=true` 与 `AINEXUS_ALLOW_REMOTE_SECRET_REVEAL=true` 才能执行。
- 授权服务器不可用时，最近一次成功校验后的 30 天内可继续离线使用。
- 后台可禁用卡密或单个设备激活。
- 旧离线卡密不再兼容；已激活的旧本地授权只作为过渡缓存使用。
