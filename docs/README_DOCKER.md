# Docker 部署指南

服务器镜像包含无头 API Provider 和嵌入式 Web 管理界面，不依赖桌面 GUI。

## 快速启动

首次启动前，确认 `cmd/server/docker-compose.yml` 的 `environment` 包含：

```yaml
- AINEXUS_LISTEN_MODE=lan
```

服务器默认只监听容器内的 `127.0.0.1`；设置为 `lan` 后，Docker 发布的宿主机端口才能连接到容器服务。

然后在仓库根目录执行：

```bash
cd cmd/server
docker compose up -d --build
docker compose logs -f ainexus
```

默认地址：

- API Provider：`http://127.0.0.1:3021`
- Web 管理界面：`http://127.0.0.1:3021/ui/`
- `/admin` 会重定向到 `/ui/`
- 健康检查：`http://127.0.0.1:3021/health`

Compose 将宿主机 `3021` 映射到容器 `3000`。如端口被占用，修改 `cmd/server/docker-compose.yml`：

```yaml
ports:
  - "8080:3000"
```

## 首次登录

服务器默认启用 Basic Auth：

- 用户名：`admin`
- 密码：首次启动时随机生成

查看密码：

```bash
docker compose logs ainexus
```

密码只会在生成时显示。生产部署建议通过环境变量显式设置：

```yaml
environment:
  - AINEXUS_BASIC_AUTH_ENABLED=true
  - AINEXUS_BASIC_AUTH_USERNAME=admin
  - AINEXUS_BASIC_AUTH_PASSWORD=replace-with-a-strong-password
```

## 数据持久化

默认 Compose 配置：

```yaml
volumes:
  - ./ainexus/:/data
```

数据库保存在宿主机 `cmd/server/ainexus/ainexus.db`。升级或重建容器不会删除该目录。

备份示例：

```bash
cp ainexus/ainexus.db "ainexus/ainexus.db.bak-$(date +%Y%m%d%H%M%S)"
```

## 环境变量

| 环境变量 | 说明 | 容器默认值 |
|----------|------|------------|
| `AINEXUS_PORT` | 容器内监听端口 | `3000` |
| `AINEXUS_LISTEN_MODE` | `local` 或 `lan` | 镜像默认 `local`；端口映射需使用 `lan` |
| `AINEXUS_LOG_LEVEL` | `0` 调试、`1` 信息、`2` 警告、`3` 错误 | `1` |
| `AINEXUS_DATA_DIR` | 数据目录 | `/data` |
| `AINEXUS_DB_PATH` | SQLite 数据库路径 | `/data/ainexus.db` |
| `AINEXUS_BASIC_AUTH_ENABLED` | 启用 Web UI/管理 API 登录 | `true` |
| `AINEXUS_BASIC_AUTH_USERNAME` | 登录用户名 | `admin` |
| `AINEXUS_BASIC_AUTH_PASSWORD` | 登录密码 | 首次启动随机生成 |

使用 Docker 发布端口时必须设置 `AINEXUS_LISTEN_MODE=lan`。不要在缺少 Basic Auth、强密码和网络访问控制时把该端口暴露到公网。

## 常用命令

```bash
# 查看状态
docker compose ps

# 跟踪日志
docker compose logs -f ainexus

# 重启
docker compose restart ainexus

# 重新构建并启动
docker compose up -d --build

# 停止服务（保留数据）
docker compose down
```

## Web 管理界面

Web UI 与服务器二进制一起构建，源代码位于：

```text
cmd/server/webui/
├── api/                # 管理 API
├── ui/                 # 嵌入式 HTML/CSS/JavaScript
└── webui.go            # 路由注册
```

管理接口统一使用 `/api/` 前缀，并与 Web UI 一样受 Basic Auth 保护。代理接口由服务器根路由提供。

## 安全建议

- 不要在公网部署时关闭 Basic Auth
- 使用强密码，不要把真实密码提交到仓库
- 通过防火墙限制来源 IP
- 公网访问时使用 Nginx、Caddy 等反向代理提供 HTTPS
- 定期备份 `/data/ainexus.db`
- 不要将健康检查和代理端点直接暴露给不可信网络

## 故障排查

### UI 无法访问

```bash
docker compose ps
docker compose logs ainexus
curl -i http://127.0.0.1:3021/health
```

确认访问路径包含结尾斜杠：`/ui/`。

### 忘记 Basic Auth 密码

首次生成的密码可在历史容器日志中查找。若无法恢复，可停止服务，在 Compose 中设置新的 `AINEXUS_BASIC_AUTH_PASSWORD` 后重新启动。

### 数据目录不可写

确认 `cmd/server/ainexus/` 对 Docker 进程可写，并检查挂载路径：

```bash
docker compose config
docker compose logs ainexus
```

### 端点配置错误

通过 Web UI 检查 API URL、认证模式、转换器和模型。除 `claude` 外的转换器通常必须填写模型。修改前先备份数据库。

## 单独构建镜像

从仓库根目录执行：

```bash
docker build -f cmd/server/Dockerfile -t ainexus .
docker run --rm \
  -p 3021:3000 \
  -v "$PWD/ainexus-data:/data" \
  -e AINEXUS_LISTEN_MODE=lan \
  -e AINEXUS_BASIC_AUTH_PASSWORD="replace-with-a-strong-password" \
  ainexus
```
