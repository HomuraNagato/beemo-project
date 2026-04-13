#!/usr/bin/env bash
set -euo pipefail

CONTAINER=${1:-eve-vllm}

echo "== docker ps =="
docker ps --format '{{.Names}}\t{{.Status}}\t{{.Image}}' | grep "^${CONTAINER}" || {
  echo "container not found: ${CONTAINER}" >&2
  exit 1
}

echo
echo "== env =="
docker exec "$CONTAINER" sh -lc 'env | sort | grep -E "^(REASONING_|LLM_|MODEL|HOSTNAME)=" || true'

echo
echo "== process =="
docker exec "$CONTAINER" sh -lc 'ps -ef'

echo
echo "== entrypoint =="
sed -n '1,200p' compose/reasoning_vllm/entrypoint.sh

echo
echo "== compose service =="
sed -n "/${CONTAINER}:/,/^[^[:space:]]/p" docker-compose.yaml | sed '$d'
