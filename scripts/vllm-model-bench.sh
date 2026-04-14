#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)
ENV_FILE="$ROOT_DIR/.env"
MODELS_DIR="$ROOT_DIR/models"
REPORT_DIR_DEFAULT="$ROOT_DIR/memory"
REPORT_DIR="${REPORT_DIR:-$REPORT_DIR_DEFAULT}"
REPORT_FILE=""
COMPOSE_FILES=(-f "$ROOT_DIR/docker-compose.yaml" -f "$ROOT_DIR/docker-compose.gpu.yaml")

DOWNLOAD_MISSING=0
INCLUDE_LOCAL=0
INCLUDE_RECOMMENDED=0
READY_TIMEOUT=600
KEEP_LAST=0
HOST_URL=${HOST_URL:-http://eve-vllm:5014}
CONTAINER=${CONTAINER:-eve-vllm}
ORCH_CONTAINER=${ORCH_CONTAINER:-eve-orchestrator}

declare -a MODEL_SPECS=()
declare -a LOCAL_MODELS=()
declare -a RECOMMENDED_MODELS=(
  "Qwen/Qwen2.5-1.5B-Instruct"
  "Qwen/Qwen2.5-1.5B-Instruct-GPTQ-Int4"
  "Qwen/Qwen2.5-3B-Instruct"
)

usage() {
  cat <<'EOF'
Usage:
  scripts/vllm-model-bench.sh [options]

Options:
  --model <hf_repo_or_local_name>   Add a model to test. Repeatable.
  --include-local                   Include every local model directory under ./models.
  --include-recommended             Include a small curated Qwen list.
  --download-missing                Download missing Hugging Face repo ids with `hf download`.
  --ready-timeout <seconds>         Wait timeout for vLLM startup. Default: 600.
  --keep-last                       Leave .env pointing at the last tested model.
  --host <url>                      vLLM base URL. Default: http://eve-vllm:5014
  --help                            Show this help.

Examples:
  scripts/vllm-model-bench.sh --include-local
  scripts/vllm-model-bench.sh --model Qwen/Qwen2.5-1.5B-Instruct --model Qwen/Qwen2.5-1.5B-Instruct-GPTQ-Int4 --download-missing
  scripts/vllm-model-bench.sh --include-local --include-recommended --download-missing

Output:
  Writes a TSV report under memory/ and prints per-model summaries.
EOF
}

log() {
  printf '[vllm-bench] %s\n' "$*"
}

die() {
  log "error: $*" >&2
  exit 1
}

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --model)
      MODEL_SPECS+=("$2")
      shift 2
      ;;
    --include-local)
      INCLUDE_LOCAL=1
      shift
      ;;
    --include-recommended)
      INCLUDE_RECOMMENDED=1
      shift
      ;;
    --download-missing)
      DOWNLOAD_MISSING=1
      shift
      ;;
    --ready-timeout)
      READY_TIMEOUT="$2"
      shift 2
      ;;
    --keep-last)
      KEEP_LAST=1
      shift
      ;;
    --host)
      HOST_URL="$2"
      shift 2
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      die "unknown arg: $1"
      ;;
  esac
done

have_cmd docker || die "docker is required"
have_cmd python3 || die "python3 is required"

mkdir -p "$REPORT_DIR" 2>/dev/null || true
if [[ ! -d "$REPORT_DIR" || ! -w "$REPORT_DIR" ]]; then
  REPORT_DIR="/tmp/vllm-model-bench"
  mkdir -p "$REPORT_DIR"
fi
REPORT_FILE="$REPORT_DIR/vllm-model-bench-$(date +%Y%m%d-%H%M%S).tsv"

if [[ $INCLUDE_LOCAL -eq 1 && -d "$MODELS_DIR" ]]; then
  while IFS= read -r model_dir; do
    LOCAL_MODELS+=("$(basename "$model_dir")")
  done < <(find "$MODELS_DIR" -mindepth 1 -maxdepth 1 -type d | sort)
fi

if [[ $INCLUDE_RECOMMENDED -eq 1 ]]; then
  MODEL_SPECS+=("${RECOMMENDED_MODELS[@]}")
fi
MODEL_SPECS+=("${LOCAL_MODELS[@]}")

if [[ ${#MODEL_SPECS[@]} -eq 0 ]]; then
  die "no models selected; use --model, --include-local, or --include-recommended"
fi

mapfile -t UNIQUE_SPECS < <(printf '%s\n' "${MODEL_SPECS[@]}" | awk 'NF && !seen[$0]++')
MODEL_SPECS=("${UNIQUE_SPECS[@]}")

if [[ ! -f "$ENV_FILE" ]]; then
  die ".env not found at $ENV_FILE"
fi

ORIGINAL_ENV=$(mktemp)
cp "$ENV_FILE" "$ORIGINAL_ENV"

cleanup() {
  if [[ $KEEP_LAST -eq 0 && -f "$ORIGINAL_ENV" ]]; then
    cp "$ORIGINAL_ENV" "$ENV_FILE"
  fi
  rm -f "$ORIGINAL_ENV"
}
trap cleanup EXIT

set_env_model() {
  local model_name="$1"
  python3 - "$ENV_FILE" "$model_name" <<'PY'
import pathlib
import re
import sys

env_path = pathlib.Path(sys.argv[1])
model_name = sys.argv[2]
text = env_path.read_text()
text = re.sub(r'(?m)^REASONING_MODEL=.*$', f'REASONING_MODEL={model_name}', text)
env_path.write_text(text)
PY
}

model_name_from_spec() {
  local spec="$1"
  printf '%s\n' "${spec##*/}"
}

local_model_path() {
  local model_name="$1"
  printf '%s/%s\n' "$MODELS_DIR" "$model_name"
}

download_model_if_needed() {
  local spec="$1"
  local model_name="$2"
  local local_path
  local_path=$(local_model_path "$model_name")
  if [[ -d "$local_path" ]]; then
    return 0
  fi
  if [[ $DOWNLOAD_MISSING -ne 1 ]]; then
    die "model not found locally: $local_path (use --download-missing and pass a Hugging Face repo id)"
  fi
  [[ "$spec" == */* ]] || die "cannot download local-only model name: $spec"
  have_cmd hf || die "`hf` CLI is required for --download-missing"
  log "downloading $spec -> ./models/$model_name"
  (cd "$ROOT_DIR" && hf download "$spec" --local-dir "./models/$model_name")
}

start_log_tail() {
  docker logs -f --tail 60 "$CONTAINER" &
  LOG_TAIL_PID=$!
}

stop_log_tail() {
  if [[ -n "${LOG_TAIL_PID:-}" ]]; then
    kill "$LOG_TAIL_PID" >/dev/null 2>&1 || true
    wait "$LOG_TAIL_PID" 2>/dev/null || true
    unset LOG_TAIL_PID
  fi
}

restart_stack_for_model() {
  local model_name="$1"
  log "restarting eve-vllm + eve-orchestrator for $model_name"
  start_log_tail
  docker compose "${COMPOSE_FILES[@]}" up -d --force-recreate eve-vllm eve-orchestrator >/dev/null
}

wait_for_vllm() {
  local model_name="$1"
  local deadline=$(( $(date +%s) + READY_TIMEOUT ))
  while (( $(date +%s) < deadline )); do
    if docker logs "$CONTAINER" 2>&1 | grep -q 'Starting vLLM API server'; then
      if docker exec "$ORCH_CONTAINER" sh -lc "cd /workspace && ./scripts/llama-chat.sh --host $HOST_URL --model $model_name --prompt 'ok'" >/dev/null 2>&1; then
        stop_log_tail
        return 0
      fi
    fi
    sleep 2
  done
  stop_log_tail
  return 1
}

elapsed_ms() {
  python3 - "$1" "$2" <<'PY'
import sys
start = float(sys.argv[1])
end = float(sys.argv[2])
print(int((end - start) * 1000))
PY
}

run_probe() {
  local label="$1"
  shift
  local tmp
  tmp=$(mktemp)
  local start end ms status output
  start=$(python3 - <<'PY'
import time
print(time.time())
PY
)
  if "$@" >"$tmp" 2>&1; then
    status="ok"
  else
    status="error"
  fi
  end=$(python3 - <<'PY'
import time
print(time.time())
PY
)
  ms=$(elapsed_ms "$start" "$end")
  output=$(tr '\n' ' ' <"$tmp" | sed 's/[[:space:]]\+/ /g' | sed 's/^ //; s/ $//')
  rm -f "$tmp"
  printf '%s\t%s\t%s\n' "$label" "$status" "$ms" >>"$CURRENT_MODEL_REPORT"
  printf '  %-20s %-5s %6sms\n' "$label" "$status" "$ms"
  printf '    %s\n' "$output"
}

benchmark_model() {
  local spec="$1"
  local model_name="$2"

  CURRENT_MODEL_REPORT=$(mktemp)
  : >"$CURRENT_MODEL_REPORT"

  printf '\n=== %s ===\n' "$model_name"

  run_probe "chat_one_word" \
    docker exec "$ORCH_CONTAINER" sh -lc "cd /workspace && ./scripts/llama-chat.sh --host $HOST_URL --model $model_name --prompt 'Reply with exactly one word: ok.'"

  run_probe "grammar_min_array" \
    docker exec "$ORCH_CONTAINER" sh -lc "cd /workspace && ./scripts/llama-complete.sh --host $HOST_URL --model $model_name --prompt 'Return the required output exactly.' --grammar-file scripts/grammars/min_array.gbnf"

  run_probe "grammar_tool_math" \
    docker exec "$ORCH_CONTAINER" sh -lc "cd /workspace && ./scripts/llama-complete.sh --host $HOST_URL --model $model_name --prompt 'Decide which tools to use for: what is 10 / 4 ? Respond with the required JSON only.' --grammar-file scripts/grammars/tool_probe_math.gbnf"

  run_probe "chat_time_question" \
    docker exec "$ORCH_CONTAINER" sh -lc "cd /workspace && ./scripts/llama-chat.sh --host $HOST_URL --model $model_name --prompt 'What time is it right now? Answer in one sentence.'"

  run_probe "chat_relative_date" \
    docker exec "$ORCH_CONTAINER" sh -lc "cd /workspace && ./scripts/llama-chat.sh --host $HOST_URL --model $model_name --prompt 'What date will it be five days from today? Answer in one sentence.'"

  while IFS=$'\t' read -r label status ms; do
    printf '%s\t%s\t%s\t%s\n' "$model_name" "$label" "$status" "$ms" >>"$REPORT_FILE"
  done <"$CURRENT_MODEL_REPORT"
  rm -f "$CURRENT_MODEL_REPORT"
}

printf 'model\tprobe\tstatus\tlatency_ms\n' >"$REPORT_FILE"

log "selected models:"
for spec in "${MODEL_SPECS[@]}"; do
  log "  $spec"
done
log "report: $REPORT_FILE"

for spec in "${MODEL_SPECS[@]}"; do
  model_name=$(model_name_from_spec "$spec")
  download_model_if_needed "$spec" "$model_name"
  set_env_model "$model_name"
  restart_stack_for_model "$model_name"
  if ! wait_for_vllm "$model_name"; then
    log "startup failed for $model_name"
    printf '%s\t%s\t%s\t%s\n' "$model_name" "startup" "error" "-1" >>"$REPORT_FILE"
    continue
  fi
  benchmark_model "$spec" "$model_name"
done

log "done"
log "tsv report written to $REPORT_FILE"
if [[ $KEEP_LAST -eq 0 ]]; then
  log ".env will be restored to its original model when this script exits"
else
  log ".env will stay pointed at the last tested model"
fi
