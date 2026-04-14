#!/bin/sh
set -eu

MODEL_DIR="${MODEL_DIR:-/models}"
MODEL_NAME="${REASONING_MODEL:?REASONING_MODEL is required}"
MODEL_PATH="${REASONING_MODEL_PATH:-${MODEL_DIR}/${MODEL_NAME}}"
PORT="${REASONING_PORT:-5014}"
HOST="${REASONING_HOST:-0.0.0.0}"
DEVICE="${VLLM_DEVICE:-auto}"
DTYPE="${VLLM_DTYPE:-}"
GPU_MEM="${VLLM_GPU_MEMORY_UTILIZATION:-0.90}"
MAX_LEN="${VLLM_MAX_MODEL_LEN:-8192}"
TP="${VLLM_TENSOR_PARALLEL_SIZE:-1}"
CPU_OFFLOAD_GB="${VLLM_CPU_OFFLOAD_GB:-0}"
SWAP_SPACE_GB="${VLLM_SWAP_SPACE_GB:-4}"
KV_CACHE_MEMORY_BYTES="${VLLM_KV_CACHE_MEMORY_BYTES:-}"
CPU_KVCACHE_SPACE="${VLLM_CPU_KVCACHE_SPACE:-0}"
REASONING_PARSER="${VLLM_REASONING_PARSER:-}"
DEFAULT_CHAT_TEMPLATE_KWARGS="${VLLM_DEFAULT_CHAT_TEMPLATE_KWARGS:-}"

echo "Starting vLLM with:"
echo "  model: $MODEL_NAME"
echo "  model path: $MODEL_PATH"
echo "  host: $HOST"
echo "  port: $PORT"
echo "  device: $DEVICE"
echo "  tensor parallel size: $TP"
echo "  max model len: $MAX_LEN"
if [ -n "$DTYPE" ]; then
  echo "  dtype: $DTYPE"
else
  echo "  dtype: auto"
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
if [ -n "$REASONING_PARSER" ]; then
  echo "  reasoning parser: $REASONING_PARSER"
else
  echo "  reasoning parser: none"
fi
if [ -n "$DEFAULT_CHAT_TEMPLATE_KWARGS" ]; then
  echo "  default chat template kwargs: $DEFAULT_CHAT_TEMPLATE_KWARGS"
else
  echo "  default chat template kwargs: none"
fi

set -- vllm serve "$MODEL_PATH" \
  --host "$HOST" \
  --port "$PORT" \
  --served-model-name "$MODEL_NAME" \
  --tensor-parallel-size "$TP" \
  --max-model-len "$MAX_LEN"

if [ -n "$DTYPE" ]; then
  set -- "$@" --dtype "$DTYPE"
fi

if [ -n "$REASONING_PARSER" ]; then
  set -- "$@" --reasoning-parser "$REASONING_PARSER"
fi

if [ -n "$DEFAULT_CHAT_TEMPLATE_KWARGS" ]; then
  set -- "$@" --default-chat-template-kwargs "$DEFAULT_CHAT_TEMPLATE_KWARGS"
fi

if [ "$DEVICE" = "cpu" ]; then
  # CPU deployments use the dedicated CPU image and CPU-specific env vars
  # such as VLLM_CPU_KVCACHE_SPACE instead of GPU memory flags.
  :
else
  set -- "$@" --cpu-offload-gb "$CPU_OFFLOAD_GB"

  if [ -n "$KV_CACHE_MEMORY_BYTES" ]; then
    set -- "$@" --kv-cache-memory-bytes "$KV_CACHE_MEMORY_BYTES"
  else
    set -- "$@" --gpu-memory-utilization "$GPU_MEM"
  fi

  # vLLM's CLI has changed across releases. Newer builds do not accept
  # --swap-space, so only pass it when the installed binary advertises support.
  if vllm serve --help 2>&1 | grep -q -- '--swap-space'; then
    set -- "$@" --swap-space "$SWAP_SPACE_GB"
  else
    echo "warning: installed vLLM does not support --swap-space; skipping VLLM_SWAP_SPACE_GB=$SWAP_SPACE_GB" >&2
  fi
fi

exec "$@"
