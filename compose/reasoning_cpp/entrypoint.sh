#!/bin/sh

echo "Starting reasoning-cpp server with:"
echo "Model name: $REASONING_MODEL"
#echo "Max tokens: $ORPHEUS_MAX_TOKENS"

exec /app/llama-server \
  -m /models/"${REASONING_MODEL}" \
  --port "${REASONING_PORT}" \
  --host 0.0.0.0
#  --n-gpu-layers 29 \
#  --ctx-size "${ORPHEUS_MAX_TOKENS}" \
#  --n-predict "${ORPHEUS_MAX_TOKENS}" \
#  --rope-scaling linear
