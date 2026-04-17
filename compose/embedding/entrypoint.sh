#!/bin/sh
set -eu

MODEL_DIR="${MODEL_DIR:-/models}"
MODEL_NAME="${EMBEDDING_MODEL:?EMBEDDING_MODEL is required}"
MODEL_PATH="${EMBEDDING_MODEL_PATH:-${MODEL_DIR}/${MODEL_NAME}}"
SERVED_MODEL_NAME="${EMBEDDING_SERVED_MODEL_NAME:-$MODEL_NAME}"
PORT="${EMBEDDING_PORT:-5021}"
HOST="${EMBEDDING_HOST:-0.0.0.0}"
DEVICE="${EMBEDDING_DEVICE:-${VLLM_DEVICE:-auto}}"
DTYPE="${EMBEDDING_DTYPE:-}"
MAX_LEN="${EMBEDDING_MAX_MODEL_LEN:-8192}"
TP="${EMBEDDING_TENSOR_PARALLEL_SIZE:-1}"
GPU_MEM="${EMBEDDING_GPU_MEMORY_UTILIZATION:-0.35}"
CPU_OFFLOAD_GB="${EMBEDDING_CPU_OFFLOAD_GB:-0}"
SWAP_SPACE_GB="${EMBEDDING_SWAP_SPACE_GB:-4}"
KV_CACHE_MEMORY_BYTES="${EMBEDDING_KV_CACHE_MEMORY_BYTES:-}"
CPU_KVCACHE_SPACE="${EMBEDDING_CPU_KVCACHE_SPACE:-0}"
RUNNER="${EMBEDDING_RUNNER:-pooling}"
TASK="${EMBEDDING_TASK:-embed}"
CONVERT="${EMBEDDING_CONVERT:-}"
TRUST_REMOTE_CODE="${EMBEDDING_TRUST_REMOTE_CODE:-false}"

echo "Starting embedding vLLM with:"
echo "  model: $MODEL_NAME"
echo "  model path: $MODEL_PATH"
echo "  served model name: $SERVED_MODEL_NAME"
echo "  host: $HOST"
echo "  port: $PORT"
echo "  device: $DEVICE"
echo "  tensor parallel size: $TP"
echo "  max model len: $MAX_LEN"
echo "  runner: $RUNNER"
echo "  task: $TASK"
if [ -n "$DTYPE" ]; then
  echo "  dtype: $DTYPE"
else
  echo "  dtype: auto"
fi
if [ -n "$CONVERT" ]; then
  echo "  convert: $CONVERT"
else
  echo "  convert: auto"
fi
if [ "$DEVICE" = "cpu" ]; then
  echo "  cpu kv cache space: $CPU_KVCACHE_SPACE"
else
  echo "  cpu offload gb: $CPU_OFFLOAD_GB"
  echo "  swap space gb: $SWAP_SPACE_GB"
  if [ -n "$KV_CACHE_MEMORY_BYTES" ]; then
    echo "  kv cache memory bytes: $KV_CACHE_MEMORY_BYTES"
  else
    echo "  kv cache memory bytes: auto"
  fi
fi

set -- vllm serve "$MODEL_PATH" \
  --host "$HOST" \
  --port "$PORT" \
  --served-model-name "$SERVED_MODEL_NAME" \
  --tensor-parallel-size "$TP" \
  --max-model-len "$MAX_LEN"

if [ -n "$RUNNER" ]; then
  set -- "$@" --runner "$RUNNER"
fi

if vllm serve --help 2>&1 | grep -q -- '--task'; then
  set -- "$@" --task "$TASK"
fi

if [ -n "$CONVERT" ] && vllm serve --help 2>&1 | grep -q -- '--convert'; then
  set -- "$@" --convert "$CONVERT"
fi

if [ -n "$DTYPE" ]; then
  set -- "$@" --dtype "$DTYPE"
fi

if [ "$TRUST_REMOTE_CODE" = "true" ]; then
  set -- "$@" --trust-remote-code
fi

if [ "$DEVICE" = "cpu" ]; then
  :
else
  set -- "$@" --cpu-offload-gb "$CPU_OFFLOAD_GB"

  if [ -n "$KV_CACHE_MEMORY_BYTES" ]; then
    set -- "$@" --kv-cache-memory-bytes "$KV_CACHE_MEMORY_BYTES"
  else
    set -- "$@" --gpu-memory-utilization "$GPU_MEM"
  fi

  if vllm serve --help 2>&1 | grep -q -- '--swap-space'; then
    set -- "$@" --swap-space "$SWAP_SPACE_GB"
  else
    echo "warning: installed vLLM does not support --swap-space; skipping EMBEDDING_SWAP_SPACE_GB=$SWAP_SPACE_GB" >&2
  fi
fi

exec "$@"
