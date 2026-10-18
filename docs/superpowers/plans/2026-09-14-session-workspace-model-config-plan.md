# 会话工作区、文件预览与模型配置实施记录

目标：建立会话级文件工作区与预览，增加用户模型服务配置，移除从头重新执行入口。所有修改仅在 `codex/long-task-optimization`。

技术栈：Go/Hertz、MongoDB、React/TypeScript、现有 Radix UI 组件。

## 1. 会话工作区

- [x] `pathsafe.SessionRoot`、`EnsureSession` 和 `CopySession` 统一解析与复制会话目录。
- [x] 文件 API 使用已授权会话的持久化所有者；查询参数 `conv` 和请求体 `conversation_id` 必须一致。
- [x] 上传、下载、删除、目录、版本、恢复通过 rooted handles 限制在会话目录。
- [x] MCP 签名、连接池、本地工具、附件、输出检查、量化报告传递会话上下文。
- [x] 测试覆盖路径越界、无会话、共享读写权限、会话复制与不同会话连接隔离。

主要实现：`orka_core/pathsafe`、`orka_control_layer/api/file*.go`、`orka_control_layer/service/tools_provider.go`、`tools_server`。详细边界见 `../reports/2026-09-14-backend-session-workspace-isolation.md`。Shell/Python 沿用工具容器既有挂载边界；没有新增独立 OS 沙箱。

## 2. 前端文件浏览

- [x] `ArtifactDrawer` 按当前会话列出文件，支持点击文件夹、面包屑和返回上级。
- [x] `Composer` 上传与 @文件补全、`Thread` 输出文件链接、`FilePreview` 均携带会话上下文。
- [x] 并发创建会话去重；切换会话后忽略旧上传、列表与预览的异步结果。
- [x] CSV 引号/逗号/多行解析，字节、行列限制和截断提示；代码使用安全文本分词着色。
- [x] 独立浏览器测试与前端单元测试验证。

## 3. 模型配置与执行

用户最终确认：删除主、次模型功能，只保留 Auto 和手动固定模型。Auto 使用有序列表首项，执行中不按复杂度升级。

- [x] `modelsettings` 模块按用户持久化私有配置，在工作区挂载外保存密钥。
- [x] `POST /api/v1/controller/model-settings/get`、`/save`、`/discover` 提供脱敏读取、保存和兼容 `/models` 获取。
- [x] 前端顶部“模型配置”提供厂商预设、Base URL、API Key、有序列表手填和 JSON 导入导出。
- [x] JSON 不包含密钥；老配置默认模型迁入列表，不恢复主次分级。
- [x] 单次运行冻结用户配置和选定模型，主任务及辅助调用不切换模型。
- [x] 删除旧自动升级路由；显式未知模型明确失败，不静默换成默认模型。
- [x] 推荐问题请求携带原回答的模型选择。

兼容协议与配置示例见 `../../model-settings.md`。厂商预设不等于支持所有厂商的原生协议。

## 4. 清理及入口

- [x] 一次性清理历史 runs、messages、conversation_turns、conversation_events、conversation_runtime；保留用户、配置与既有文件。没有新增产品内常驻清空入口。
- [x] 删除“重新执行（从头开始）”按钮及从头执行说明，保留小型重新生成动作。
- [x] 注册并登录 `real@test.com` 测试账号。
- [x] 实际两个会话验证文件上传/目录/下载、另一会话不可见以及路径越界拒绝。

## 5. 最终交付检查

- [x] 完成最新 Auto/手动语义的全量 Go 测试、race、vet 与前端浏览器测试、构建。
- [x] 重建工具镜像、重启当前分支前后端并验证健康状态。
- [x] 实际兼容服务请求验证 Auto 与手动均到达指定模型，未知模型不调用服务；恢复临时测试覆盖配置。
- [x] 交叉审查、差异检查通过；集成仅使用当前分支。
