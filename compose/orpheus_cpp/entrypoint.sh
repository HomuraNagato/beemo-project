#!/bin/sh

echo "Starting orpheus-cpp-server with:"
echo "Model name: $ORPHEUS_MODEL_NAME"
echo "Max tokens: $ORPHEUS_MAX_TOKENS"

exec /app/llama-server \
  -m /models/"${ORPHEUS_MODEL_NAME}" \
  --port "${ORPHEUS_API_PORT}" \
  --host 0.0.0.0 \
  --n-gpu-layers 29 \
  --ctx-size "${ORPHEUS_MAX_TOKENS}" \
  --n-predict "${ORPHEUS_MAX_TOKENS}" \
  --rope-scaling linear
