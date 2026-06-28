#!/usr/bin/env bash
#
# dev.sh — one-command local stack for Orka, with the environment guards that
# were learned the hard way:
#
#   * sources the PERSISTENT repo .env (never a /tmp copy)
#   * refuses to run with workspace storage under /tmp (macOS purges /tmp, which
#     silently eats user files after ~3 days)
#   * brings up the docker deps (mongo + redis, tools + gui), builds the control
#     plane, starts it, and health-checks the model endpoint
#
# Usage:
#   scripts/dev.sh            # bring up everything + start control
#   scripts/dev.sh deps       # only the docker dependencies
#   scripts/dev.sh control    # only (re)build + restart the control plane
#   scripts/dev.sh stop       # stop the control plane
#
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"
RUN_DIR="$ROOT/.run"            # gitignored: pid + logs (NOT under /tmp)
mkdir -p "$RUN_DIR"
LOG="$RUN_DIR/control.log"

say()  { printf '\033[1;36m▸ %s\033[0m\n' "$*"; }
warn() { printf '\033[1;33m! %s\033[0m\n' "$*"; }
die()  { printf '\033[1;31m✗ %s\033[0m\n' "$*" >&2; exit 1; }

load_env() {
  [ -f .env ] || { cp .env.example .env; warn ".env created from .env.example — set OPENAI_API_KEY then re-run"; exit 1; }
  set -a; . "$ROOT/.env"; set +a   # the persistent repo .env — the single source of truth

  # --- storage guard: NEVER /tmp ---
  : "${BASE_STORAGE_PATH:=./data/storage}"
  case "$BASE_STORAGE_PATH" in
    /tmp/*|/private/tmp/*|/var/folders/*)
      die "BASE_STORAGE_PATH=$BASE_STORAGE_PATH is under a temp dir the OS purges. Set it to a persistent path (e.g. \$HOME/.orka/storage) in .env." ;;
  esac
  mkdir -p "$BASE_STORAGE_PATH"
  say "storage: $BASE_STORAGE_PATH (persistent)"

  # --- quant harness interpreter (optional) ---
  if [ -n "${ORKA_QUANT_PYTHON:-}" ] && [ ! -x "$ORKA_QUANT_PYTHON" ]; then
    warn "ORKA_QUANT_PYTHON=$ORKA_QUANT_PYTHON not found — backtests fall back to synthetic data until you create the venv"
  fi
}

deps() {
  say "starting docker deps (mongo + redis)…"
  docker compose up -d
  if [ -f docker-compose.tools.yml ]; then
    say "starting tools + gui…"
    docker compose -f docker-compose.tools.yml up -d || warn "tools/gui compose failed (optional)"
  fi
  # mongo data lives in the orka_mongo_data docker volume → persistent across restarts.
}

control() {
  say "building control plane…"
  go build -o "$RUN_DIR/orka_control" ./orka_control_layer
  pkill -f "$RUN_DIR/orka_control" 2>/dev/null || true
  sleep 1
  say "starting control plane (logs → $LOG)…"
  nohup "$RUN_DIR/orka_control" >"$LOG" 2>&1 &
  echo $! >"$RUN_DIR/control.pid"
  sleep 3
  local addr="${CONTROL_ADDR:-:8088}"; local port="${addr##*:}"
  if ! lsof -nP -iTCP:"$port" -sTCP:LISTEN >/dev/null 2>&1; then
    tail -n 20 "$LOG" >&2; die "control plane did not come up on :$port"
  fi
  say "control listening on :$port"
  health_check
}

health_check() {
  [ -n "${OPENAI_API_KEY:-}" ] || { warn "OPENAI_API_KEY unset — skipping model check"; return; }
  local code
  code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 \
    -X POST "${OPENAI_BASE_URL:-https://api.deepseek.com}/chat/completions" \
    -H "Authorization: Bearer $OPENAI_API_KEY" -H 'Content-Type: application/json' \
    -d "{\"model\":\"${MODEL:-deepseek-chat}\",\"messages\":[{\"role\":\"user\",\"content\":\"ok\"}],\"max_tokens\":1}" || echo 000)
  [ "$code" = "200" ] && say "model OK ($MODEL @ $OPENAI_BASE_URL)" || warn "model check returned HTTP $code — verify OPENAI_API_KEY / OPENAI_BASE_URL / MODEL in .env"
}

stop() {
  pkill -f "$RUN_DIR/orka_control" 2>/dev/null && say "control stopped" || warn "control not running"
}

case "${1:-all}" in
  deps)    load_env; deps ;;
  control) load_env; control ;;
  stop)    stop ;;
  all)
    load_env; deps; control
    echo
    say "stack up. next: 'make run-web' (vite dev server) — or it's already running."
    ;;
  *) die "unknown command: $1 (use: all | deps | control | stop)" ;;
esac
