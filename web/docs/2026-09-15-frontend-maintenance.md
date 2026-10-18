# 前端收尾约定

本次仅修改 web；不提交、不重启真实服务、不操作演示账号，也不调用真实模型。浏览器回归使用独立 Vite/Chromium 和模拟 API。

## 状态与入口

- 聊天没有常驻 TaskSummary 或预算/统计卡。正常进度使用原消息流；断线、停止等待与错误按条件展示，保留重连与再次停止。
- 当前会话预算在工作台「概览」中折叠编辑；空值/0 保留部署默认。Composer 与自动追问发送均带该会话预算。运行记录中的预算、指标和验收按 run_id 查询；全局指标留在原「运营台 → 指标」中。
- 验收复用运行历史的展开入口；没有检查、存在未验证项目或错误，均不显示通过。交付快照与当前工作区区分；历史消息只按自己的 run_id + manifest 精确路径绑定快照，未发布/旧历史标注当前工作区 fallback。
- 专业功能折叠，重型面板与 XLSX 库按需加载。XLSX 多工作表预览显示文件缓存结果，不执行公式；有大小、工作表和网格限制。
- GUI 只显示当前 run 的 SSE 截图与动作证据，没有生产共享 noVNC 地址或嵌入。

## 草稿与发送快照

`useConversationDraft` 由会话层持有，Composer 和工作台共同使用；`useConversationSettings` 持有模型、工具 scope、技能与确认模式。`SessionRecoveryStore` 仅将白名单字段写 sessionStorage：schema v1、owner + conversation + kind，24 小时 TTL，单项 256 KiB、总量 2 MiB、最多 32 项。附件保存作用域和路径引用，不复制文件字节。

登录 owner 变化清理其他 owner 的记录；logout 清理并使旧异步写入失效。损坏、过期、版本不匹配、超限或存储失败不得恢复旧快照，失败会提示。快照不包含 token/API key。重试快照独立于当前编辑的草稿，保留原附件、tools/scope、模型及 profile revision、确认模式、技能和预算。

发送在等待创建会话之前捕获本次输入与设置；Composer 显式传已确定的 conversation_id，避免创建期间切换会话导致重定向。成功只清理被本次发送接受的内容，失败保留草稿附件。

## 自动追问（覆盖此前手动 opt-in 方案）

回答完成后自动生成建议，只有原有风格的建议 chips，没有生成按钮或说明行。只请求当前 owner 自有会话最新已回答轮次，使用回答自己的 conversation_id、run_id、model_profile。缺少 run/profile 的旧历史不请求。

前端缓存和在途请求按 owner、conversation、run、模型 revision 与回答区分（最多 50 项）；切换会话不重复调用，晚到结果不串会话，logout/401 使旧请求写入失效。后端按 owner/cid/run 校验可信落库输入并去重，辅助调用归独立有限辅助预算与 daily 账本，不重开原任务预算。

终态 SSE 领先落库时，409 最多重试两次（250/750 ms）。连续冲突后，仍停留在该回答会在 5 秒后再尝试一次（合计最多四次）。切换会话/卸载取消等待，后续再次进入可领取尚未使用的这一次；用完不再自动循环。其他失败安静结束，不新增重试按钮。

## 工作台宽度与视觉

`workbenchWidth` 负责纯宽度规则和用户偏好，`useWorkbenchWidth` 负责窗口事件、指针捕获与键盘行为；业务面板不持有拖拽状态。默认 400px，通常 360–720px；桌面保留 320px 外部空间，小屏保留 16px 边距，更窄窗口允许低于 360px。调整值按 owner 存 localStorage，不存业务或认证数据；窗口缩小时仅夹紧显示值，放大后恢复偏好。

左边界 separator 有可见把手、悬停提示和焦点反馈；方向键每次 20px、Shift 60px、Home/End 到上下限。使用 Pointer Events + capture 支持鼠标和触控，取消/窗口变化会结束拖拽。内部容器 min-width:0，导航可换行，长文本/文件路径可折行，表格保留可访问的横向滚动。

ActionChip 复用 Button 的 pill/headerChip variants 与 warm tokens。顶部「模型配置」「工作台」与「不确认」同为 28px 高、12px 字、全圆角；工作台选中态有可辨背景。新验收、预算、交付等操作复用紧凑胶囊，不重做原有工具选择器与页面。

## 验证入口

- `npm test --prefix web`：纯模块，包括持久化边界和宽度规则。
- `ORKA_CAPTURE_SCREENSHOTS=1 npm run test:browser --prefix web`：独立模拟 API，含 reload、发送竞态、恢复缺口、命名模型能力、预算、私有 GUI、固定交付/验收、693px 真拖拽与长文本溢出。
- `npm run build --prefix web`：TypeScript + Vite，写入 web/dist。
- 最终日志：tests/hardening-final-unit.log、hardening-final-browser.log、hardening-final-build.log；截图在 tests/screenshots。真实后端部署与账号演示由父层验收。

## DOM browser 工具入口

现有 catalog picker 将 `browser` 显示为「网页 DOM」，`gui_agent` 显示为「GUI 视觉」。DOM 描述指向网页结构、表单和页面脚本，GUI 描述指向截图视觉操作。仅在后端 catalog 返回能力时显示；选择分别发送原 group ID，不附加 code/python/shell scope，不新增顶部入口或审批。模拟 API 回归验证选择、刷新持久化与发送范围；Go catalog/运行时集成由后端负责。
