#!/usr/bin/env bash
set -euo pipefail

ORCH_ADDR=${ORCH_ADDR:-localhost:5013}
PROMPT=${1:-"What time is it?"}
SESSION_ID=${SESSION_ID:-"cli"}

if ! command -v grpcurl >/dev/null 2>&1; then
  echo "error: grpcurl not found. Install it or run via a Go client." >&2
  exit 1
fi

payload=$(cat <<JSON
{
  "session_id": "${SESSION_ID}",
  "messages": [
    {"role": "user", "content": "${PROMPT}"}
  ]
}
JSON
)

grpcurl -plaintext \
  -proto proto/agent.proto \
  -d "$payload" \
  "$ORCH_ADDR" \
  eve.Orchestrator/Chat
