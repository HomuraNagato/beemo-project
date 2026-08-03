#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: beemo-init.sh [gpu|cpu] [--models|--no-models] [--force-download]

First-run Beemo setup:
  - downloads Hugging Face model directories required by config/config.yaml

Defaults:
  accelerator: cpu
  models:      enabled

Examples:
  ./scripts/beemo-init.sh cpu
  ./scripts/beemo-init.sh cpu --force-download
USAGE
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ACCEL="cpu"
INIT_MODELS=1
FORCE_DOWNLOAD=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    gpu|cpu)
      ACCEL="$1"
      ;;
    --models)
      INIT_MODELS=1
      ;;
    --no-models)
      INIT_MODELS=0
      ;;
    --force-download)
      FORCE_DOWNLOAD=1
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

cd "$ROOT_DIR"

config_value() {
  section="$1"
  key="$2"
  python3 - "$ROOT_DIR/config/config.yaml" "$section" "$key" <<'PY'
import sys

path, section, key = sys.argv[1:]
current = None
with open(path, "r", encoding="utf-8") as handle:
    for raw_line in handle:
        line = raw_line.rstrip()
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if not raw_line.startswith((" ", "\t")) and stripped.endswith(":"):
            current = stripped[:-1]
            continue
        if current == section and stripped.startswith(key + ":"):
            value = stripped.split(":", 1)[1].strip().strip("'\"")
            print(value)
            break
PY
}

model_repo() {
  model="$1"
  case "$model" in
    */*) printf '%s\n' "$model" ;;
    Qwen*) printf 'Qwen/%s\n' "$model" ;;
    *)
      printf 'cannot infer Hugging Face repo for model name %q; set it as namespace/repo in config/config.yaml or download it manually\n' "$model" >&2
      return 1
      ;;
  esac
}

model_dir_name() {
  model="$1"
  printf '%s\n' "${model##*/}"
}

download_model() {
  label="$1"
  model="$2"
  image="$3"

  if [ -z "$model" ]; then
    printf 'Skipping %s model: env value is empty\n' "$label"
    return
  fi

  repo="$(model_repo "$model")"
  dir_name="$(model_dir_name "$model")"
  target="$ROOT_DIR/models/$dir_name"

  if [ "$FORCE_DOWNLOAD" -ne 1 ] && [ -f "$target/config.json" ] && find "$target" -maxdepth 1 \( -name '*.safetensors' -o -name '*.bin' -o -name '*.gguf' \) | grep -q .; then
    printf '%s model already exists: models/%s\n' "$label" "$dir_name"
    return
  fi

  mkdir -p "$ROOT_DIR/models"
  printf 'Downloading %s model %s -> models/%s\n' "$label" "$repo" "$dir_name"
  docker run --rm -i \
    --entrypoint python \
    -v "$ROOT_DIR/models:/models" \
    "$image" \
    - "$repo" "/models/$dir_name" <<'PY'
import sys
from huggingface_hub import snapshot_download

repo_id = sys.argv[1]
local_dir = sys.argv[2]
snapshot_download(repo_id=repo_id, local_dir=local_dir)
PY
}

init_models() {
  case "$ACCEL" in
    cpu) vllm_image="docker.io/vllm/vllm-openai-cpu:latest-x86_64" ;;
    gpu) vllm_image="docker.io/vllm/vllm-openai" ;;
  esac

  download_model "reasoning" "$(config_value llm model)" "$vllm_image"
  download_model "embedding" "$(config_value embedding model)" "$vllm_image"
}

printf 'Initializing Beemo using %s stack\n' "$ACCEL"
if [ "$INIT_MODELS" -eq 1 ]; then
  init_models
fi

printf 'Beemo init complete\n'
printf 'Next: ./scripts/beemo-start.sh vllm-%s --no-voice\n' "$ACCEL"
