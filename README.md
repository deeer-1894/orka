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
| `gui_agent` | Python LangGraph/CDP GUI executor (independent) |

## Quick start

```bash
make up        # start redis + mongo
make build     # go work sync && go build ./...
make test      # go test ./...
make run-control
make run-tools
```

Config lives in `config.yaml`; every key is overridable via env (`.env.example`).

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
| `LLM_MOCK=1` | use the deterministic offline demo LLM (no API key needed) |
| `RUN_MODE=graph` | deterministic Graph runner (reproducible) instead of model-driven ADK |
| `TOOLS_MCP_URL=` | source tools from a remote tools_server over MCP (else local fs tools) |
| `GUI_AGENT_WS_URL=` | wire the real `run_agent` GUI tool (else a mock) |
| `SCHEDULER_ENABLE=1` | run the cron scheduler (renders task templates → triggers chat) |
| `OTEL_TRACES_STDOUT=1` | export OpenTelemetry spans (chat.run / tool.invoke) to stdout |
| `OTEL_EXPORTER_OTLP_ENDPOINT=` | export spans to an OTLP/HTTP collector (Jaeger/Tempo) |
| `CDP_URL=` | (gui_agent) attach to a remote Chromium over CDP instead of launching one |
| `VLM_ENABLE=1` | (gui_agent) use a multimodal model as DOM-first fallback |

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
