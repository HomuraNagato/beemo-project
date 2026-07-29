#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [ -x "$ROOT_DIR/bin/beemo" ] \
  && ! find "$ROOT_DIR/cmd" "$ROOT_DIR/internal" -type f -name '*.go' -newer "$ROOT_DIR/bin/beemo" -print -quit | grep -q .; then
  exec "$ROOT_DIR/bin/beemo" "$@"
fi

export GOCACHE="${GOCACHE:-/tmp/beemo-go-cache}"
exec go run ./cmd/beemo "$@"
