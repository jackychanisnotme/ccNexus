# 开发指南

## 环境要求

- Go 1.24+
- Node.js 18+
- Wails CLI v2
- 桌面平台所需的 Wails 系统依赖

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails doctor
```

## 桌面应用开发

桌面应用使用 Wails v2，前端由 Vite 构建，界面代码为原生 JavaScript/CSS。

```bash
cd cmd/desktop/frontend
npm install

cd ..
wails dev
```

`wails dev` 会启动前端热重载服务并运行桌面应用。

## 桌面应用构建

在 `cmd/desktop` 目录执行：

```bash
wails build
wails build -platform linux/amd64
wails build -platform darwin/amd64
wails build -platform windows/amd64
```

构建产物位于 `cmd/desktop/build/bin/`。跨平台构建仍需满足 Wails 对目标平台工具链的要求。

## 服务器模式

从仓库根目录运行：

```bash
go run ./cmd/server
```

或构建独立二进制：

```bash
cd cmd/server
go build -ldflags="-s -w" -o ainexus-server .
./ainexus-server
```

默认监听 `127.0.0.1:3000`，Web 管理界面位于 `http://127.0.0.1:3000/ui/`。

## 分发站点

`site/` 是独立的 Vue 3 + TypeScript + Vite 项目：

```bash
cd site
npm install
npm run dev
```

构建和测试：

```bash
npm run build
npm test
```

## 测试与代码质量

在仓库根目录执行：

```bash
go test ./... -count=1
go vet ./...
go fmt ./...
```

运行重点模块测试：

```bash
go test -v ./internal/proxy/...
go test -v ./internal/transformer/convert/...
```

桌面前端测试文件位于 `cmd/desktop/frontend/test/`，站点测试通过 `cd site && npm test` 运行。

## Docker

```bash
cd cmd/server
docker compose up -d --build
```

详细配置见 [Docker 部署指南](README_DOCKER.md)。

## 项目结构

```text
AINexus/
├── cmd/
│   ├── desktop/              # Wails 桌面应用
│   │   ├── frontend/         # Vite + 原生 JavaScript/CSS
│   │   └── main.go
│   └── server/               # 无头服务器与嵌入式 Web UI
│       ├── webui/
│       ├── Dockerfile
│       └── main.go
├── internal/
│   ├── config/               # 配置与端点规则
│   ├── proxy/                # HTTP 代理、轮换与故障转移
│   ├── storage/              # SQLite 存储
│   ├── transformer/          # API 协议转换
│   ├── webdav/               # WebDAV 同步
│   └── tray/                 # 桌面系统托盘
└── site/                     # Vue 3 分发站点
```
