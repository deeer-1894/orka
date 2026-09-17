# 内置 Browser 工具

## 职责与调用链

`browser` 是主模型可直接调用的 Go 内置工具，本身不调用另一个模型。模型读取有界的 DOM 快照，选择元素引用或唯一 CSS 选择器，调用一次操作，再根据返回的页面状态决定下一步。

```text
主模型 → browsertool 工具适配器 → Go 操作引擎
                                      ↓
                     connectors.BrowserDialer / BrowserLease
                                      ↓ 私有认证 WebSocket
                         Python BrowserRuntime / PageChannel
                                      ↓
                  当前 owner + conversation 的 Chromium Page
                                      ↑
                           gui_agent 共用队列与页面
```

Go 负责动作语义、快照、引用校验、页面脚本和文件发布。Python 服务是唯一的浏览器生命周期管理者，负责上下文隔离、共享队列、页面代次与 CDP 白名单。工具适配器只做参数校验、可信执行身份注入和结构化结果转换；文件模块只处理截图、下载与原子发布。

每次工具调用取得一个操作租约，期间可以发送多条 CDP 指令；结束和取消时释放。模型思考期间不持有租约。GUI 与 DOM 浏览器不会同时操作可见页面；GUI 接力、新文档、页面重建或运行身份变化会使旧元素引用失效。

## 使用范围

在现有工具选择器中启用 DOM 浏览器。工具名为 `browser`，支持：

| 动作 | 用途 |
| --- | --- |
| `open` / `snapshot` | 导航到 HTTP(S) 页面，读取文本、可操作元素、状态与能力边界 |
| `preview` | 读取并渲染本会话已有的单文件 HTML |
| `click` / `fill` / `select` / `press` | 对当前快照引用或唯一 CSS 目标操作 |
| `scroll` / `wait` | 滚动或等待明确的页面条件 |
| `evaluate` | 在当前页面隔离世界中执行有界 JavaScript，返回结构化结果 |
| `screenshot` / `download` | 将 PNG 或下载文件写入当前会话工作区 |

优先使用快照提供的 `ref` 与对应 `snapshot_id`；二者必须一起传递。不要在导航、GUI 接力或目标替换后复用旧引用。CSS 匹配多个元素会明确失败，不会任取第一个。

`wait.condition` 支持 `domcontentloaded`、`load`、`visible`、`hidden`、`enabled`、`text` 和 `url`。它等待具体条件，不能用于无限等待或长时间休眠。JavaScript 在网页中执行，不具有宿主 shell、文件系统或代码沙箱权限。

## 约束与失败语义

- 第一版操作主文档和开放的 Shadow DOM。iframe 边界会报告；跨进程 iframe、弹窗管理及纯像素内容需要 GUI 或明确后续支持。
- 快照最多200个引用、默认12000文本字符；模型可见快照和脚本结果限制64KiB，脚本限制16KiB。
- 默认排队30秒、执行45秒，单次上限60秒和256条CDP指令。私有普通回复上限128KiB，截图另有界限。
- 截图和下载最多16MiB。下载使用当前页面中的固定 fetch 帮助函数，支持同源登录态和页面 blob 导出，遵守浏览器 CORS/CSP；不承诺任意跨域或网站特殊下载流程。
- 下载分块传回控制层，不把大段 base64 交给模型。默认 `mode=create` 拒绝覆盖；显式 `replace` 复用历史保存规则。失败不会发布半个文件。
- 输入字段值和敏感动作参数不重复显示在快照、普通动作日志中；这不是对任意网页内容或用户原始消息的全局保密承诺。
- 普通 `type=number` 控件的输入不加入全文替换脱敏，避免数值筛选破坏日期和统计值。标识为验证码、PIN、密码、卡号等的数值控件仍隐藏输入回显；文本输入保持原有脱敏，字段值本身不纳入快照。
- `outcome_unknown` 表示指令可能已经改变页面，不得盲目重放。取消不能撤销网站已经完成的业务操作；服务会确认清理，必要时关闭不确定页面，再释放队列。

导航或点击成功只证明页面操作已执行。报告数字、支付、提交、业务状态等需要读取结果并单独验证。

## 配置与验证

### 会话 HTML 预览

`browser` 的 `preview` 动作接收当前会话内已有的 `.html` / `.htm` 相对路径，支持最多 1 MiB 的非空 UTF-8 单文件页面。Go 文件模块使用已认证的 owner/conversation 定位工作区，拒绝越界路径、符号链接、非普通文件；`os.Root` 约束实际读取。原始文件 SHA-256、字节数与路径作为 `preview` 回执返回，页面正文不经模型转发。

浏览器引擎继续使用原有会话租约，私有 `Orka.previewHTML` 方法只接收有界 base64 文档。只有这一方法扩展请求帧上限，普通 CDP 请求、脚本和结果上限不变。GUI 端将页面导航到新的不透明来源，再安装先于文档正文解析的离线 CSP，之后才渲染文件；不挂载工作区、不使用 `file://`、不开放本地 HTTP 服务、不向页面提供访问令牌。

该入口支持内嵌脚本、样式和 data 图片；相对资源文件、外部请求及需要后端的应用不在支持范围内。返回快照后，模型应继续通过真实点击、输入、选择及截图核验行为；加载回执本身不等于交互验收通过。重新预览会重新读取文件并重置页面。预览沿用现有变更类浏览器操作的确认设置。

独立端到端回归 `TestRealWorkspacePreview` 覆盖超过普通消息大小的文件传输、真实输入/点击、DOM 断言、截图和重新加载。GUI 的桥协议测试同时覆盖无效文档保留原页面、旧引用失效、来源隔离和外部请求被阻止。

浏览器端点从已配置 GUI 服务地址派生，共用既有私有服务认证令牌；缺少配置时返回不可用，不连接用户默认浏览器，也不使用其他账号的模型密钥。浏览器工具不要求视觉模型；调用 GUI 时仍使用该任务通过能力验证的视觉模型配置。

独立真实 Chromium 契约测试使用 `ORKA_BROWSER_TEST_WS` 和虚构测试令牌 `ORKA_BROWSER_TEST_TOKEN`；当桥运行于Docker时设`ORKA_BROWSER_TEST_HOST=host.docker.internal`并配置host-gateway，运行 `go test ./orka_control_layer/browsertool -run TestRealBrowserContract -count=1 -v`。测试应连接独立的测试服务，不连接生产用户会话；默认未配置时跳过，不可将跳过算作通过。协议、引擎、文件与服务的普通单元测试不需要模型或供应商网络。

实现计划与实际验收记录见 [Go Browser Tool Implementation Plan](../superpowers/plans/2026-09-15-go-browser-tool.md)。
