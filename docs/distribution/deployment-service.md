# AINexus 代部署服务交付模板

## 适合谁

- 有 VPS、NAS、软路由或团队服务器，但不想自行部署和排障的用户。
- 需要服务器模式、Docker、HTTPS、Basic Auth、WebUI 和备份策略的用户。
- 希望 Codex CLI、Claude Code、OpenAI SDK 共享一个远程 AINexus base URL 的团队。

## 交付内容

- 服务器模式或 Docker Compose 部署。
- `AINEXUS_DATA_DIR`、`AINEXUS_DB_PATH` 和数据目录持久化。
- Basic Auth 开启、用户名和随机密码交付。
- 反向代理和 HTTPS 配置，优先 Caddy 或 Nginx。
- WebUI `/ui/` 远程访问和 `/health` 健康检查。
- 至少一个端点配置、测试和 fallback 顺序。
- Codex CLI、Claude Code、OpenAI SDK 接入示例。
- WebDAV、本地或 S3 兼容备份方案。
- 30 分钟基础使用培训或录屏。

## 标准 Docker Compose

```yaml
services:
  ainexus:
    image: ainexus:6.1.4
    container_name: ainexus
    restart: unless-stopped
    ports:
      - "3021:3000"
    volumes:
      - ./ainexus:/data
    environment:
      - AINEXUS_PORT=3000
      - AINEXUS_DATA_DIR=/data
      - AINEXUS_DB_PATH=/data/ainexus.db
      - AINEXUS_BASIC_AUTH_ENABLED=true
      - AINEXUS_BASIC_AUTH_USERNAME=admin
      - TZ=Asia/Shanghai
```

## 价格

- 标准部署：499-999 元一次性，单机 Docker/服务器模式和一个客户端跑通。
- 高级部署：999-1999 元一次性，包含 HTTPS、备份、多端点、多个客户端。
- 月维护：199-499 元/月，包含升级、排障、配置调整和备份检查。

## 交付时间

- 标准部署：用户提供服务器权限后 1 个工作日内。
- 高级部署：用户提供域名、服务器和上游信息后 2 个工作日内。
- 月维护：按月响应，紧急程度和响应时间在付款前确认。

## 限制

- 不包含云服务器、域名、SSL 商业证书和第三方上游费用。
- 不承诺无限并发、无限额度或绕过上游限制。
- 不处理来源不明的账号池或违反上游条款的用途。
- 用户需要保管服务器登录方式、Basic Auth 密码和备份凭据。

## 验收方式

- `GET /health` 返回正常。
- WebUI `/ui/` 可登录访问。
- 至少一个端点测试通过。
- 至少一个客户端通过远程 base URL 跑通请求。
- 备份策略完成一次手动备份或明确记录下一步动作。
