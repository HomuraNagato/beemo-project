#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' "usage: $0 [gpu|cpu] [vllm|llama] [--no-voice] [--restart-orchestrator]"
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ACCEL="${BEEMO_ACCEL:-gpu}"
RUNTIME="${BEEMO_RUNTIME:-vllm}"
VOICE="${BEEMO_VOICE:-1}"
RESTART_ORCH="${BEEMO_RESTART_ORCH:-0}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    gpu|cpu)
      ACCEL="$1"
      ;;
    vllm|llama|llamacpp)
      RUNTIME="$1"
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

case "$RUNTIME" in
  vllm|llama|llamacpp) ;;
  *)
    printf 'invalid runtime %q; expected vllm or llama\n' "$RUNTIME" >&2
    exit 2
    ;;
esac

compose_files=(-f docker-compose.yaml -f "docker-compose.${ACCEL}.yaml")
if [ "$ACCEL" = "cpu" ]; then
  case "$RUNTIME" in
    vllm)
      compose_files+=(-f docker-compose.cpu.vllm.yaml)
      ;;
    llama|llamacpp)
      compose_files+=(-f docker-compose.cpu.llamacpp.yaml)
      ;;
  esac
elif [ "$RUNTIME" != "vllm" ]; then
  printf 'runtime %q is only supported for cpu right now\n' "$RUNTIME" >&2
  exit 2
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

wait_grpc_health() {
  name="$1"
  container="$2"
  service="$3"
  timeout="${4:-120}"
  start="$(date +%s)"
  while true; do
    if docker exec "$container" grpcurl -plaintext \
      -d "{\"service\":\"$service\"}" \
      localhost:5013 grpc.health.v1.Health/Check >/dev/null 2>&1; then
      printf '%s is ready\n' "$name"
      return 0
    fi
    if ! docker ps --filter "name=^/${container}$" --filter "status=running" --format '{{.Names}}' | grep -qx "$container"; then
      printf '%s container is not running\n' "$name" >&2
      docker logs --tail 80 "$container" >&2 || true
      return 1
    fi
    now="$(date +%s)"
    if [ $((now - start)) -ge "$timeout" ]; then
      printf 'timed out waiting for %s gRPC health\n' "$name" >&2
      docker logs --tail 80 "$container" >&2 || true
      return 1
    fi
    sleep 2
  done
}

cd "$ROOT_DIR"

printf 'Starting Beemo services using %s/%s compose stack\n' "$ACCEL" "$RUNTIME"

compose up -d --build --force-recreate eve-reasoning
wait_http "eve-reasoning" "http://127.0.0.1:5014/health" "eve-reasoning" 300

compose up -d --build --force-recreate eve-embedding
wait_http "eve-embedding" "http://127.0.0.1:5021/health" "eve-embedding" 300
wait_http "eve-reasoning" "http://127.0.0.1:5014/health" "eve-reasoning" 120

if [ "$VOICE" = "1" ]; then
  compose up -d --build eve-asr
fi

if [ "$RESTART_ORCH" = "1" ]; then
  compose rm -sf eve-orchestrator >/dev/null 2>&1 || true
fi

compose up -d --build --force-recreate eve-orchestrator
wait_grpc_health "eve-orchestrator" "eve-orchestrator" "eve.Orchestrator" 180

if [ "$VOICE" = "1" ]; then
  compose up -d --build eve-wakeword
fi

printf 'Beemo startup command complete\n'
if [ "$VOICE" = "0" ]; then
  printf 'Voice disabled; starting TUI. Exit with /quit or Ctrl-D.\n'
  exec ./scripts/eve-tui.sh
fi

printf 'Try: ./scripts/eve-orchestrator.sh "what time is it?"\n'
