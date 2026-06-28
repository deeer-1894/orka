# Orka (OSS replica)

A runnable open-source replica of the Orka enterprise AI Agent platform:
control plane + tool plane + execution plane, with an ADK-style middleware
runtime, MCP tools, SSE streaming, Clarify interrupt + checkpoint resume, and
a Playwright/CDP GUI agent.

## Layout (go.work multi-module)

| Module | Role |
|--------|------|
| `orka_core` | protocol + SDK: messages, state, checkpoint, agent runtime, ws, trace, security |
| `orka_middleware` | tool-abstraction layer: ToolsManager + multi-transport MCP client |
| `tools_server` | MCP gateway service: multi-source tool aggregation + routing |
| `orka_control_layer` | control plane: API, SSE chat, middleware pipeline, persistence |
| `gui_agent` | Python LangGraph/CDP GUI executor (independent); plans with UI-TARS (`GUI_PLANNER=uitars`) or a DOM-first rule/SoM-VLM fallback |

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
| `RUN_MODE=graph` | deterministic Graph runner (reproducible) instead of model-driven ADK |
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

## Security model

- Path containment via `filepath.Rel` (pathsafe), per-user storage roots.
- CORS exact-host allowlist (not substring).
- Downstream trust requires a signed `ContextToken` (HMAC), not raw headers; the
  gateway applies RBAC scopes + a tool blacklist + ToolGroup isolation.
- Clarify checkpoints use version CAS + atomic claim (resume is idempotent) + TTL.
