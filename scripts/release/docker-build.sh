#!/usr/bin/env bash
set -euo pipefail

version="${1:-6.1.4}"
image="${2:-ainexus:$version}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

docker build -f "$root/cmd/server/Dockerfile" -t "$image" "$root"

echo "Built Docker image: $image"
echo "Smoke test:"
echo "  docker run --rm -p 3021:3000 $image"
echo "  curl http://127.0.0.1:3021/health"
