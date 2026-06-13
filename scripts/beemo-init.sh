#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: beemo-init.sh [gpu|cpu] [--db|--no-db] [--models|--no-models] [--force-download]

First-run Beemo setup:
  - starts the local pensieve Postgres container and applies db/migrations
  - downloads Hugging Face model directories required by .env

Defaults:
  accelerator: cpu
  db:          enabled
  models:      enabled

Examples:
  ./scripts/beemo-init.sh cpu
  ./scripts/beemo-init.sh cpu --force-download
  ./scripts/beemo-init.sh cpu --no-db
USAGE
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ACCEL="${BEEMO_ACCEL:-cpu}"
INIT_DB=1
INIT_MODELS=1
FORCE_DOWNLOAD=0

while [ "$#" -gt 0 ]; do
  case "$1" in
    gpu|cpu)
      ACCEL="$1"
      ;;
    --db)
      INIT_DB=1
      ;;
    --no-db)
      INIT_DB=0
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

compose_files=(-f docker-compose.yaml -f "docker-compose.${ACCEL}.yaml" -f docker-compose.pensieve.yaml)

compose() {
  docker compose "${compose_files[@]}" "$@"
}

env_value() {
  name="$1"
  awk -v key="$name" '
    $0 ~ "^[[:space:]]*" key "=" {
      line = $0
      sub("^[[:space:]]*" key "=", "", line)
      sub(/[[:space:]]+#.*$/, "", line)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", line)
      print line
      exit
    }
  ' .env
}

sql_literal() {
  value="${1//\'/\'\'}"
  printf "'%s'" "$value"
}

model_repo() {
  model="$1"
  case "$model" in
    */*) printf '%s\n' "$model" ;;
    Qwen*) printf 'Qwen/%s\n' "$model" ;;
    *)
      printf 'cannot infer Hugging Face repo for model name %q; set the value as namespace/repo in .env or download it manually\n' "$model" >&2
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

init_db() {
  db_user="$(env_value PENSIEVE_POSTGRES_USER)"
  db_name="$(env_value PENSIEVE_POSTGRES_DB)"
  migrations_dir="$(env_value DB_MIGRATIONS_DIR)"
  db_user="${db_user:-postgres}"
  db_name="${db_name:-beemo}"
  migrations_dir="${migrations_dir:-db/migrations}"

  printf 'Starting local Postgres container: pensieve\n'
  compose up -d pensieve

  printf 'Waiting for Postgres database: %s\n' "$db_name"
  deadline=$(( $(date +%s) + 120 ))
  until compose exec -T pensieve pg_isready -U "$db_user" -d "$db_name" >/dev/null 2>&1; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      printf 'timed out waiting for pensieve database %s\n' "$db_name" >&2
      return 1
    fi
    sleep 1
  done

  printf 'Applying database migrations from %s\n' "$migrations_dir"
  compose exec -T pensieve psql -U "$db_user" -d "$db_name" -v ON_ERROR_STOP=1 \
    -c "CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())"

  for migration in "$ROOT_DIR/$migrations_dir"/*.sql; do
    [ -e "$migration" ] || continue
    name="$(basename "$migration")"
    quoted_name="$(sql_literal "$name")"
    if compose exec -T pensieve psql -U "$db_user" -d "$db_name" -v ON_ERROR_STOP=1 -Atc "SELECT 1 FROM schema_migrations WHERE name = $quoted_name" | grep -qx 1; then
      printf 'Migration already applied: %s\n' "$name"
      continue
    fi
    printf 'Applying migration: %s\n' "$name"
    compose exec -T pensieve psql -U "$db_user" -d "$db_name" -v ON_ERROR_STOP=1 < "$migration"
    compose exec -T pensieve psql -U "$db_user" -d "$db_name" -v ON_ERROR_STOP=1 \
      -c "INSERT INTO schema_migrations (name, applied_at) VALUES ($quoted_name, NOW()) ON CONFLICT (name) DO NOTHING"
  done
}

init_models() {
  case "$ACCEL" in
    cpu) vllm_image="vllm/vllm-openai-cpu:latest-x86_64" ;;
    gpu) vllm_image="vllm/vllm-openai" ;;
  esac

  download_model "reasoning" "$(env_value REASONING_MODEL)" "$vllm_image"
  download_model "embedding" "$(env_value EMBEDDING_MODEL)" "$vllm_image"
}

printf 'Initializing Beemo using %s stack\n' "$ACCEL"
if [ "$INIT_DB" -eq 1 ]; then
  init_db
fi
if [ "$INIT_MODELS" -eq 1 ]; then
  init_models
fi

printf 'Beemo init complete\n'
printf 'Next: ./scripts/beemo-start.sh %s --db --no-voice\n' "$ACCEL"
