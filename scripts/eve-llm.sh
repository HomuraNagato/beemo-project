#!/usr/bin/env bash
set -euo pipefail

LLM_HTTP_URL=${LLM_HTTP_URL:-http://eve-vllm:5014/v1/chat/completions}
MODEL=${REASONING_MODEL:-Qwen2.5-7B-Instruct}
PROMPT=${1:-"what is the definition of mellifluous?"}

payload=$(cat <<JSON
{
  "model": "${MODEL}",
  "messages": [
    {"role": "user", "content": "${PROMPT}"}
  ],
  "stream": false
}
JSON
)

echo $payload

curl -sS \
  -H "Content-Type: application/json" \
  -d "$payload" \
  "$LLM_HTTP_URL"
