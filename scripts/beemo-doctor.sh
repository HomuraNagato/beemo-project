#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' "usage: beemo-doctor.sh [vllm-gpu|vllm-cpu|llama-cpu]"
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT_DIR/scripts/beemo-lib.sh"
PROFILE="$(beemo_default_profile)"

while [ "$#" -gt 0 ]; do
  case "$1" in
    vllm-gpu|vllm-cpu|llama-cpu)
      PROFILE="$1"
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      usage >&2
      printf 'unknown argument: %s\n' "$1" >&2
      exit 2
      ;;
  esac
  shift
done

cd "$ROOT_DIR"
beemo_set_profile "$PROFILE"

ok=1
docker_available=0

check() {
  local label="$1"
  shift
  if "$@" >/dev/null 2>&1; then
    printf 'ok   %s\n' "$label"
  else
    printf 'fail %s\n' "$label"
    ok=0
  fi
}

if check "docker is available" docker ps; then
  docker_available=1
fi
check "compose config is valid for $BEEMO_PROFILE_NAME" docker compose "${BEEMO_COMPOSE_FILES[@]}" config --quiet

if [ "$docker_available" -eq 1 ] && docker ps -a --format '{{.Names}}' 2>/dev/null | grep -qx eve-vllm; then
  printf 'warn old eve-vllm container exists; remove with: docker rm -f eve-vllm\n'
fi

reasoning_config="$ROOT_DIR/config/config.yaml"
if [ "$BEEMO_PROFILE_NAME" = "llama-cpu" ]; then
  reasoning_config="$ROOT_DIR/config/config.llamacpp.yaml"
fi
reasoning_model="$(python3 "$ROOT_DIR/scripts/config-value.py" "$reasoning_config" llm model)"
embedding_model="$(python3 "$ROOT_DIR/scripts/config-value.py" "$ROOT_DIR/config/config.yaml" embedding model)"
check "reasoning model directory exists: models/$reasoning_model" test -d "$ROOT_DIR/models/$reasoning_model"
check "embedding model directory exists: models/$embedding_model" test -d "$ROOT_DIR/models/$embedding_model"

if [ "$docker_available" -ne 1 ]; then
  printf 'warn skipping live container checks because Docker is unavailable\n'
elif docker ps --filter "name=^/eve-reasoning$" --filter "status=running" --format '{{.Names}}' 2>/dev/null | grep -qx eve-reasoning; then
  check "eve-reasoning health" curl -fsS http://127.0.0.1:5014/health
else
  printf 'warn eve-reasoning is not running\n'
fi

if [ "$docker_available" -eq 1 ] && docker ps --filter "name=^/eve-embedding$" --filter "status=running" --format '{{.Names}}' 2>/dev/null | grep -qx eve-embedding; then
  check "eve-embedding health" curl -fsS http://127.0.0.1:5021/health
elif [ "$docker_available" -eq 1 ]; then
  printf 'warn eve-embedding is not running\n'
fi

if [ "$ok" -ne 1 ]; then
  exit 1
fi
