#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$ROOT_DIR"

CONFIG="${CONFIG:-config.yaml}"
BACKEND_ADDR="${BACKEND_ADDR:-127.0.0.1:8080}"
FRONTEND_HOST="${FRONTEND_HOST:-127.0.0.1}"
FRONTEND_PORT="${FRONTEND_PORT:-5173}"

BACKEND_PID=""
FRONTEND_PID=""

is_windows_bash() {
  case "$(uname -s 2>/dev/null || echo unknown)" in
    MINGW*|MSYS*|CYGWIN*) return 0 ;;
    *) return 1 ;;
  esac
}

cleanup() {
  if [[ -n "$FRONTEND_PID" ]] && kill -0 "$FRONTEND_PID" 2>/dev/null; then
    kill "$FRONTEND_PID" 2>/dev/null || true
  fi
  if [[ -n "$BACKEND_PID" ]] && kill -0 "$BACKEND_PID" 2>/dev/null; then
    kill "$BACKEND_PID" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

require_cmd go
require_cmd npm

if [[ ! -f "$CONFIG" ]]; then
  if [[ "$CONFIG" == "config.yaml" && -f config.example.yaml ]]; then
    cp config.example.yaml config.yaml
    echo "created config.yaml from config.example.yaml"
  else
    echo "config file not found: $CONFIG" >&2
    exit 1
  fi
fi

if [[ ! -d web/node_modules ]]; then
  echo "installing frontend dependencies..."
  (cd web && npm install)
fi

echo "starting API on ${BACKEND_ADDR} using ${CONFIG}"
if is_windows_bash && command -v cmd.exe >/dev/null 2>&1 && command -v cygpath >/dev/null 2>&1; then
  ROOT_WIN="$(cygpath -w "$ROOT_DIR")"
  if [[ "$CONFIG" = /* ]]; then
    CONFIG_WIN="$(cygpath -w "$CONFIG")"
  else
    CONFIG_WIN="$CONFIG"
  fi
  cmd.exe /c "cd /d \"$ROOT_WIN\" && set GO111MODULE=on&& go run ./cmd/screener -config \"$CONFIG_WIN\"" &
else
  GO111MODULE=on go run ./cmd/screener -config "$CONFIG" &
fi
BACKEND_PID="$!"

echo "starting frontend on http://${FRONTEND_HOST}:${FRONTEND_PORT}"
(
  cd web
  VITE_API_TARGET="http://${BACKEND_ADDR}" npm run dev -- --host "$FRONTEND_HOST" --port "$FRONTEND_PORT"
) &
FRONTEND_PID="$!"

echo
echo "Dev server is starting:"
echo "  Frontend: http://${FRONTEND_HOST}:${FRONTEND_PORT}/"
echo "  API:      http://${BACKEND_ADDR}/api/health"
echo
echo "Press Ctrl+C to stop both processes."

if wait -n "$BACKEND_PID" "$FRONTEND_PID" 2>/dev/null; then
  exit 0
fi

wait "$BACKEND_PID" "$FRONTEND_PID"
