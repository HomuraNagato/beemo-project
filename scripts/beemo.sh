#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [ -x "$ROOT_DIR/bin/beemo" ]; then
  exec "$ROOT_DIR/bin/beemo" "$@"
fi

export GOCACHE="${GOCACHE:-/tmp/beemo-go-cache}"
exec go run ./cmd/beemo "$@"
