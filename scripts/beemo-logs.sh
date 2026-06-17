#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'USAGE'
usage: beemo-logs.sh [service] [--tail N]

Default service: eve-orchestrator
USAGE
}

SERVICE="eve-orchestrator"
TAIL="200"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --tail)
      if [ "$#" -lt 2 ]; then
        printf 'missing value for --tail\n' >&2
        exit 2
      fi
      TAIL="$2"
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    --*)
      usage >&2
      printf 'unknown argument: %s\n' "$1" >&2
      exit 2
      ;;
    *)
      SERVICE="$1"
      ;;
  esac
  shift
done

exec docker logs -f --tail "$TAIL" "$SERVICE"
