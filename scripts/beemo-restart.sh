#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: beemo-restart.sh [vllm-gpu|vllm-cpu|llama-cpu] <service>

Examples:
  beemo-restart.sh vllm-gpu eve-reasoning
  beemo-restart.sh eve-orchestrator
USAGE
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
source "$ROOT_DIR/scripts/beemo-lib.sh"
PROFILE="$(beemo_default_profile)"
SERVICE=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    vllm-gpu|vllm-cpu|llama-cpu)
      PROFILE="$1"
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --*)
      usage >&2
      printf 'unknown argument: %s\n' "$1" >&2
      exit 2
      ;;
    *)
      if [ -n "$SERVICE" ]; then
        usage >&2
        printf 'only one service may be provided\n' >&2
        exit 2
      fi
      SERVICE="$1"
      ;;
  esac
  shift
done

if [ -z "$SERVICE" ]; then
  usage >&2
  printf 'missing service\n' >&2
  exit 2
fi

cd "$ROOT_DIR"
beemo_set_profile "$PROFILE"

docker compose "${BEEMO_COMPOSE_FILES[@]}" up -d --build --force-recreate "$SERVICE"
