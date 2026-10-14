#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
MODEL_NAME="kokoro-int8-en-v0_19"
MODEL_DIR="$ROOT_DIR/models/$MODEL_NAME"
MODEL_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/$MODEL_NAME.tar.bz2"
ARCHIVE="$ROOT_DIR/models/$MODEL_NAME.tar.bz2.part"

if [ -f "$MODEL_DIR/model.int8.onnx" ] && [ "${1:-}" != "--force" ]; then
  printf 'Kokoro model already exists: models/%s\n' "$MODEL_NAME"
  exit 0
fi

mkdir -p "$ROOT_DIR/models"
printf 'Downloading %s\n' "$MODEL_URL"
curl -fL --retry 3 -o "$ARCHIVE" "$MODEL_URL"
if [ "${1:-}" = "--force" ]; then
  rm -rf "$MODEL_DIR"
fi
tar -xjf "$ARCHIVE" -C "$ROOT_DIR/models"
rm -f "$ARCHIVE"
test -f "$MODEL_DIR/model.int8.onnx"
printf 'Kokoro model ready: models/%s\n' "$MODEL_NAME"
