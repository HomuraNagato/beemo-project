#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: hf-download.sh <owner/model> [--quant Q4_K_M] [--file FILE] [--force]

Thin wrapper around `hf download` for llama.cpp GGUF files.

Examples:
  ./scripts/hf-download.sh Qwen/Qwen2.5-1.5B-Instruct-GGUF
  ./scripts/hf-download.sh Qwen/Qwen2.5-1.5B-Instruct-GGUF --file qwen2.5-1.5b-instruct-q4_k_m.gguf

Defaults:
  quant: Q4_K_M

The downloaded file is symlinked to:
  models/<model-name>.<quant>.gguf

That stable name is what docker-compose.cpu.llamacpp.yaml can point at.
USAGE
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODEL_NAME="${1:-}"
QUANT="${GGUF_QUANT:-Q4_K_M}"
REMOTE_FILE=""
FORCE=0

case "$MODEL_NAME" in
  -h|--help)
    usage
    exit 0
    ;;
esac

if [ -z "$MODEL_NAME" ]; then
  usage >&2
  exit 2
fi
shift

while [ "$#" -gt 0 ]; do
  case "$1" in
    --quant)
      QUANT="${2:-}"
      if [ -z "$QUANT" ]; then
        printf 'missing value for --quant\n' >&2
        exit 2
      fi
      shift 2
      ;;
    --file)
      REMOTE_FILE="${2:-}"
      if [ -z "$REMOTE_FILE" ]; then
        printf 'missing value for --file\n' >&2
        exit 2
      fi
      shift 2
      ;;
    --force)
      FORCE=1
      shift
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
done

have_cmd() {
  command -v "$1" >/dev/null 2>&1
}

hf_cmd() {
  if have_cmd hf; then
    hf "$@"
    return
  fi
  if [ -x "$HOME/.local/bin/hf" ]; then
    "$HOME/.local/bin/hf" "$@"
    return
  fi
  printf 'hf CLI not found. Run ../scripts/install-beemo-host-deps.sh or install huggingface_hub.\n' >&2
  exit 1
}

lower() {
  printf '%s' "$1" | tr '[:upper:]' '[:lower:]'
}

local_name_for_model() {
  printf '%s\n' "${1##*/}"
}

cd "$ROOT_DIR"
mkdir -p models

case "$MODEL_NAME" in
  */*) ;;
  *)
    printf 'expected Hugging Face repo id in owner/model form, got %q\n' "$MODEL_NAME" >&2
    printf 'example: ./scripts/hf-download.sh Qwen/Qwen2.5-1.5B-Instruct-GGUF\n' >&2
    exit 2
    ;;
esac

REPO="$MODEL_NAME"
LOCAL_MODEL="$(local_name_for_model "$MODEL_NAME")"
LOCAL_MODEL="${LOCAL_MODEL%-GGUF}"
if [ -z "$REMOTE_FILE" ]; then
  REMOTE_FILE="$(lower "$LOCAL_MODEL")-$(lower "$QUANT").gguf"
fi
STABLE_FILE="${LOCAL_MODEL}.${QUANT}.gguf"
TARGET="models/$STABLE_FILE"

if [ "$FORCE" -ne 1 ] && [ -f "$TARGET" ]; then
  printf 'GGUF already exists: %s\n' "$TARGET"
  exit 0
fi

printf 'Downloading %s from %s\n' "$REMOTE_FILE" "$REPO"
hf_cmd download "$REPO" "$REMOTE_FILE" --local-dir models

if [ ! -f "models/$REMOTE_FILE" ]; then
  printf 'expected downloaded file at models/%s\n' "$REMOTE_FILE" >&2
  printf 'if the repo uses a different filename, run: hf ls %s\n' "$REPO" >&2
  printf 'then pass it with: ./scripts/hf-download.sh %s --file FILE\n' "$MODEL_NAME" >&2
  exit 1
fi

if [ "$REMOTE_FILE" != "$STABLE_FILE" ]; then
  ln -sf "$REMOTE_FILE" "$TARGET"
fi

printf 'Ready: %s\n' "$TARGET"
