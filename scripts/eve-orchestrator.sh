#!/usr/bin/env bash
set -euo pipefail

ORCH_ADDR=${ORCH_ADDR:-127.0.0.1:5013}
PROMPT=${1:-"What time is it?"}
SESSION_ID=${SESSION_ID:-"cli"}

payload=$(cat <<JSON
{
  "session_id": "${SESSION_ID}",
  "messages": [
    {"role": "user", "content": "${PROMPT}"}
  ]
}
JSON
)

if command -v grpcurl >/dev/null 2>&1; then
  exec grpcurl -plaintext \
    -proto proto/agent.proto \
    -d "$payload" \
    "$ORCH_ADDR" \
    eve.Orchestrator/Chat
fi

if docker ps --filter "name=^/eve-orchestrator$" --filter "status=running" --format '{{.Names}}' | grep -qx eve-orchestrator; then
  exec docker exec -i eve-orchestrator grpcurl -plaintext \
    -proto proto/agent.proto \
    -d "$payload" \
    localhost:5013 \
    eve.Orchestrator/Chat
fi

echo "error: grpcurl not found and eve-orchestrator is not running." >&2
exit 1
