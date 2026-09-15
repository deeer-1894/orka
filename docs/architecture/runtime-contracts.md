# 运行、模型、工具与交付的边界

本文件描述 2026-09-15 加固后的职责划分。实际实现与回归测试是契约依据；运行中的版本可在工作台的服务状态入口查看。

## 状态所有权

| 状态 | 所属模块 | 调用方职责 |
|---|---|---|
| 会话运行占用、执行 ID、取消 | `service/execution_registry.go` + `db/execution_lease.go` | API 验证账号、会话和任务关联后申请；工作流父运行显式派生子执行 |
| 流序号、缓存、订阅 | `api/stream_hub.go` | 按执行实例发布和结束；游标缺口先补历史，禁止静默跳过 |
| 工作流与调度状态 | `workflow/`、`scheduled_task/`、对应 DB store | 只有 done 放行依赖；租约令牌控制续租与完成，旧执行不可影响新执行 |
| 模型连接、密钥、能力与调用策略 | `modelsettings/`、`core/modelprofile/` | 每次执行冻结所选配置；密钥不出现在响应、导出和普通日志中 |
| 调用预留、结算、任务及日额度 | `service/budget_*`、`db/usage*`、`llm/call_accounting.go` | 每次真实 provider attempt 先预留再结算，所有分支共享父任务 allowance |
| 文件写入意图和历史 | `core/workspaceio/` | 本地与 MCP 适配器复用，不再各自实现覆盖语义 |
| 程序运行边界 | `tools_server/runner/` | 清洁环境、当前会话挂载、限时、输出限额、取消整个执行；隔离不可用则拒绝 |
| GUI 浏览器状态 | `gui_agent/operators/sessions.py`、`service/runtime.py` | 信任身份只来自认证后的控制服务；按 owner/conversation 分配上下文，事件带 run/invocation ID |
| 业务验收、固定交付 | `core/acceptance/`、`service/acceptance*`、`core/delivery/` | 真实文件检查、保护原要求与历史、复制后校验、最后发布不可重写的清单 |
| 页面草稿、动作与文件状态 | `web/src/hooks/` 和 `web/src/lib/run*` | 按会话绑定资源；运行历史和会话入口复用同一动作规则 |

控制层不读取工具进程的实现细节；工具侧不导入控制层状态。共享模块承载真正相同的规则，传输层负责身份和数据转换。新增工具优先复用 runner/workspaceio，不能以另一条子进程或文件路径绕过它们。

## 调用预算

默认仍为单任务 2,000,000 tokens、7,200 秒、300 轮；部署可设默认与上限，单次请求可在上限内选择。主模型、摘要、标题、重试、子代理、GUI 和自动追问均需进入同一计量边界。推荐工厂顺序：

```text
Limiter → Retry → Metered → Accounted → Provider
```

预留发生在实际尝试之前；失败不能自动等同免费。供应商缺少 usage 时保留未知/估算与占用，不能当作零；收到真实 usage 后才替换估算。断线、暂停和恢复不会重置累计 token 义务。协议明确返回空、不完整或无效用量时使用 Incomplete 标记，不能被旧客户端的正数兼容逻辑重新判为完整。新增非聊天调用必须明确安装 budget context，不能仅在 UI 汇总数字。

自动追问使用关联原会话/运行的独立辅助额度（64k tokens、60 秒、6 步），进入同一账号日账本；不会重开原运行或延长其期限。API 从落库运行读取可信正文，校验 owner/conversation/run 与模型配置。重复请求在同一 ChatService 进程中合并，并缓存最多 256 项、24 小时；跨实例或重启后的去重不在本轮承诺内。

## 工具和隔离

普通工具目录读取使用 `tools:catalog` 元数据凭据，该凭据无法调用任何工具，也不创建假会话。任意代码执行需要部署启用 `CODE_EXECUTION`，并且本次请求明确选择 code/python/shell；文件权限不蕴含代码权限。外部 MCP 名称包含连接来源，原始名称仅在传输调用时使用，聚合时拒绝重名。

严格 Linux runner 使用 bubblewrap，将当前会话挂载到 `/workspace`，不暴露存储父目录、宿主临时目录、凭据或网络。镜像必须经过真实 namespace/mount 验证；缺少隔离条件时保持拒绝，不能悄悄退回宿主执行。无网络意味着任务不能直接 pip/npm 下载依赖，依赖应在可信镜像构建阶段准备。部署细节见 `tools_server/EXECUTION.md`。

GUI 模型来自本次任务的冻结连接；视觉执行要求该模型图片能力已通过真实检测。系统不再凭模型名字推断视觉能力，也不回退到另一个账号的环境密钥。多阶段 GUI 任务保留有界的阶段观察记忆，来源绑定动作前的截图和步骤；模型记录的观察不自动等于验收通过。当前动作只使用当前页面的元素，历史编号不能复用。产品只接收自己的截图和动作证据；共享 noVNC 仅是回环地址的管理员调试界面。

## 验收与交付

`check_delivery` 仅在声明交付的文件集合中检查格式、引用和数值绑定；工作区里存在但未声明的依赖不会使预检通过，错误会列出需要补入 `update_plan.outputs` 的路径。`check_acceptance` 读取 `*.acceptance.json` 并执行有边界的 contains/CSV 断言；manual 条目保持未验证。二者都不证明模型是否完整提取了原要求，也不替代来源核查。

验收样例：

```json
{
  "kind": "orka.acceptance/v1",
  "requirements": [
    {
      "id": "minimum_margin",
      "description": "各饮品原材料口径毛利率至少65%",
      "method": "csv",
      "file": "outputs/pricing.csv",
      "column": "margin",
      "operation": "min",
      "exclude": {"drink": "ALL"},
      "compare": "gte",
      "expected": "0.65"
    }
  ]
}
```

CSV 支持 count/min/max/sum，精确匹配 where/exclude，eq/gte/lte；数字使用有界十进制或科学记数法，按有理数精确计算。报告数据如果是百分数 65 而非比例 0.65，必须在期望值和说明中保持相同单位。检查记录位于会话可执行工作区之外；再次检查会保留新旧结果，不能通过删掉已出现过的 ID 绕过要求。

完成交付时，控制层复制声明文件、在复制后的文件系统上重做适用检查，最后原子提交 manifest。引用的输入文件也必须在交付清单内，才能离开原工作区独立验证。快照下载按 manifest 校验大小与 SHA-256，永不回退为当前工作区文件。普通脚本写入不自动产生逐文件历史；不可变交付只保证已发布版本，这是两个不同的承诺。

## 验证入口

- `make test lint`：全部 Go 模块与静态检查。
- `make eval`：离线契约评测；真实模型任务必须显式 `make eval-live`。
- `npm --prefix web test`、`npm --prefix web run test:browser`、`npm --prefix web run build`。
- `make test-gui` 或 `make test-gui-container GUI_TEST_IMAGE=<已构建镜像>`。
- Mongo 并发测试使用 `ORKA_TEST_MONGO_URI`，在唯一临时数据库内构造虚构身份与租约。
- 严格执行沙箱测试设置 `ORKA_REQUIRE_SANDBOX_TEST=1`，不能把跳过当作通过。

运行状态和健康检查只代表它们明确检测过的范围。服务状态页面的 GUI health 可达不代表模型已验证，模型列表可发现也不代表账号有额度或访问权限。
