#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' "usage: $0 [gpu|cpu] [--db|--no-db] [--no-voice] [--restart-orchestrator]"
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ACCEL="${BEEMO_ACCEL:-gpu}"
DB_MODE="${BEEMO_DB:-external}"
VOICE="${BEEMO_VOICE:-1}"
RESTART_ORCH="${BEEMO_RESTART_ORCH:-0}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    gpu|cpu)
      ACCEL="$1"
      ;;
    --db|--local-db)
      DB_MODE="local"
      ;;
    --no-db|--external-db)
      DB_MODE="external"
      ;;
    --no-voice)
      VOICE="0"
      ;;
    --restart-orchestrator)
      RESTART_ORCH="1"
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

case "$ACCEL" in
  gpu|cpu) ;;
  *)
    printf 'invalid accelerator %q; expected gpu or cpu\n' "$ACCEL" >&2
    exit 2
    ;;
esac

compose_files=(-f docker-compose.yaml -f "docker-compose.${ACCEL}.yaml")
if [ "$DB_MODE" = "local" ]; then
  compose_files+=(-f docker-compose.pensieve.yaml)
fi

compose() {
  docker compose "${compose_files[@]}" "$@"
}

wait_http() {
  name="$1"
  url="$2"
  container="${3:-}"
  timeout="${4:-240}"
  start="$(date +%s)"
  while true; do
    if curl -fsS "$url" >/dev/null 2>&1; then
      printf '%s is ready\n' "$name"
      return 0
    fi
    if [ -n "$container" ] && ! docker ps --filter "name=^/${container}$" --filter "status=running" --format '{{.Names}}' | grep -qx "$container"; then
      printf '%s container is not running\n' "$name" >&2
      docker logs --tail 80 "$container" >&2 || true
      return 1
    fi
    now="$(date +%s)"
    if [ $((now - start)) -ge "$timeout" ]; then
      printf 'timed out waiting for %s at %s\n' "$name" "$url" >&2
      if [ -n "$container" ]; then
        docker logs --tail 80 "$container" >&2 || true
      fi
      return 1
    fi
    sleep 2
  done
}

cd "$ROOT_DIR"

printf 'Starting Beemo services using %s compose stack\n' "$ACCEL"
if [ "$DB_MODE" = "local" ]; then
  printf 'Including local Postgres service: pensieve\n'
else
  printf 'Using external Postgres from DATABASE_URL\n'
fi

if [ "$DB_MODE" = "local" ]; then
  compose up -d --build pensieve
  printf 'Waiting briefly for local Postgres container\n'
  sleep 3
fi

compose up -d --build eve-vllm
wait_http "eve-vllm" "http://127.0.0.1:5014/health" "eve-vllm" 300

compose up -d --build eve-embedding
wait_http "eve-embedding" "http://127.0.0.1:5021/health" "eve-embedding" 300

if [ "$VOICE" = "1" ]; then
  compose up -d --build eve-asr
fi

if [ "$RESTART_ORCH" = "1" ]; then
  compose rm -sf eve-orchestrator >/dev/null 2>&1 || true
fi

compose up -d --build --force-recreate eve-orchestrator

if [ "$VOICE" = "1" ]; then
  compose up -d --build eve-wakeword
fi

printf 'Beemo startup command complete\n'
printf 'Try: ./scripts/eve-orchestrator.sh "what time is it?"\n'
