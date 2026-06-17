#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
config_file="$root_dir/config/config.yaml"

llm_http_url="$(python3 "$root_dir/scripts/config-value.py" "$config_file" llm http_url)"
model="$(python3 "$root_dir/scripts/config-value.py" "$config_file" llm model)"
prompt=${1:-"what is the definition of mellifluous?"}

payload=$(cat <<JSON
{
  "model": "${model}",
  "messages": [
    {"role": "user", "content": "${prompt}"}
  ],
  "stream": false
}
JSON
)

echo $payload

curl -sS \
  -H "Content-Type: application/json" \
  -d "$payload" \
  "$llm_http_url"
