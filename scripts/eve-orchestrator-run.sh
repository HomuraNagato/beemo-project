#!/usr/bin/env bash
set -euo pipefail

ORCH_CONTAINER=${ORCH_CONTAINER:-eve-orchestrator}
GO_BIN=${GO_BIN:-/usr/local/go/bin/go}

exec docker exec -d "$ORCH_CONTAINER" bash -lc "cd /workspace && exec $GO_BIN run ./src/orchestrator >/proc/1/fd/1 2>/proc/1/fd/2"
