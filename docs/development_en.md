# Development Guide

## Requirements

- Go 1.24+
- Node.js 18+
- Wails CLI v2
- Platform-specific system dependencies required by Wails

```bash
go install github.com/wailsapp/wails/v2/cmd/wails@latest
wails doctor
```

## Desktop Development

The desktop app uses Wails v2. Its frontend is built with Vite and uses native JavaScript/CSS.

```bash
cd cmd/desktop/frontend
npm install

cd ..
wails dev
```

`wails dev` starts the frontend hot-reload server and launches the desktop app.

## Desktop Builds

Run these commands from `cmd/desktop`:

```bash
wails build
wails build -platform linux/amd64
wails build -platform darwin/amd64
wails build -platform windows/amd64
```

Build output is written to `cmd/desktop/build/bin/`. Cross-platform builds still require the target toolchain expected by Wails.

## Server Mode

Run from the repository root:

```bash
go run ./cmd/server
```

Or build a standalone binary:

```bash
cd cmd/server
go build -ldflags="-s -w" -o ainexus-server .
./ainexus-server
```

The default listener is `127.0.0.1:3000`, and the Web UI is available at `http://127.0.0.1:3000/ui/`.

## Distribution Site

`site/` is a separate Vue 3, TypeScript, and Vite project:

```bash
cd site
npm install
npm run dev
```

Build and test it with:

```bash
npm run build
npm test
```

## Tests and Code Quality

Run from the repository root:

```bash
go test ./... -count=1
go vet ./...
go fmt ./...
```

Run focused package tests:

```bash
go test -v ./internal/proxy/...
go test -v ./internal/transformer/convert/...
```

Desktop frontend tests are under `cmd/desktop/frontend/test/`. Site tests run with `cd site && npm test`.

## Docker

```bash
cd cmd/server
docker compose up -d --build
```

See the [Docker Deployment Guide](README_DOCKER.md) for details.

## Project Structure

```text
AINexus/
├── cmd/
│   ├── desktop/              # Wails desktop application
│   │   ├── frontend/         # Vite + native JavaScript/CSS
│   │   └── main.go
│   └── server/               # Headless server and embedded Web UI
│       ├── webui/
│       ├── Dockerfile
│       └── main.go
├── internal/
│   ├── config/               # Configuration and endpoint rules
│   ├── proxy/                # HTTP proxy, rotation, and failover
│   ├── storage/              # SQLite storage
│   ├── transformer/          # API protocol conversion
│   ├── webdav/               # WebDAV synchronization
│   └── tray/                 # Desktop system tray
└── site/                     # Vue 3 distribution site
```
