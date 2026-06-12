---
description: "启动或重启 Orka 本地开发服务。支持选择性启动 control/tools/gui/web 全部或指定服务。"
---

启动或重启 Orka 本地开发环境的服务。

## 使用方式
- `start-services` — 启动所有服务（control + tools + gui + web）
- `start-services control` — 仅启动 control layer
- `start-services tools` — 仅启动 tools_server
- `start-services gui` — 仅启动 gui_agent
- `start-services web` — 仅启动 web 前端
- `start-services control tools` — 启动指定的多个服务

## 服务端口
| 服务 | 端口 | 健康检查 |
|------|------|----------|
| orka_control | 8088 | `curl -s http://127.0.0.1:8088/health` |
| tools_server | 8090 | `lsof -nP -iTCP:8090` |
| gui_agent | 8100 | `curl -s http://127.0.0.1:8100/health` |
| web (vite) | 5173 | `curl -s http://localhost:5173/` |

## 执行步骤

### 1. 停止残留进程
```bash
pkill -f '/tmp/orka_control' 2>/dev/null
pkill -f '/tmp/orka_tools' 2>/dev/null
pkill -f 'service.web_socket.server' 2>/dev/null
pkill -f 'vite' 2>/dev/null
sleep 1
```

### 2. 构建二进制（如需启动 control 或 tools）
```bash
cd /Users/shiyi/cavis
go build -o /tmp/orka_control ./orka_control_layer
go build -o /tmp/orka_tools ./tools_server
```

### 3. 启动各服务

**tools_server:**
```bash
set -a; . ./.env; set +a
TOOLS_ADDR=:8090 BASE_STORAGE_PATH=/tmp/orka-storage CTX_TOKEN_SECRET=${CTX_TOKEN_SECRET:-devsecret} \
  /tmp/orka_tools >/tmp/orka_tools.log 2>&1 &
```

**orka_control:**
```bash
set -a; . ./.env; set +a
export MONGO_URI=${MONGO_URI:-mongodb://127.0.0.1:27017}
export REDIS_ADDR=${REDIS_ADDR:-127.0.0.1:6379}
export SCHEDULER_ENABLE=1
/tmp/orka_control >/tmp/orka_control.log 2>&1 &
```

**gui_agent (Docker):**
```bash
docker build -t cavis-gui-agent gui_agent/
docker rm -f cavis-gui 2>/dev/null
docker run -d --name cavis-gui --rm --shm-size=1g -p 8100:8100 -p 6080:6080 cavis-gui-agent
```

**web (Vite dev server):**
```bash
cd web && nohup npm run dev >/tmp/vite.log 2>&1 &
```

### 4. 等待健康检查
对每个启动的服务，轮询健康检查端点直到就绪（最多 30 秒）。

### 5. 输出状态汇总
列出每个服务的端口和运行状态。

## 注意事项
- 启动前先加载 `.env` 环境变量
- control 依赖 tools_server（TOOLS_MCP_URL），应先启动 tools
- gui_agent 可用 Docker 或本地 Python 方式启动，默认用 Docker
- 如遇端口占用，先 `lsof -nP -iTCP:<port>` 检查并 kill 残留进程

$ARGUMENTS
