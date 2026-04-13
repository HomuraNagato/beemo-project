#!/usr/bin/env bash
set -euo pipefail

GO_BIN=${GO_BIN:-go}
if ! command -v "$GO_BIN" >/dev/null 2>&1; then
  if [ -x /usr/local/go/bin/go ]; then
    GO_BIN=/usr/local/go/bin/go
  else
    echo "error: go not found on PATH and /usr/local/go/bin/go is unavailable" >&2
    exit 1
  fi
fi

exec "$GO_BIN" run ./src/tui "$@"
