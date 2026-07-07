#!/usr/bin/env bash
set -euo pipefail

ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
PKG="$ROOT/npm/opencode"

if [ "${1:-}" = "--build" ]; then
  make -C "$ROOT" build-all fetch-model
fi

rm -rf "$PKG/dist" "$PKG/prompts" "$PKG/assets"
mkdir -p "$PKG/dist"

for os in darwin linux windows; do
  for arch in amd64 arm64; do
    ext=""
    if [ "$os" = "windows" ]; then ext=".exe"; fi
    src="$ROOT/bin/witness-$os-$arch$ext"
    if [ ! -f "$src" ]; then
      echo "missing $src; run: make build-all" >&2
      exit 1
    fi
    cp "$src" "$PKG/dist/"
  done
done

cp -R "$ROOT/prompts" "$PKG/prompts"

if [ -d "$ROOT/assets/e5-small" ]; then
  mkdir -p "$PKG/assets"
  cp -R "$ROOT/assets/e5-small" "$PKG/assets/e5-small"
fi

chmod +x "$PKG/bin/witness.js"
chmod +x "$PKG/dist"/witness-* 2>/dev/null || true

echo "staged npm package assets in $PKG"
