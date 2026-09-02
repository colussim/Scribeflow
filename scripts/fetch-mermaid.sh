#!/usr/bin/env bash
# Author: Emmanuel COLUSSI
# Download mermaid.min.js through npm and place it in
# internal/mermaid/assets/mermaid.min.js so go:embed can include it in the
# binary. Run this script again to change the Mermaid version configured below.
set -euo pipefail

MERMAID_VERSION="${MERMAID_VERSION:-10}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ASSETS_DIR="$ROOT_DIR/internal/mermaid/assets"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading mermaid@${MERMAID_VERSION}..."
( cd "$TMP_DIR" && npm pack "mermaid@${MERMAID_VERSION}" --silent )

TARBALL="$(ls "$TMP_DIR"/mermaid-*.tgz | head -1)"
tar xzf "$TARBALL" -C "$TMP_DIR"

mkdir -p "$ASSETS_DIR"
cp "$TMP_DIR/package/dist/mermaid.min.js" "$ASSETS_DIR/mermaid.min.js"

echo "OK -> $ASSETS_DIR/mermaid.min.js ($(du -h "$ASSETS_DIR/mermaid.min.js" | cut -f1))"
