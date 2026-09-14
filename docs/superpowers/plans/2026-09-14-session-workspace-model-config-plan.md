# 会话工作区、文件预览与模型配置 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立会话级文件隔离与预览能力，增加多厂商模型配置，并清理旧数据和过时的重新执行入口。

**Architecture:** 后端以认证用户和 conversation ID 共同解析工作区，文件 API 不再依赖共享根目录；模型配置按用户保存并通过 OpenAI 兼容协议获取模型列表。前端以当前会话 ID请求文件，使用目录状态驱动浏览与预览，模型设置独立成表单面板。

**Tech Stack:** Go、Gin、MongoDB、React/TypeScript、现有 UI 组件、现有路径安全工具。

## Global Constraints

- 所有文件操作必须限制在当前用户当前会话工作区。
- API Key 只能写入和脱敏读取，禁止出现在列表、日志和运行记录。
- 保留已有用户文件；只清理 MongoDB 历史运行数据。
- 不引入重量级前端依赖；CSV 与代码预览使用现有能力或轻量实现。
- 所有改动只提交到 `codex/long-task-optimization`。

### Task 1: 会话工作区和文件 API

**Files:**
- Modify: `orka_core/pathsafe/pathsafe.go`
- Modify: `orka_control_layer/api/file.go`
- Modify: `orka_control_layer/api/chat.go`
- Modify: `orka_control_layer/router.go`
- Test: `orka_control_layer/api/file_test.go`

**Interfaces:**
- Produce `SessionRoot(base, user, conversationID string) (string, error)`。
- 文件列表、读取、下载、删除接口统一接受当前会话上下文。

- [ ] 写跨会话读取、路径越界和会话目录创建的失败测试。
- [ ] 运行 `go test ./orka_control_layer/api ./orka_core/pathsafe`，确认测试先失败。
- [ ] 实现会话根目录解析和 API 上下文校验。
- [ ] 重跑上述测试并补充已有用户根目录兼容行为。
- [ ] 提交 `feat: isolate files by conversation workspace`。

### Task 2: 前端文件浏览和预览

**Files:**
- Modify: `web/src/types.ts`
- Modify: `web/src/api.ts`
- Modify: `web/src/components/WorkspacePanel.tsx`
- Create: `web/src/components/FilePreview.tsx`
- Test: `web/src/components/WorkspacePanel.test.tsx`

**Interfaces:**
- `listFiles(conversationId, path)` 返回带 `kind`、`size`、`modifiedAt` 的条目。
- `FilePreview` 根据扩展名渲染 CSV 表格或代码文本。

- [ ] 写目录点击、返回上级、CSV 表头/行限制和代码扩展名识别测试。
- [ ] 运行前端测试确认失败。
- [ ] 接入当前会话 ID并实现目录导航。
- [ ] 实现 CSV 截断提示、代码等宽展示和安全文本渲染。
- [ ] 运行 `cd web && npm run build` 与测试。
- [ ] 提交 `feat: add session file navigation and previews`。

### Task 3: 模型配置后端

**Files:**
- Modify: `orka_core/config/config.go`
- Modify: `orka_control_layer/db/model.go`
- Create: `orka_control_layer/api/model_settings.go`
- Modify: `orka_control_layer/router.go`
- Test: `orka_control_layer/api/model_settings_test.go`

**Interfaces:**
- `GET/PUT /api/v1/settings/model` 返回脱敏配置。
- `POST /api/v1/settings/model/models` 从配置的 Base URL 获取模型列表，失败时返回可编辑空列表和错误原因。

- [ ] 写 API Key 脱敏、用户隔离、模型列表失败回退测试。
- [ ] 运行 Go 测试确认失败。
- [ ] 实现按用户保存、脱敏序列化和兼容 `/models` 请求。
- [ ] 接入运行请求的模型选择校验。
- [ ] 运行 API 测试、`go vet ./orka_control_layer/...`。
- [ ] 提交 `feat: add per-user model settings`。

### Task 4: 模型配置前端

**Files:**
- Modify: `web/src/api.ts`
- Create: `web/src/components/ModelSettings.tsx`
- Modify: `web/src/App.tsx`
- Test: `web/src/components/ModelSettings.test.tsx`

- [ ] 写厂商预设、自定义 Base URL、模型列表刷新、手填模型和脱敏显示测试。
- [ ] 实现配置表单和保存反馈。
- [ ] 将会话运行使用的模型选择接入新配置。
- [ ] 运行前端测试和 `npm run build`。
- [ ] 提交 `feat: add model provider settings UI`。

### Task 5: 清理数据和移除过时入口

**Files:**
- Modify: `web/src/components/RunResult.tsx`
- Modify: `web/src/components/ConversationView.tsx`
- Create: `orka_control_layer/db/cleanup.go`
- Test: `orka_control_layer/db/cleanup_test.go`

- [ ] 写清理集合范围和幂等性测试。
- [ ] 实现一次性清理 `runs/messages/checkpoints/events`，保留账号、配置和文件。
- [ ] 删除“重新执行（从头开始）”按钮及提示，保留刷新/重新生成按钮。
- [ ] 运行后端测试与前端构建。
- [ ] 提交 `feat: remove restart-from-scratch action`。

### Task 6: 注册与端到端验收

- [ ] 启动当前分支前后端并确认健康检查。
- [ ] 注册 `real@test.com` / `123456`，验证登录。
- [ ] 创建两个会话，分别写入文件，验证互不可见。
- [ ] 验证文件夹、CSV、代码预览和模型配置脱敏展示。
- [ ] 执行数据库清理并确认账号、配置和文件仍存在。
- [ ] 运行 `git diff --check`、Go 测试、前端构建，提交最终整合提交并推送当前分支。
