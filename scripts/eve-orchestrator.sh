#!/usr/bin/env bash
set -euo pipefail

orch_addr="127.0.0.1:5013"
prompt=${1:-"What time is it?"}
session_id="cli"

payload=$(cat <<JSON
{
  "session_id": "${session_id}",
  "messages": [
    {"role": "user", "content": "${prompt}"}
  ]
}
JSON
)

if command -v grpcurl >/dev/null 2>&1; then
  exec grpcurl -plaintext \
    -proto proto/agent.proto \
    -d "$payload" \
    "$orch_addr" \
    eve.Orchestrator/Chat
fi

if docker ps --filter "name=^/eve-orchestrator$" --filter "status=running" --format '{{.Names}}' | grep -qx eve-orchestrator; then
  exec docker exec -i eve-orchestrator grpcurl -plaintext \
    -proto proto/agent.proto \
    -d "$payload" \
    127.0.0.1:5013 \
    eve.Orchestrator/Chat
fi

echo "error: grpcurl not found and eve-orchestrator is not running." >&2
exit 1
