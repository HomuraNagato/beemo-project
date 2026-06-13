#!/usr/bin/env bash
set -euo pipefail

usage() {
  printf '%s\n' "usage: $0 [gpu|cpu] [--db|--no-db] [--no-voice] [--restart-orchestrator]"
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ACCEL="${BEEMO_ACCEL:-gpu}"
DB_MODE="${BEEMO_DB:-external}"
VOICE="${BEEMO_VOICE:-1}"
RESTART_ORCH="${BEEMO_RESTART_ORCH:-0}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    gpu|cpu)
      ACCEL="$1"
      ;;
    --db|--local-db)
      DB_MODE="local"
      ;;
    --no-db|--external-db)
      DB_MODE="external"
      ;;
    --no-voice)
      VOICE="0"
      ;;
    --restart-orchestrator)
      RESTART_ORCH="1"
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

compose_files=(-f docker-compose.yaml -f "docker-compose.${ACCEL}.yaml")
if [ "$DB_MODE" = "local" ]; then
  compose_files+=(-f docker-compose.pensieve.yaml)
fi

compose() {
  docker compose "${compose_files[@]}" "$@"
}

wait_http() {
  name="$1"
  url="$2"
  container="${3:-}"
  timeout="${4:-240}"
  start="$(date +%s)"
  while true; do
    if curl -fsS "$url" >/dev/null 2>&1; then
      printf '%s is ready\n' "$name"
      return 0
    fi
    if [ -n "$container" ] && ! docker ps --filter "name=^/${container}$" --filter "status=running" --format '{{.Names}}' | grep -qx "$container"; then
      printf '%s container is not running\n' "$name" >&2
      docker logs --tail 80 "$container" >&2 || true
      return 1
    fi
    now="$(date +%s)"
    if [ $((now - start)) -ge "$timeout" ]; then
      printf 'timed out waiting for %s at %s\n' "$name" "$url" >&2
      if [ -n "$container" ]; then
        docker logs --tail 80 "$container" >&2 || true
      fi
      return 1
    fi
    sleep 2
  done
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

sql_identifier() {
  value="${1//\"/\"\"}"
  printf '"%s"' "$value"
}

db_url_parts() {
  database_url="$1"
  python3 - "$database_url" <<'PY'
import sys
from urllib.parse import urlsplit, urlunsplit

raw = sys.argv[1].strip()
if not raw:
    raise SystemExit(1)

parts = urlsplit(raw)
db_name = parts.path.lstrip("/") or "postgres"
admin = urlunsplit((parts.scheme, parts.netloc, "/postgres", parts.query, parts.fragment))
print(db_name)
print(admin)
print(raw)
PY
}

bootstrap_db_external() {
  database_url="$1"
  migrations_dir="$2"
  mapfile -t parts < <(db_url_parts "$database_url")
  db_name="${parts[0]}"
  admin_url="${parts[1]}"
  target_url="${parts[2]}"

  printf 'Ensuring Postgres database and tables exist: %s\n' "$db_name"
  docker run --rm \
    --add-host host.docker.internal:host-gateway \
    -v "$ROOT_DIR/$migrations_dir:/migrations:ro" \
    pgvector/pgvector:pg16 \
    sh -eu -c '
      db_name="$1"
      admin_url="$2"
      target_url="$3"
      db_name_lit=$(printf "%s" "$db_name" | sed "s/'\''/'\'''\''/g; s/^/'\''/; s/$/'\''/")
      db_name_ident=$(printf "%s" "$db_name" | sed "s/\"/\"\"/g; s/^/\"/; s/$/\"/")
      deadline=$(( $(date +%s) + 120 ))
      until psql "$admin_url" -v ON_ERROR_STOP=1 -Atc "SELECT 1" >/dev/null 2>&1; do
        if [ "$(date +%s)" -ge "$deadline" ]; then
          echo "timed out waiting for Postgres admin connection" >&2
          exit 1
        fi
        sleep 1
      done
      if ! psql "$admin_url" -v ON_ERROR_STOP=1 -Atc "SELECT 1 FROM pg_database WHERE datname = $db_name_lit" | grep -qx 1; then
        psql "$admin_url" -v ON_ERROR_STOP=1 -c "CREATE DATABASE $db_name_ident"
      fi
      psql "$target_url" -v ON_ERROR_STOP=1 -c "CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())"
      for migration in /migrations/*.sql; do
        [ -e "$migration" ] || continue
        name="$(basename "$migration")"
        name_lit=$(printf "%s" "$name" | sed "s/'\''/'\'''\''/g; s/^/'\''/; s/$/'\''/")
        if ! psql "$target_url" -v ON_ERROR_STOP=1 -Atc "SELECT 1 FROM schema_migrations WHERE name = $name_lit" | grep -qx 1; then
          psql "$target_url" -v ON_ERROR_STOP=1 -f "$migration"
          psql "$target_url" -v ON_ERROR_STOP=1 -c "INSERT INTO schema_migrations (name, applied_at) VALUES ($name_lit, NOW()) ON CONFLICT (name) DO NOTHING"
        fi
      done
    ' sh "$db_name" "$admin_url" "$target_url"
}

bootstrap_db_local() {
  migrations_dir="$1"
  db_user="$(env_value PENSIEVE_POSTGRES_USER)"
  db_name="$(env_value PENSIEVE_POSTGRES_DB)"
  db_user="${db_user:-postgres}"
  db_name="${db_name:-beemo}"

  printf 'Ensuring local pensieve tables exist: %s\n' "$db_name"
  deadline=$(( $(date +%s) + 120 ))
  until compose exec -T pensieve pg_isready -U "$db_user" -d "$db_name" >/dev/null 2>&1; do
    if [ "$(date +%s)" -ge "$deadline" ]; then
      printf 'timed out waiting for pensieve database %s\n' "$db_name" >&2
      return 1
    fi
    sleep 1
  done
  compose exec -T pensieve psql -U "$db_user" -d "$db_name" -v ON_ERROR_STOP=1 \
    -c "CREATE TABLE IF NOT EXISTS schema_migrations (name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW())"
  for migration in "$ROOT_DIR/$migrations_dir"/*.sql; do
    [ -e "$migration" ] || continue
    name="$(basename "$migration")"
    quoted_name="$(sql_literal "$name")"
    if ! compose exec -T pensieve psql -U "$db_user" -d "$db_name" -v ON_ERROR_STOP=1 -Atc "SELECT 1 FROM schema_migrations WHERE name = $quoted_name" | grep -qx 1; then
      compose exec -T pensieve psql -U "$db_user" -d "$db_name" -v ON_ERROR_STOP=1 < "$migration"
      compose exec -T pensieve psql -U "$db_user" -d "$db_name" -v ON_ERROR_STOP=1 \
        -c "INSERT INTO schema_migrations (name, applied_at) VALUES ($quoted_name, NOW()) ON CONFLICT (name) DO NOTHING"
    fi
  done
}

cd "$ROOT_DIR"

printf 'Starting Beemo services using %s compose stack\n' "$ACCEL"
if [ "$DB_MODE" = "local" ]; then
  printf 'Including local Postgres service: pensieve\n'
else
  printf 'Using external Postgres from DATABASE_URL\n'
fi

if [ "$DB_MODE" = "local" ]; then
  compose up -d --build pensieve
  printf 'Waiting briefly for local Postgres container\n'
  sleep 3
fi

database_url="$(env_value DATABASE_URL)"
migrations_dir="$(env_value DB_MIGRATIONS_DIR)"
migrations_dir="${migrations_dir:-db/migrations}"
if [ -n "$database_url" ]; then
  if [ "$DB_MODE" = "local" ]; then
    bootstrap_db_local "$migrations_dir"
  else
    bootstrap_db_external "$database_url" "$migrations_dir"
  fi
fi

compose up -d --build eve-vllm
wait_http "eve-vllm" "http://127.0.0.1:5014/health" "eve-vllm" 300

compose up -d --build eve-embedding
wait_http "eve-embedding" "http://127.0.0.1:5021/health" "eve-embedding" 300

if [ "$VOICE" = "1" ]; then
  compose up -d --build eve-asr
fi

if [ "$RESTART_ORCH" = "1" ]; then
  compose rm -sf eve-orchestrator >/dev/null 2>&1 || true
fi

compose up -d --build --force-recreate eve-orchestrator

if [ "$VOICE" = "1" ]; then
  compose up -d --build eve-wakeword
fi

printf 'Beemo startup command complete\n'
printf 'Try: ./scripts/eve-orchestrator.sh "what time is it?"\n'
