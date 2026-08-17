# Orka (OSS replica)

A runnable open-source replica of the Orka enterprise AI Agent platform:
control plane + tool plane + execution plane, driven by a single Eino (ADK)
agent runtime, with MCP tools, SSE streaming, Clarify interrupt + checkpoint
resume, a Playwright/CDP GUI agent, a React workspace UI, and an end-to-end
research-report → quant-factor pipeline on top.

## Layout

| Component | Role |
|--------|------|
| `orka_core` | protocol + SDK: messages, state, checkpoint, agent runtime, ws, trace, security |
| `orka_middleware` | tool-abstraction layer: ToolsManager + multi-transport MCP client |
| `tools_server` | MCP gateway service: multi-source tool aggregation + routing |
| `orka_control_layer` | control plane: API, SSE chat, Eino runtime, persistence, quant pipeline |
| `web` | React + Vite workspace UI: chat thread, execution timeline, artifacts, factor library |
| `gui_agent` | Python CDP GUI executor (independent); plans with UI-TARS (`GUI_PLANNER=uitars`) or a DOM-first rule/SoM-VLM fallback |

(`orka_core / orka_middleware / tools_server / orka_control_layer` are the four
`go.work` modules; `web` is a Vite app; `gui_agent` is a Python service.)

## What's inside

- **Agent runtime** — one Eino/ADK ChatModelAgent orchestrates sub-agents
  (researcher / writer / browser / engineer + the quant workers) as tools;
  streaming tokens + reasoning, structured `plan` events, and a live execution
  timeline in the UI.
- **Tools** — filesystem, web search/fetch, HTTP, code (`python`/`shell`), office
  & data (docx/pdf/csv/sql/chart), a GUI browser tool, plus artifact publishing.
- **Human-in-the-loop** — `clarify` (pause & ask) and a danger-tool confirm gate
  with allow-once / always-this-session / deny.
- **Automation** — reusable Workflow DAGs, cron-scheduled tasks + webhooks, and a
  Runs/Mission-Control view with run-to-run diff.
- **Artifacts** — live, shareable pages (chart / mermaid / sandboxed HTML).
- **Quant factor pipeline** — turns the natural-language investment logic in
  research reports (PDF/HTML/MD) into backtestable factors: parse → double-blind
  extract → GP-evolve + backtest → schema-validate → human review → factor
  library. Real A-share data via akshare (synthetic fallback). See below.

## Quick start

```bash
make up        # start redis + mongo
make build     # go work sync && go build ./...
make test      # go test ./...
make run-control
make run-tools
```

Config lives in `config.yaml`; every key is overridable via env (`.env.example`).

### One command (recommended)

```bash
cp .env.example .env      # then set OPENAI_API_KEY
scripts/dev.sh            # deps (docker) + build + run control, with health checks
#   scripts/dev.sh deps      # only the docker dependencies
#   scripts/dev.sh control   # only (re)build + restart the control plane
#   scripts/dev.sh stop      # stop the control plane
make run-web              # the Vite dev server (separate terminal)
```

`scripts/dev.sh` sources the **persistent repo `.env`** and starts the control
plane from it — never a `/tmp` copy of the env or binary.

### Environment must be persistent (don't use `/tmp`)

> ⚠️ **Never point `BASE_STORAGE_PATH` (or the `.env` / launch artifacts) at `/tmp`.**
> macOS purges `/tmp` (files untouched for ~3 days are deleted), which silently
> destroys user workspace files. `scripts/dev.sh` refuses to start if
> `BASE_STORAGE_PATH` is under `/tmp`, `/private/tmp`, or `/var/folders`.

Persistence conventions:

| What | Persistent location | Notes |
| --- | --- | --- |
| Workspace files | `BASE_STORAGE_PATH` → `./data/storage` or `$HOME/.orka/storage` | the per-user file root; **must not be `/tmp`** |
| Mongo data | docker volume `orka_mongo_data` (via `docker compose`) | survives container restarts; don't run a second native `mongod` on 27017 (it shadows the container) |
| Config / secrets | the repo `.env` (gitignored) | the single source of truth the launcher reads |
| Quant harness deps | `ORKA_QUANT_PYTHON` → a venv with `akshare` (`.venv-quant`) | keeps akshare out of system Python; falls back to synthetic data if unset |

## Running the full stack

The LLM is any OpenAI-compatible endpoint (defaults to DeepSeek in `config.yaml`).
Provide the key via env — never commit it.

```bash
export OPENAI_API_KEY=sk-...        # your DeepSeek/OpenAI/vLLM key
export CTX_TOKEN_SECRET=devsecret   # shared by control layer + tools_server

# 1) tool gateway (MCP over streamable HTTP at /api/v1/tools/mcp)
TOOLS_ADDR=:8090 make run-tools

# 2) GUI executor (FastAPI WS; launches headless Chromium itself, or set CDP_URL)
cd gui_agent && . .venv/bin/activate && python -m playwright install chromium
GUI_PORT=8100 python main.py

# 3) control plane (chat SSE), wired to the gateway + GUI agent
CONTROL_ADDR=:8088 \
  TOOLS_MCP_URL=http://127.0.0.1:8090/api/v1/tools/mcp \
  GUI_AGENT_WS_URL=ws://127.0.0.1:8100/api/v1/exec/gui/ws \
  make run-control
```

### Try it

```bash
B=http://127.0.0.1:8088/api/v1/controller
# tool call (file_write via the MCP gateway, RBAC-scoped, per-user storage)
curl -sN -X POST $B/chat/run -d '{"message":"write report.txt with: hi","user_email":"u@x.com","enabled_tools":["file"]}'
# GUI task (browser automation; browser events stream into SSE)
curl -sN -X POST $B/chat/run -d '{"message":"open https://example.com and tell me the title","user_email":"u@x.com","enabled_tools":["gui_agent"]}'
# file API + chunked resumable upload
curl -s -H "X-User-Email: u@x.com" -F file=@./x.txt -F dir=docs $B/file/upload
curl -s $B/metrics
```

### Feature toggles (env)

| Env | Effect |
|-----|--------|
| `TOOLS_MCP_URL=` | source tools from a remote tools_server over MCP (else local fs tools) |
| `GUI_AGENT_WS_URL=` | wire the real `run_agent` GUI tool (else a mock) |
| `SCHEDULER_ENABLE=1` | run the cron scheduler (renders task templates → triggers chat) |
| `OTEL_TRACES_STDOUT=1` | export OpenTelemetry spans (chat.run / tool.invoke) to stdout |
| `OTEL_EXPORTER_OTLP_ENDPOINT=` | export spans to an OTLP/HTTP collector (Jaeger/Tempo) |
| `CDP_URL=` | (gui_agent) attach to a remote Chromium over CDP instead of launching one |
| `GUI_PLANNER=uitars` | (gui_agent) plan with a UI-TARS GUI-agent model: coordinate-grounded click/type/drag from raw screenshots (`UITARS_BASE_URL`/`UITARS_API_KEY`/`UITARS_MODEL`, e.g. vLLM serving `UI-TARS-1.5-7B`) |
| `VLM_ENABLE=1` | (gui_agent) use a generic multimodal model via Set-of-Marks as DOM-first fallback (legacy; superseded by `GUI_PLANNER=uitars`) |

### Eval (regression / reproducibility)

```bash
make eval   # replays recorded samples, asserts tool-sequence + final, checks adk==graph
```

## Quant factor pipeline

Turns research reports into backtestable, human-reviewed quant factors.

```bash
# 1) drop reports (PDF/HTML/MD) into the caller's workspace under reports/
#    (BASE_STORAGE_PATH/<user>/reports/…)
# 2) run the pipeline over every report (detached; one isolated Workflow run each)
curl -s -X POST $B/quant/pipeline/run  -H "Authorization: Bearer $TOK"
# 3) inspect / review the factor library
curl -s -X POST $B/quant/factors       -H "Authorization: Bearer $TOK"
curl -s -X POST $B/quant/factor/status -H "Authorization: Bearer $TOK" \
     -d '{"factor_id":"…","status":"approved"}'   # or rejected
```

Stages (a Workflow DAG, each step retried): `parse → propose_a / propose_b
(double-blind) → agreement gate → gp_evolve + backtest → validate → review →
ingest`. Factors land as `backtested` (pending) and are approved in the UI's
**因子 (factors)** panel; only approved factors feed weighted portfolios.

Backtest data comes from **akshare** (real A-share bars + valuation), cached on
disk per day; it falls back to a deterministic synthetic panel if akshare/network
is unavailable. Point `ORKA_QUANT_PYTHON` at a venv that has `akshare` installed
(see the persistence table above). Set `ORKA_BACKTEST_OFFLINE=1` to force the
synthetic panel (used by tests).

## Security model

- Path containment via `filepath.Rel` (pathsafe), per-user storage roots.
- CORS exact-host allowlist (not substring).
- Downstream trust requires a signed `ContextToken` (HMAC), not raw headers; the
  gateway applies RBAC scopes + a tool blacklist + ToolGroup isolation.
- Clarify checkpoints use version CAS + atomic claim (resume is idempotent) + TTL.
