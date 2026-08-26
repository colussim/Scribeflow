#!/usr/bin/env bash
# Télécharge mermaid.min.js (via npm) et le place dans
# internal/mermaid/assets/mermaid.min.js pour qu'il soit embarqué dans le
# binaire via go:embed. À relancer si vous voulez changer de version de
# mermaid (variable MERMAID_VERSION ci-dessous).
set -euo pipefail

MERMAID_VERSION="${MERMAID_VERSION:-10}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ASSETS_DIR="$ROOT_DIR/internal/mermaid/assets"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Téléchargement de mermaid@${MERMAID_VERSION}..."
( cd "$TMP_DIR" && npm pack "mermaid@${MERMAID_VERSION}" --silent )

TARBALL="$(ls "$TMP_DIR"/mermaid-*.tgz | head -1)"
tar xzf "$TARBALL" -C "$TMP_DIR"

mkdir -p "$ASSETS_DIR"
cp "$TMP_DIR/package/dist/mermaid.min.js" "$ASSETS_DIR/mermaid.min.js"

echo "OK -> $ASSETS_DIR/mermaid.min.js ($(du -h "$ASSETS_DIR/mermaid.min.js" | cut -f1))"
