---
description: "对 Orka 项目进行全面分析：架构设计、代码质量、产品设计、优缺点、改进意见。自动读取所有模块核心代码，输出结构化分析报告。"
---

对当前项目进行全面深度分析。按以下步骤执行：

## 第一步：项目概览
1. 读取项目根目录结构 (`ls` + `find -maxdepth 2`)
2. 读取 `README.md`、`go.work`、`config.yaml`、`docker-compose.yml`、`.env.example`
3. 统计各语言代码行数（Go/Python/TypeScript）

## 第二步：核心代码阅读
按模块逐一阅读核心文件：

- **orka_core**: `agent/agent.go`, `agent/runner.go`, `agent/emit.go`, `messages/messages.go`, `state/state.go`, `state/redis.go`, `security/token.go`, `checkpoint/checkpoint.go`, `ws/client.go`
- **orka_middleware**: 所有 `.go` 文件
- **orka_control_layer**: `api/api.go`, `api/chat.go`, `api/auth.go`, `service/adk_chat.go`, `service/middleware.go`, `service/middlewares/*.go`, `service/tools_provider.go`, `llm/llm.go`, `llm/openai.go`, `main.go`
- **tools_server**: `server/mcp.go`, `tools/*.go`
- **gui_agent**: `agent/graph.py`, `agent/model.py`, `agent/state.py`, `service/web_socket/server.py`, `main.py`
- **web**: `src/App.tsx`, `src/components/*.tsx`, `src/hooks/*.ts`

## 第三步：Git 历史
运行 `git log --oneline | head -40` 查看提交历史和演进脉络。

## 第四步：输出分析报告
用中文输出，按以下结构组织：

### 一、项目概述
项目定位、技术栈、模块组成、代码规模

### 二、架构设计分析
- 三层架构（控制面/工具面/执行面）的设计思路
- 中间件管道（ADK-style）的实现
- MCP 工具抽象
- SSE 流式通信
- Redis checkpoint 机制
- 优点和问题

### 三、代码质量分析
- 错误处理
- 测试覆盖
- 安全性（认证、SSRF防护、路径遍历）
- 性能考量
- 代码组织和可维护性

### 四、产品设计分析
- 用户体验
- 功能完整度
- 与竞品对比（ByteDance Cavis）

### 五、优缺点总结
用表格列出核心优缺点

### 六、改进建议
按优先级给出具体的优化方向，包括：
- 架构层面
- 代码层面
- 产品层面
- 运维层面

$ARGUMENTS
