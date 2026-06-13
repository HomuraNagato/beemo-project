#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
APP_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
TARGET="$APP_DIR/beemo-ui.desktop"

mkdir -p "$APP_DIR"
cp "$ROOT_DIR/desktop/beemo-ui.desktop" "$TARGET"
chmod 0644 "$TARGET"

if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database "$APP_DIR" >/dev/null 2>&1 || true
fi

printf 'Installed native Beemo launcher: %s\n' "$TARGET"
