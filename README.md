# Cavis (OSS replica)

A runnable open-source replica of the Cavis enterprise AI Agent platform:
control plane + tool plane + execution plane, with an ADK-style middleware
runtime, MCP tools, SSE streaming, Clarify interrupt + checkpoint resume, and
a Playwright/CDP GUI agent.

## Layout (go.work multi-module)

| Module | Role |
|--------|------|
| `cavis_core` | protocol + SDK: messages, state, checkpoint, agent runtime, ws, trace, security |
| `cavis_middleware` | tool-abstraction layer: ToolsManager + multi-transport MCP client |
| `tools_server` | MCP gateway service: multi-source tool aggregation + routing |
| `cavis_control_layer` | control plane: API, SSE chat, middleware pipeline, persistence |
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

See the phased build plan for implementation status.
