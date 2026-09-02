#!/usr/bin/env bash
# Author: Emmanuel COLUSSI
# Build confluence-publish for Windows (amd64) and macOS (amd64 + arm64).
# Requires Go >= 1.21 and internal/mermaid/assets/mermaid.min.js
# (see scripts/fetch-mermaid.sh).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [ ! -f internal/mermaid/assets/mermaid.min.js ]; then
  echo "internal/mermaid/assets/mermaid.min.js is missing; running fetch-mermaid.sh..."
  ./scripts/fetch-mermaid.sh
fi

mkdir -p dist
export CGO_ENABLED=0

build() {
  local goos="$1" goarch="$2" out="$3"
  echo "-> $goos/$goarch"
  GOOS="$goos" GOARCH="$goarch" go build -ldflags "-s -w" -o "dist/$out" ./cmd/confluence-publish
}

build windows amd64 confluence-publish-windows-amd64.exe
build darwin  amd64 confluence-publish-darwin-amd64
build darwin  arm64 confluence-publish-darwin-arm64

echo
echo "Binaries generated in dist/:"
ls -la dist/
