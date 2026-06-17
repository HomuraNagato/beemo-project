#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' "usage: beemo-status.sh [vllm-gpu|vllm-cpu|llama-cpu]"
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

printf 'profile: %s\n' "$BEEMO_PROFILE_NAME"
docker compose "${BEEMO_COMPOSE_FILES[@]}" ps

printf '\nhealth:\n'
for item in eve-reasoning:http://127.0.0.1:5014/health eve-embedding:http://127.0.0.1:5021/health; do
  name="${item%%:*}"
  url="${item#*:}"
  if curl -fsS "$url" >/dev/null 2>&1; then
    printf '  %-14s ok\n' "$name"
  else
    printf '  %-14s not ready\n' "$name"
  fi
done

if docker ps --filter "name=^/eve-orchestrator$" --filter "status=running" --format '{{.Names}}' | grep -qx eve-orchestrator; then
  if docker exec eve-orchestrator grpcurl -plaintext \
    -d '{"service":"eve.Orchestrator"}' \
    localhost:5013 grpc.health.v1.Health/Check >/dev/null 2>&1; then
    printf '  %-14s ok\n' "eve-orchestrator"
  else
    printf '  %-14s not ready\n' "eve-orchestrator"
  fi
else
  printf '  %-14s not running\n' "eve-orchestrator"
fi
