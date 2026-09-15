# 工作区与代码执行迁移说明

本批实现共享文件写入契约、代码执行沙箱和生成工具的暂存发布；独立固定交付核心位于 `orka_core/delivery`。已在 tools Dockerfile 安装 bubblewrap，Compose 的 tools 小节按 `HOST_UID/HOST_GID`（默认 1000）运行；未修改 control service 或 GUI 小节，未重启部署。控制层授权与终态接线由主 agent 处理。

## 接入要求

- 将部署的 `SHELL_TOOL=1` 迁移为 `CODE_EXECUTION=1`，统一启用 shell 和 python。旧开关不再授予代码执行能力；未启用时两者均不注册。
- 签发 MCP 上下文 token 时，仅在本次请求明确选择 code/python/shell 后添加 `code:execute`，并把 scope 纳入连接池键；不要向所有任务的默认 scope 列表加执行权限。网关同时在 tools/list 与 tools/call 检查 scope；`file:write` 不自动包含代码执行权限。
- tools 镜像已安装 bubblewrap，并保留非 root 运行（镜像默认 UID 10001，Compose 覆盖为工作区所有者 UID）。Linux runner 默认使用 `/usr/bin/bwrap`；可通过部署管理的绝对路径 `CODE_BWRAP_PATH` 指定二进制。已使用 Ubuntu bubblewrap 0.9.0 验证。
- `CODE_SANDBOX_MODE` 默认为 `bwrap`。缺少二进制、命名空间被禁用、挂载失败、配置拼写错误都明确拒绝执行，绝不切换为宿主进程。
- 容器内必须允许 bwrap 所需 user/mount/PID/network/IPC/UTS 命名空间及绑定挂载。需要在实际镜像和宿主机上验证 seccomp/AppArmor/userns 策略；不能仅因容器有 `no-new-privileges` 就认为沙箱可用。不要用 privileged 或挂载宿主根目录来规避测试失败。CPU、内存、进程数和磁盘配额仍应由部署的 cgroup/存储策略限制。
- `CODE_SANDBOX_MODE=unsafe-dev` 是显式的受信任本地开发选项，**没有文件系统或网络隔离**，启动时会记录警告；仍使用干净环境、限时、输出限额与进程组取消。生产不得设置此值。

## 实际边界

严格模式创建新的文件系统、PID 和网络等命名空间，只把当前会话目录挂载到 `/workspace`，通过已打开的目录描述符绑定。系统运行时目录只读挂载：`/usr`、兼容的 bin/lib 路径以及必要的字体、证书、动态链接配置。不挂载宿主 `/home`、存储父目录、`/run`、宿主 `/tmp` 或宿主 `/proc`。运行时目录必须由部署维护，不得保存会话数据或凭据；runner 拒绝工作区与运行时挂载重叠。

HOME 指向 `/workspace`；有独立 `/tmp`、`/dev`、`/proc`。严格模式无网络，因此 pip/npm/git 拉取等联网操作需要独立的受控依赖准备流程。没有提供共享宿主网络的隐式兼容选项。

不继承网关环境变量，包括签名密钥、模型/搜索凭据、代理和加载器配置。只设置固定 PATH、HOME、TMPDIR、locale、TZ，及内置办公脚本明确允许的数据参数。bwrap 启动自身也使用干净环境。

所有 shell/python 和内置办公子进程统一经过 `runner`。办公工具保留原能力权限，不要求启用任意代码执行，但同样需要可用沙箱。stdout/stderr 各自在捕获阶段限制为 32 KiB，办公工具兼容文本合并后总计限制为 32 KiB；取消会关闭进程组，严格模式同时销毁 PID 命名空间。每次运行先探测完整隔离配方，探测失败不执行用户代码。

## 执行结果契约

shell 与 python 都返回 JSON 文本：`{ok, exit_code, stdout, stderr, timed_out, canceled}`；失败附加 `error` 说明。`exit_code=-1` 表示未启动或被信号终止，其他值是实际进程退出码。父进程先以 0 退出但后代持有输出管道直到超时的场景，exit_code 仍可为 0，必须依据 ok/timed_out 判断结果。非零退出、超时、取消、输入错误和沙箱拒绝均 `isError=true`；成功 `isError=false`。两路输出保留各自内容，不把 runner 诊断混进 stderr。Go 核心接口为 `runner.Config.Execute(ctx, Request) (Outcome, error)`，办公生成工具继续使用共用 runner 的文本适配 `Run`。

## 无会话元数据目录

UI catalog 不应调用 `ToolsFor` 或 `EnsureSession`。远端使用单独短生命周期 MCP 客户端，签发仅含 `tools:catalog` scope 的 token，conversation 留空，调用 ListTools 后关闭；不要放进执行客户端池。网关仅返回已注册且未被 blacklist 移除的工具 metadata，包括对应能力 schema。catalog 身份的所有 tools/call 均拒绝（包括原本不要求 scope 的 calculator/current_time），不会创建工作区或获得代码权限。

本地文件目录使用 `filesystem.Catalog() []ToolMetadata`，其中 `ToolMetadata` 仅含 Name/Description/Schema，不含可执行工具对象。主线自行合并 GUI、skills 和连接器 metadata。

## 文件契约

`orka_core/workspaceio` 统一 schema、默认 create、显式 append/replace、正文类型与历史占位符拒绝、备份格式及保留策略。两端不再复制写入实现。文件操作使用 `os.Root` 固定工作区句柄；create 保留 O_EXCL，append 保留 O_APPEND。旧秒级与新纳秒级历史版本均可读取和恢复，新版本目录通过排他创建避免时间戳冲突。

原有文本 file_write 的 best-effort 备份语义保留：备份失败不阻断显式 replace/append。新增 `workspaceio.ReplaceGenerated` 则先生成临时文件，成功后必须备份已有输出才允许原子替换；生成失败或备份失败保留原输出。doc_export、chart、xlsx/csv 转换、可选文件输出的 pdf_extract/doc_read/sql_query、csv_join、slides、qrcode 已接入。

任意 shell/python 脚本的写入不会自动版本化，不能把生成工具的保护当作任意程序写入的版本保证。固定交付由控制层调用独立 `delivery.Publish` 完成，存储在执行沙箱不可见的 `.orka_deliveries`；其 API 见 `orka_core/delivery/README.md`。业务验收、finalization/API/routes/前端接线和磁盘配额由主 agent 处理。

## 验证入口

```sh
go test ./orka_core/workspaceio/... ./orka_middleware/local/filesystem ./tools_server/...
go test ./orka_control_layer/api -run TestLegacyWorkspaceVersionCanRestore -count=1
CODE_BWRAP_PATH=/usr/bin/bwrap ORKA_REQUIRE_SANDBOX_TEST=1 go test ./tools_server/runner ./tools_server/tools -run 'Sandbox|Execution' -count=1 -v
```

严格 CI 设置 `ORKA_REQUIRE_SANDBOX_TEST=1`，禁止因 bwrap 不可用而跳过真实隔离测试。测试均使用临时虚构账号、会话和假环境变量，不读取真实秘密，也不调用模型。严格集成覆盖当前会话读写、同账号另一会话、另一账号、符号链接、宿主回环网络、环境和 PID 隔离、取消后脱离进程组的子进程清理。另有缺失二进制、宿主拒绝命名空间和未知模式的明确拒绝测试。

## 目标镜像验证与部署限制

目标镜像 `orka-tools:workspace-sandbox-review` 基于项目 tools Dockerfile；内含 Debian bookworm bubblewrap 0.8.0。已使用非 root 1000:1000、宿主临时工作区 bind mount、no-new-privileges、cap_drop ALL、2 GiB/256 PID 限额和正式 seccomp profile，通过真实 Docker 内的隔离与 office 集成。没有启动/重启现有网关，未调用模型。

默认 Docker seccomp 下先观察到 bwrap 拒绝；专用 [seccomp profile](security/README.md) 只在上游默认 allowlist 上增加精确 namespace 创建及必需 mount 操作。已接入 Compose 的 tools 小节，不使用 SYS_ADMIN、privileged、seccomp unconfined 或 unsafe-dev。本机无 AppArmor；其他宿主策略仍必须验证，无法建立隔离时办公/代码工具明确失败。

验证内容包括跨账号/同账号跨会话、符号链接、环境与 PID 隔离、外层回环网络不可达、固定交付不可见、取消杀死后代；shell/python 成功、非零退出、超时、取消、拒绝均检查 JSON 和 MCP isError。实际格式集成包括 HTML/DOCX/PDF 导出及旧版本保留、CSV↔XLSX、PNG chart、PPTX slides、SQL、CSV join、DOCX 读取和 PDF 文本提取。catalog metadata 和 code 权限测试也在目标镜像运行。

复现：先构建项目 Dockerfile，再运行 `bash tools_server/test-image.sh`（`ORKA_TEST_IMAGE` 可选指定标签）。脚本只使用临时假账号和临时挂载数据，不读取 .env。Go 测试编译使用 GOPROXY=off，要求本地已有模块缓存。Docker legacy builder 不识别 Dockerfile 专属 ignore 文件，建议只打包 orka_core/tools_server 两模块作为构建上下文。最终验证构建使用本地缓存生成临时 vendor 并 `docker build --network none --build-arg GOPROXY=off`，没有访问第三方 Go 代理。

tools 镜像保留默认 CODE_EXECUTION=0。启用 UI 真实 code 场景时，部署须显式设 CODE_EXECUTION=1，同时本次请求须有 code:execute；该开关不会向所有任务签发执行权限。office 不依赖任意代码开关，但仍依赖可用的严格沙箱。UID/GID 必须与挂载工作区所有者一致，Compose 默认1000，可通过 HOST_UID/HOST_GID 覆盖。
