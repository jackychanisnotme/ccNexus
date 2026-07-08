#!/usr/bin/env bash
set -euo pipefail

if ! command -v go >/dev/null 2>&1; then
  cat >&2 <<'EOF'
Go is not available in PATH.

Install Go 1.24.3+ on the machine/environment running Claude, then retry:
  macOS Homebrew: brew install go
  Official installer: https://go.dev/doc/install

This repository declares:
  go 1.24.0
  toolchain go1.24.3
EOF
  exit 127
fi

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

echo "==> Go version"
go version

echo "==> Formatting Go files"
go fmt ./...

echo "==> Running Go tests"
go test ./... -count=1
