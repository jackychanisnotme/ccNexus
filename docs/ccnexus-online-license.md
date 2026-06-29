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
export CCNEXUS_LICENSE_BIND=0.0.0.0
export CCNEXUS_LICENSE_DATA_DIR=/var/www/ccnexus-license/shared
export CCNEXUS_LICENSE_DB_PATH=/var/www/ccnexus-license/shared/license.db
export CCNEXUS_LICENSE_KEY_PATH=/var/www/ccnexus-license/shared/private_key.txt
```

首次启动会自动生成 Ed25519 私钥，并在同目录写出 `public_key.txt`，同时在日志输出客户包需要嵌入的公钥。私钥只放服务器，不要提交到仓库。

## 共享服务器部署约定

在 `207.57.134.147` 上使用独立命名空间：

- 项目目录：`/var/www/ccnexus-license`
- PM2 进程：`ccnexus-license`
- 服务端口：`24220`
- HTTPS 入口：`https://license.wenche.xyz`
- IP 备用入口：`http://207.57.134.147:24220`

不要修改 `wenche-ai` 或 `flower-logistics` 的目录、PM2 进程、Nginx 配置。HTTPS 域名使用独立 Nginx 配置；IP + 端口保留为授权刷新和远程维护的备用链路。

## 客户 App

客户 App 使用在线激活：

```bash
CCNEXUS_LICENSE_SERVER_URL=https://license.wenche.xyz
CCNEXUS_LICENSE_PUBLIC_KEY=<server-public-key>
```

新版客户端默认内置双通道：优先使用 `https://license.wenche.xyz`，备用使用 `http://207.57.134.147:24220`。如需显式指定多个地址，可使用逗号分隔：

```bash
CCNEXUS_LICENSE_SERVER_URLS=https://license.wenche.xyz,http://207.57.134.147:24220
```

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
http://207.57.134.147:24220
```

## 授权规则

- 卡密由服务器生成，只返回明文一次，数据库只保存哈希。
- 每张卡密可配置允许设备数，默认 1 台。
- 同设备续期：未过期从当前到期时间继续累加，已过期从当前时间开始。
- App 激活后缓存服务器签名票据。
- 授权服务器不可用时，最近一次成功校验后的 30 天内可继续离线使用。
- 后台可禁用卡密或单个设备激活。
- 旧离线卡密不再兼容；已激活的旧本地授权只作为过渡缓存使用。
