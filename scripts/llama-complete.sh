#!/usr/bin/env bash
set -euo pipefail

if [[ "${1:-}" == "--docker" ]]; then
  shift
  DOCKER_CONTAINER=${DOCKER_CONTAINER:-eve-orchestrator}
  quoted_args=""
  for arg in "$@"; do
    quoted_args+=" $(printf '%q' "$arg")"
  done
  exec docker exec "$DOCKER_CONTAINER" sh -lc "cd /workspace && ./scripts/llama-complete.sh${quoted_args}"
fi

HOST=${HOST:-http://127.0.0.1:5014}
MODEL=${MODEL:-${REASONING_MODEL:-}}
PROMPT=${PROMPT:-}
GRAMMAR_FILE=${GRAMMAR_FILE:-}
MAX_TOKENS=${MAX_TOKENS:-256}
TEMPERATURE=${TEMPERATURE:-0}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --host)
      HOST=$2
      shift 2
      ;;
    --model)
      MODEL=$2
      shift 2
      ;;
    --prompt)
      PROMPT=$2
      shift 2
      ;;
    --prompt-file)
      PROMPT=$(cat "$2")
      shift 2
      ;;
    --grammar-file)
      GRAMMAR_FILE=$2
      shift 2
      ;;
    --max-tokens)
      MAX_TOKENS=$2
      shift 2
      ;;
    --temperature)
      TEMPERATURE=$2
      shift 2
      ;;
    *)
      echo "unknown arg: $1" >&2
      exit 1
      ;;
  esac
done

if [[ -z "$PROMPT" ]]; then
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

"$PYTHON_BIN" - "$HOST" "$MODEL" "$PROMPT" "$GRAMMAR_FILE" "$MAX_TOKENS" "$TEMPERATURE" <<'PY'
import json
import sys
from pathlib import Path
from urllib.request import Request, urlopen

host, model, prompt, grammar_file, max_tokens, temperature = sys.argv[1:]
payload = {
    "prompt": prompt,
    "stream": False,
    "max_tokens": int(max_tokens),
    "temperature": float(temperature),
}
if model:
    payload["model"] = model
if grammar_file:
    payload["structured_outputs"] = {
        "grammar": Path(grammar_file).read_text(),
    }

req = Request(
    host.rstrip("/") + "/v1/completions",
    data=json.dumps(payload).encode("utf-8"),
    headers={"Content-Type": "application/json"},
    method="POST",
)
with urlopen(req) as resp:
    body = resp.read().decode("utf-8")
print(body)
PY
