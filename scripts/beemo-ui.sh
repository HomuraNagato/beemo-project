#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: scripts/beemo-ui.sh [--windowed] [--no-start] [--url URL]

Launch the Beemo browser UI as a fullscreen Linux app.

Options:
  --windowed   Open in a normal app window instead of fullscreen kiosk mode.
  --no-start   Do not start the eve-ui container first.
  --url URL    Open a custom UI URL. Defaults to http://127.0.0.1:5017/.
EOF
}

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
URL="${BEEMO_UI_URL:-http://127.0.0.1:5017/}"
START_UI="${BEEMO_UI_START:-1}"
WINDOWED="${BEEMO_UI_WINDOWED:-0}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --windowed)
      WINDOWED="1"
      ;;
    --no-start)
      START_UI="0"
      ;;
    --url)
      if [ "$#" -lt 2 ]; then
        printf 'missing value for --url\n' >&2
        exit 2
      fi
      URL="$2"
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
  shift
done

cd "$ROOT_DIR"

if [ "$START_UI" = "1" ]; then
  docker compose up -d eve-ui
fi

browser="${BEEMO_UI_BROWSER:-}"
if [ -z "$browser" ]; then
  for candidate in chromium chromium-browser google-chrome google-chrome-stable firefox; do
    if command -v "$candidate" >/dev/null 2>&1; then
      browser="$candidate"
      break
    fi
  done
fi

if [ -z "$browser" ]; then
  printf 'No supported browser found. Install Chromium, Chrome, or Firefox.\n' >&2
  exit 1
fi

case "$(basename "$browser")" in
  firefox)
    if [ "$WINDOWED" = "1" ]; then
      exec "$browser" --new-window "$URL"
    fi
    exec "$browser" --kiosk "$URL"
    ;;
  *)
    profile_dir="${BEEMO_UI_PROFILE_DIR:-$HOME/.local/share/beemo-ui/chromium-profile}"
    mkdir -p "$profile_dir"
    if [ "$WINDOWED" = "1" ]; then
      exec "$browser" \
        --user-data-dir="$profile_dir" \
        --app="$URL"
    fi
    exec "$browser" \
      --user-data-dir="$profile_dir" \
      --kiosk \
      --no-first-run \
      --disable-session-crashed-bubble \
      "$URL"
    ;;
esac
