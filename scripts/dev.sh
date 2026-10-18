#!/usr/bin/env bash
# Project-scoped Linux launcher. Does not stop untracked processes or call models.
# all: mongo/redis dependencies + local tools/gui/control/web
# control|tools|gui|web: start one component (already-running components are kept)
# stop [component]: stop only matching .run identity records; data/infra are kept
# status: report process identities and owned listening ports
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

load_env() {
  if [ ! -f "$ROOT/.env" ]; then
    printf '%s\n' 'Missing .env: copy .env.example and configure this project before starting.' >&2
    exit 1
  fi
  set -a
  . "$ROOT/.env"
  set +a
  BASE_STORAGE_PATH="${BASE_STORAGE_PATH:-./data/storage}"
  BASE_STORAGE_PATH="$(python3 - "$BASE_STORAGE_PATH" <<'PY'
import pathlib, sys
print(pathlib.Path(sys.argv[1]).expanduser().resolve())
PY
)"
  case "$BASE_STORAGE_PATH" in
    /tmp|/tmp/*|/private/tmp|/private/tmp/*|/var/folders|/var/folders/*)
      printf '%s\n' 'BASE_STORAGE_PATH must be persistent, not a temporary directory.' >&2
      exit 1 ;;
  esac
  # Export the exact addresses that the launcher verifies, so config.yaml cannot
  # silently make a process bind a different port than its lifecycle record.
  export CONTROL_ADDR="${CONTROL_ADDR:-:8088}"
  export TOOLS_ADDR="${TOOLS_ADDR:-:8090}"
  export CONTROL_URL="${CONTROL_URL:-http://127.0.0.1:${CONTROL_ADDR##*:}}"
  export BASE_STORAGE_PATH
  mkdir -p "$BASE_STORAGE_PATH"
}

deps() {
  # Compose retains its existing project/volume identity. Never use down -v.
  # Tools and GUI are managed locally by stack.py, not a second overlapping stack.
  docker compose --project-directory "$ROOT" -f "$ROOT/docker-compose.yml" up -d mongo redis
}

case "${1:-all}" in
  stop|status) exec python3 "$ROOT/scripts/stack.py" "$@" ;;
  deps) load_env; deps ;;
  all) load_env; deps; exec python3 "$ROOT/scripts/stack.py" all ;;
  control|tools|gui|web) load_env; exec python3 "$ROOT/scripts/stack.py" "$@" ;;
  *) printf '%s\n' 'Use: all | deps | control | tools | gui | web | status | stop [component]' >&2; exit 2 ;;
esac
