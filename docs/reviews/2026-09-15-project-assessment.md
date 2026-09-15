# Orka 项目评估与功能取舍（2026-09-15）

基于 `codex/long-task-optimization` 当前代码、六个主要模块的调用链，以及已经保存的长任务实测记录。此次工作包含静态审查、现有测试与构建、两个临时目录中的针对性复现；没有启动付费模型任务、修改运行中的服务或修复生产代码。

判断：项目已有通用 Agent 工作台的主要功能，下一阶段应集中提升执行隔离、状态一致性、验收可信度和交付完整性。继续增加专业工具之前，应先让现有能力在正常、失败、降级、续跑等路径中遵守同一套契约。

## 已有能力，应保留并补强

- 会话管理、消息搜索、分享与分支；按会话存储文件、目录导航、CSV/代码等预览、文件历史与恢复。
- Auto 与手动模型选择；厂商地址、密钥、模型列表发现、手填模型、无密钥配置导入导出。Auto 当前明确代表列表首个模型，不进行复杂度路由。
- SSE 后台执行与重新挂接、运行记录、心跳清理、检查点及长任务恢复、任务预算、澄清与操作确认。
- MCP 工具、检索缓存和证据存储、上下文分层压缩、子代理、GUI 动作收据、文件结构与数值绑定检查。
- 工作流、定时任务、Webhook、通知、可分享页面、量化因子流程与技能管理。

这些功能大多已经存在，建议围绕它们补齐边界，而不是重复建设。

## 一、优先修复的执行问题

### 1. 同一会话并发执行会串流和互相终止（P0，已复现）

`ChatRun` 用 conversation ID 开流；`streamHub.start` 无条件替换同 ID 的流，后续 publish/finish 又按 ID 查当前实例。第一轮未结束时启动第二轮，第一轮的消息会投递到第二轮，第一轮结束会关闭第二轮事件流。服务取消表同样按 task/conversation ID 覆盖，旧运行结束还可能删除新运行的取消入口。

证据：[ChatRun](/home/aibox/orka/orka_control_layer/api/chat.go:42)、[streamHub](/home/aibox/orka/orka_control_layer/api/stream_hub.go:39)、[取消注册表](/home/aibox/orka/orka_control_layer/service/adk_chat.go:115)。临时复现确认了串流和错误关闭，两项安全预期均失败；没有启动真实会话。

建议：普通会话入口实行原子运行占用，重复请求返回已有 run ID 或明确排队；每次执行使用独立 run ID，流发布/终结绑定实例。工作流允许内部并行时，使用 step execution ID 和父运行取消范围，不能简单禁止所有共享 conversation 的内部工作。

验收：双击发送、两个标签页同时发送、旧任务结束与新任务启动交错时，消息和取消对象均不串；工作流并行步骤仍能正常完成。

### 2. 文件降级路径绕过了刚补上的防覆盖保护（P0，已复现）

远端文件工具有 create/append/replace；MCP 初始化或连接失败时，控制层回退到本地 filesystem 工具。本地实现没有 mode，直接覆盖已有文件，且备份时间戳只有秒，而恢复接口要求纳秒格式。

证据：[降级入口](/home/aibox/orka/orka_control_layer/service/tools_provider.go:323)、[本地写入](/home/aibox/orka/orka_middleware/local/filesystem/fs.go:57)、[备份格式](/home/aibox/orka/orka_middleware/local/filesystem/fs.go:94)、[恢复校验](/home/aibox/orka/orka_control_layer/api/file_versions.go:98)。临时文件验证：传 mode=create 仍覆盖原文且没有错误；旧备份时间戳不能通过恢复格式校验。

建议：将写入意图、正文校验、备份格式和原子发布收敛到共享文件模块，两种传输适配器复用；补旧备份格式的读取兼容。对不能维持相同契约的写操作，不应静默降级。

验收：正常与降级工具跑同一套契约测试；默认拒绝覆盖、追加保留旧文、历史可恢复，压缩占位符不得成为正文。

### 3. 工作流把部分完成、暂停也当作成功（P1，代码确认）

`runStep` 仅判断 status != failed，即返回 ok=true。因此 partial 和 paused 都可能放行依赖步骤；定时任务的失败熔断也把 partial 当成功。流程创建只校验名称和至少一个步骤，缺少完整的节点唯一性、依赖存在性和环校验；无可运行节点时执行循环直接退出。

证据：[步骤结果](/home/aibox/orka/orka_control_layer/service/workflow.go:113)、[创建流程](/home/aibox/orka/orka_control_layer/api/workflow.go:32)、[自动任务结果](/home/aibox/orka/orka_control_layer/service/adk_chat.go:449)。

建议：步骤成功只接受明确完成；暂停、部分完成、失败分别决定等待/恢复/终止。增加流程整体状态、步骤状态和定义校验。定时触发还需原子占用与重叠策略：当前 AdvanceTaskRun 按 task_id 更新，不能保证多实例互斥，也不阻止执行时间超过调度间隔后的重叠。[调度更新](/home/aibox/orka/orka_control_layer/db/storage.go:632)

验收：上游 partial/paused 不触发下游；循环依赖在保存前拒绝；两台调度器或运行跨周期不重复启动。

### 4. 文件检查通过不等于业务任务通过（P1，已有实测）

`check_delivery` 已明确限定为文件结构、声明的产物和数值绑定；这是正确的边界，但平台仍缺少独立的任务验收层。上一轮38项自测和文件检查通过后，外部验收仍发现零收入异常、中途舍入错误、区域范围混入汇总行、HTML缺列、ZIP与README不一致；观察记录还需要外部恢复。

证据：[检查工具的边界](/home/aibox/orka/orka_control_layer/service/delivery_contract.go:105)、[完整实测](/home/aibox/orka/docs/superpowers/reports/2026-09-15-flash-gui-recovery.md)。

建议：增加版本化任务要求与验收项，每条记录对应文件、检查命令/数值断言、来源证据和待确认事项；中途变更显式修订约束。文件结构检查、业务规则校验和人工判断分别表达，不让模型一句“完成”代替验收。程序交付在全新目录按README复跑；报告重算与来源核对独立于生成过程。无法自动验证的条件保留为未验证。

验收：回归上述已知失败，另换输入与区域排序，避免固定样例通过即声称通用正确。当前报告 binding.where 为精确匹配，可优先增加独立聚合数据与明确范围，而非把业务规则散落在字符串模板里。[绑定实现](/home/aibox/orka/orka_core/reporting/render.go:238)

## 二、跨会话边界和可维护性

### 5. 文件接口按会话隔离，代码执行和GUI尚未达到同样边界（多用户部署前P0；单用户开发P1）

- shell/python 设置 cwd 和 HOME，但继承网关环境；Docker 挂载整个存储根。工作目录不是访问边界。代码进程存在读取其他会话或继承凭据的风险，本次没有尝试读取任何秘密。[进程环境](/home/aibox/orka/tools_server/tools/office_gen.go:22)、[挂载](/home/aibox/orka/docker-compose.tools.yml:27)
- shell/python 的 scope 为空，shell 有启用开关而 python 仍注册。应统一 code:execute 能力授权。[工具权限](/home/aibox/orka/tools_server/tools/tools.go:55)
- GUI 全局浏览器与锁仅保证串行；about:blank 不清除 Cookie 和站点存储。应传递可信 owner/conversation/run 身份，按会话分配浏览器上下文，并在当前会话映射正确的可视浏览器。[GUI生命周期](/home/aibox/orka/gui_agent/service/web_socket/server.py:44)
- 技能是全局注册表，安装和删除接口没有所有者参数，运行时 skill_create 也写全局。应区分只读系统技能、个人技能和明确共享技能。[注册表](/home/aibox/orka/orka_control_layer/service/middlewares/skills_registry.go:29)、[删除接口](/home/aibox/orka/orka_control_layer/api/skill.go:46)

建议先做真实的执行边界：按会话挂载沙箱、白名单环境、GUI上下文租用、用户技能仓库；不要把所有访问控制塞进聊天服务。验收使用两个虚构账号互相不可见的测试文件与浏览器存储，不接触真实账号秘密。

### 6. 工具身份与成功状态需要统一契约（P1）

工具管理器拼接多来源工具，子代理按裸名称建 map，重名后者覆盖前者；成功判定部分依赖文本前缀及JSON中的若干字段。接入更多MCP服务后容易出现语义漂移。

证据：[工具聚合](/home/aibox/orka/orka_middleware/toolsmanager/manager.go:27)、[名称覆盖](/home/aibox/orka/orka_control_layer/service/eino_chat.go:201)、[结果判断](/home/aibox/orka/orka_control_layer/service/call_limit_outcome.go:46)。

建议：内部工具ID包含来源，保留内置名称并拒绝冲突；适配层统一 outcome、副作用属性、产物、证据、用量与可重试性。模型看到的文本是展示层，执行状态来自结构化结果。无需一次替换所有工具，先覆盖文件、代码与GUI三条主要路径。

### 7. 文件历史应升级为可追踪交付版本（P1）

已有历史列表、差异和恢复，不能把“增加版本功能”当作全新需求。但终端脚本、doc_export等写入不会自动经过 file_write 的备份；历史回复下载链接指向当前路径，后续修改会改变下载内容。

证据：[导出实现](/home/aibox/orka/tools_server/tools/office_gen.go:71)、[交付回执](/home/aibox/orka/orka_control_layer/service/delivery_response.go:32)。

建议：工具生成临时文件，校验后原子发布；任务结束形成产物清单，绑定 run ID、文件版本、哈希、验收记录。编程任务可在沙箱内记录工作区变更，发布交付快照。不要承诺仅改 file_write 就能保护任意脚本写入。

## 三、长任务成本、模型与前端

### 8. 预算需要完整计量和可配置策略（P1）

单次任务2M tokens、2小时、300轮是代码常量；日限额可配置。GUI usage 未进入主账本；日限额根据已落库run的tokens求和，没有对并发在途调用预留额度。多任务同时启动时存在超出日上限的窗口。

证据：[任务限额](/home/aibox/orka/orka_control_layer/service/run_quota.go:24)、[日用量汇总](/home/aibox/orka/orka_control_layer/db/storage.go:757)、[GUI实测计量边界](/home/aibox/orka/docs/superpowers/reports/2026-09-15-flash-gui-recovery.md)。

建议保留用户已经要求的长任务额度，改为部署默认＋本次任务策略；展示额度、已用量和剩余额度。主调用、摘要、重试、子代理、GUI、追问建议纳入统一用量账本，缺少供应商usage时标记未知/估算。对在途调用预留再结算；并发分支共享父任务预算。延迟按排队、首个有效动作、模型、工具、GUI、恢复分解，优化目标是单位成果耗时与用量。

### 9. 模型配置应从“地址列表”完善为能力与连接配置（P1）

当前用户配置支持一个服务地址及其模型列表，运行客户端主要使用OpenAI兼容协议。前端选择不会自动同步GUI服务，后者读取独立环境变量；GLM等模型的调用策略由精确名称表配置，摘要另有策略。Ark无列表接口时已正确显示预设候选，不应再把预设当权限验证。

证据：[用户配置解析](/home/aibox/orka/orka_control_layer/service/model_settings.go:49)、[调用策略](/home/aibox/orka/orka_control_layer/service/model_call_limits.go:10)、[发现与预设](/home/aibox/orka/orka_control_layer/modelsettings/discover.go:34)、[GUI请求](/home/aibox/orka/orka_control_layer/connectors/gui.go:55)。

建议增加多个命名连接配置、显式协议适配、文本/图片/工具调用能力及连接测试结果，区分未验证候选和已验证模型；不凭名字猜能力。用户交互继续只保留Auto和手动选择。GUI优先使用所选且经过验证的多模态模型；能力不符时明确说明，不悄悄调用另一个账号的默认模型。统一GUI_PLANNER与VLM_ENABLE的输入模式判断，避免双开关矛盾。

### 10. 前端运行状态与多个操作入口规则分叉（P1）

- 会话页有完整恢复资格、最新运行和预算检查，运行历史页仅根据 resumable 直接请求并报成功。[历史续跑](/home/aibox/orka/web/src/components/ArtifactDrawer.tsx:1036)
- Stop 发请求后立即断流并设 idle，吞掉停止请求失败；应显示停止中，等确认后终结，连接故障与执行失败分别表示。[停止实现](/home/aibox/orka/web/src/hooks/useChatStream.ts:267)
- Thread.group 在整个会话只保留一个planBlock，新用户轮次不重置，后一个任务计划会替换前一个位置的计划。[计划分组](/home/aibox/orka/web/src/components/Thread.tsx:47)
- 输入草稿与附件未按会话成套保留；重新生成只重新发送文字，没有复用完整附件输入。[草稿](/home/aibox/orka/web/src/components/Composer.tsx:155)、[重新生成](/home/aibox/orka/web/src/App.tsx:338)
- 模型配置加载失败后仍显示可保存的空初始表单；模型列表为空时选择器存在空值访问风险。[配置加载](/home/aibox/orka/web/src/components/ModelSettings.tsx:18)

建议提取统一运行动作控制器，所有入口复用；连接状态与任务状态分开；计划按run/轮次分组；草稿及附件按会话保存，发送失败保留输入，原请求重试绑定输入快照。

### 11. 产物和长会话体验（P2）

文件面板缺少任务产物变化驱动的统一刷新；@文件只列根目录；运行历史固定取50条，“只看失败”空时却说所有运行成功。已有文件夹打开、CSV和代码高亮应保留，补分页、刷新错误、目录引用和当前任务产物范围。

证据：[文件加载](/home/aibox/orka/web/src/components/ArtifactDrawer.tsx:470)、[文件引用](/home/aibox/orka/web/src/components/Composer.tsx:228)、[运行历史空态](/home/aibox/orka/web/src/components/ArtifactDrawer.tsx:980)。

SSE只缓存256帧，结束保留30秒且在内存；大缺口应返回明确的补历史/快照指令，持久化事件位置与run ID一致，不能默认所有缺失帧都能补回。[SSE缓冲](/home/aibox/orka/orka_control_layer/api/stream_hub.go:21)

构建入口JS约971kB、gzip约303kB，有大块提示。这是构建观察，未测真实首屏卡顿；建议按运营面板和重型预览按需加载，再用浏览器性能记录验证。长会话可在量化渲染耗时后引入分段加载/虚拟列表。

## 四、明确的功能增删建议

| 动作 | 功能点 | 目的与边界 |
|---|---|---|
| 增加 | 当前任务摘要卡 | 阶段、实际模型、用量、未完成项、已有产物、最近有效动作；复用运行账本，不再维护一套状态 |
| 增加 | 任务验收清单与证据入口 | 原要求、中途约束变更、检查记录、未验证项、人工干预均可追踪 |
| 增加 | 按会话保存的草稿和完整重试快照 | 保留文字、附件、模型与工具选择，避免重新生成悄悄换输入 |
| 增加 | XLSX优先的办公文件只读预览 | 接续现有CSV/代码/PDF预览，随后按需求做DOCX/PPTX；限制大小和资源加载 |
| 增加 | 多个命名模型连接与能力检测 | 保留Auto/手动，区分列表发现、权限与实际可调用性 |
| 增加 | 服务就绪与运行版本页面 | 显示控制层、工具、GUI的版本和能力是否就绪；现有health只证明HTTP进程存活 |
| 合并 | 文件与可分享页面的产物入口 | 统一发现，保留类型和范围筛选；分享权限仍由各类型后端管理 |
| 合并 | 续跑/停止/重新生成的业务规则 | 共用运行动作模块，保留不同动作的明确语义和小按钮 |
| 移出默认主流程 | 量化因子、回测、GP进化 | 七个量化工具目前追加给普通运行；按插件/能力包显式启用，通用研究和编程任务不必承担这部分选择负担 |
| 默认折叠 | 高级DAG编辑、调度管理、技能安装 | 放到自动化/设置/专业功能入口，不因当前不常用就删除有价值的实现 |
| 删除生产路径 | 未配置GUI时返回“completed”的mock | 未配置就显示能力不可用，mock仅留测试；避免模拟结果成为完成证据 |
| 删除重复实现 | 本地与远端文件写入的不同业务规则 | 传输适配器复用共享写入核心，保留可用的本地模式 |
| 逐步删除 | Main/Mini、MiniModel、旧mini参数与escalated残留 | 实际选择已统一；迁移期仅在配置读取边界兼容，内部不再传播旧概念 |
| 改为按需 | 自动追问建议 | 当前会实际调用模型；增加关闭选项与缓存，计量可见 |
| 默认关闭有副作用自动重放 | GUI宏中的写入操作 | 当前测试环境已关闭宏，但代码/Compose默认开启；需账户、站点、前置条件、敏感参数和部分执行边界完备后再启用 |

功能取舍依据：[量化工具默认追加](/home/aibox/orka/orka_control_layer/service/tools_provider.go:39)、[七个量化工具](/home/aibox/orka/orka_control_layer/service/quant_tools.go:37)、[GUI mock](/home/aibox/orka/orka_control_layer/service/tools_default.go:23)、[旧模型兼容](/home/aibox/orka/orka_core/config/config.go:42)、[追问调用](/home/aibox/orka/orka_control_layer/service/followups.go:32)、[宏入口](/home/aibox/orka/gui_agent/service/web_socket/server.py:145)。宏风险来自静态代码，未对真实站点进行副作用重放测试。

## 五、可维护的模块调整顺序

保持现有Go/Python/React边界，优先按状态所有权收敛职责，暂不需要增加微服务数量：

| 模块职责 | 对外只暴露 | 应封装的复杂度 |
|---|---|---|
| 运行生命周期 | start / attach / stop / resume / status | 独占或排队、执行身份、租约、持久化、终态、工作流父子关系 |
| 模型连接 | resolve / discover / probe / invoke | 连接配置、协议能力、密钥、重试、调用上限和用量上报 |
| 工作区与交付 | read / write / versions / publish | 路径约束、写入意图、备份、版本、交付清单、文件检查 |
| 工具执行 | discover / invoke / cancel | 来源身份、授权、沙箱、结构化结果、证据与使用量 |
| 前端会话资源 | 任务、草稿、文件、运行动作的hooks | 请求过期保护、加载/失败状态、入口一致性、缓存刷新 |

不要按文件行数机械拆分：先修本地/远端文件语义分叉和两处恢复按钮的规则分叉，再把确定共享的规则移到独立模块。较大的ArtifactDrawer、Thread和ChatService随后按已识别的职责拆分，避免新增抽象后业务状态依然散落各处。

## 六、验证与交付工程

此次验证结果：

- `make test`：四个Go模块现有测试通过，部分命中缓存。
- `npm test`：现有前端测试通过。
- `npm run build`：通过，有大块提示；生成的已跟踪编译缓存已还原。
- 临时并发流复现：两个隔离预期失败，确认事件串流与旧执行关闭新流。
- 临时本地文件验证：mode=create仍覆盖；旧备份时间戳不被当前恢复格式接受。
- `make eval`：失败，指向不存在的 `orka_control_layer/eval/`。
- 现存 `cmd/eval` 的文件检查、读取和清理没有带新会话范围，尚未跟上会话工作区接口；历史评测结果不能直接作为当前端到端保证。[文件评测调用](/home/aibox/orka/orka_control_layer/cmd/eval/main.go:506)

建议统一验证入口并接入CI：快速契约测试覆盖主要模块；浏览器模拟接口测试覆盖真实操作入口；固定夹具覆盖研究、表格、编程、GUI、中途变更、断线、重启、预算耗尽与双会话并发。昂贵模型评测单独显式启用，记录模型/配置/代码版本、耗时、tokens、首次正确交付率及外部干预次数。

部署方面保留现有持久存储约定，增加可复现版本与就绪检查，区分进程活着和工具可用。启动脚本目前按进程名广泛终止控制层、前端仍单独启动；应按当前部署身份管理生命周期，避免多分支本地开发时误停其他实例。[启动脚本](/home/aibox/orka/scripts/dev.sh:72)

推荐实施顺序：第一批修同会话并发、文件降级契约、工作流终态与评测入口；第二批统一运行动作、GUI/代码隔离、模型配置和预算计量；第三批补验收清单与不可变交付，再整理产物界面和按需专业功能。没有依据给出确定工期，执行时应按小批可回归的变更推进。
