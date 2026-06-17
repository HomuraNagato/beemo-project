#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' "usage: beemo-stop.sh [vllm-gpu|vllm-cpu|llama-cpu]"
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

docker compose "${BEEMO_COMPOSE_FILES[@]}" stop \
  eve-wakeword \
  eve-asr \
  eve-orchestrator \
  eve-reasoning \
  eve-embedding
