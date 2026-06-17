#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--docker" ]]; then
  shift
  docker_container=eve-orchestrator
  quoted_args=""
  for arg in "$@"; do
    quoted_args+=" $(printf '%q' "$arg")"
  done
  exec docker exec "$docker_container" sh -lc "cd /workspace && ./scripts/llama-chat.sh${quoted_args}"
fi

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
config_file="$root_dir/config/config.yaml"
llm_http_url="$(python3 "$root_dir/scripts/config-value.py" "$config_file" llm http_url)"
host="${llm_http_url%/v1/chat/completions}"
model="$(python3 "$root_dir/scripts/config-value.py" "$config_file" llm model)"
prompt=
temperature=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)
      host=$2
      shift 2
      ;;
    --model)
      model=$2
      shift 2
      ;;
    --prompt)
      prompt=$2
      shift 2
      ;;
    --prompt-file)
      prompt=$(cat "$2")
      shift 2
      ;;
    --temperature)
      temperature=$2
      shift 2
      ;;
    *)
      echo "unknown arg: $1" >&2
      exit 1
      ;;
  esac
done

if [[ -z "$prompt" ]]; then
  echo "error: provide --prompt or --prompt-file" >&2
  exit 1
fi

PYTHON_BIN=
if command -v python3 >/dev/null 2>&1; then
  PYTHON_BIN=python3
elif command -v python >/dev/null 2>&1; then
  PYTHON_BIN=python
fi

if [[ -z "$PYTHON_BIN" ]]; then
  echo "error: python3 or python is required" >&2
  exit 1
fi

"$PYTHON_BIN" - "$host" "$model" "$prompt" "$temperature" <<'PY'
import json
import sys
from urllib.request import Request, urlopen

host, model, prompt, temperature = sys.argv[1:]
payload = {
    "messages": [
        {"role": "user", "content": prompt},
    ],
    "stream": False,
    "temperature": float(temperature),
}
if model:
    payload["model"] = model

req = Request(
    host.rstrip("/") + "/v1/chat/completions",
    data=json.dumps(payload).encode("utf-8"),
    headers={"Content-Type": "application/json"},
    method="POST",
)
with urlopen(req) as resp:
    body = resp.read().decode("utf-8")
print(body)
PY
