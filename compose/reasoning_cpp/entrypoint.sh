#!/bin/sh
set -eu

MODEL_DIR="${MODEL_DIR:-/models}"
MODEL_NAME="${REASONING_MODEL:?REASONING_MODEL is required}"
MODEL_PATH="${REASONING_MODEL_PATH:-${MODEL_DIR}/${MODEL_NAME}}"
PORT="${REASONING_PORT:-5014}"
HOST="${REASONING_HOST:-0.0.0.0}"
CTX_SIZE="${LLAMA_CTX_SIZE:-2048}"

echo "Starting reasoning-cpp server with:"
echo "  model: $MODEL_NAME"
echo "  model path: $MODEL_PATH"
echo "  host: $HOST"
echo "  port: $PORT"
echo "  ctx size: $CTX_SIZE"

exec /app/llama-server \
  -m "$MODEL_PATH" \
  --port "$PORT" \
  --host "$HOST" \
  --ctx-size "$CTX_SIZE"
#  --n-gpu-layers 29 \
#  --ctx-size "${ORPHEUS_MAX_TOKENS}" \
#  --n-predict "${ORPHEUS_MAX_TOKENS}" \
#  --rope-scaling linear
