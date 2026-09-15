# 固定版本交付核心

供控制层 finalization/API 接线使用；不由 tools_server 导入或调用（只有测试验证沙箱不能访问交付存储）。调用者必须先验证 owner/conversation 的访问权限，不能直接信任请求中的 owner。

```go
func Publish(base, owner, conv, run string, paths []string) (Manifest, error)
func PublishChecked(base, owner, conv, run string, paths []string, validate func(fs.FS) error) (Manifest, error)
func Read(base, owner, conv, run, path string) ([]byte, error)
func List(base, owner, conv string) ([]Manifest, error)

type File struct {
    Path string `json:"path"`
    Size int64 `json:"size"`
    SHA256 string `json:"sha256"`
}
type Manifest struct {
    Version string `json:"version"` // "orka.delivery/v1"
    ConversationID string `json:"conversation_id"`
    RunID string `json:"run_id"`
    CreatedAt time.Time `json:"created_at"`
    Files []File `json:"files"`
}
```

来源是 `base/<owner>/sessions/<conv>`。固定存储是 `base/.orka_deliveries/<sha256(owner)>/<conv>/<run>/`，其中 `manifest.json` 记录元数据，`files/<declared path>` 是独立字节副本。目录 0700，文件和 manifest 0400；生产沙箱只挂载来源会话，不能读取、改写或删除固定存储。服务自身/存储管理员仍有维护存储的权限，这不是对管理员防篡改的签名系统。

`Publish` 原子占用 run ID，复制并计算 SHA-256，拒绝复制过程中观察到的源文件变化，最后原子发布 manifest。成功返回前同步写入文件和 manifest。失败清理未完成目录；崩溃留下的无 manifest 目录不会出现在 List 中，后续对同 run 的发布返回 ErrExists，需控制层运维确认后清理。已发布的 run 不允许重写，包括内容完全相同的重复请求。

`PublishChecked` 在全部文件复制完成后、manifest 提交前调用非 nil 的校验器。校验器收到只包含声明文件的只读 `fs.FS`，其生命周期到回调返回为止；不能保存句柄供稍后读取。失败用 `%w` 保留回调错误，清理未提交 run，不会出现在 List/Read 中；成功才提交 manifest。原 `Publish` 保留无需业务校验的基础接口。

控制层在 done 终态使用闭包调用 `artifacts.CheckFS(ctx, snapshot, declaredPaths)`，将 `Report.OK == false` 转为 error，并在发布失败时将结果改为 partial。不要先检查 live workspace 再将该结果用于快照。HTML/report/acceptance 的本地引用文件必须也在 declaredPaths 中，否则快照检查将拒绝缺少的依赖；`CheckFS` 保留 acceptance 的 `DecodeSpec` strict 解码。交付模块不决定运行状态、不签发权限、不调用模型。

`Read` 只返回 manifest 声明的快照文件，并验证其大小和 SHA-256；不会回退到当前工作区。`List` 返回当前 owner/conversation 已提交的 manifest，按创建时间降序排列，校验 manifest 格式和边界；文件内容的完整性在 Read 时校验。返回的 Manifest 不包含内部存储绝对路径。

可用 `errors.Is` 区分：

- `ErrExists`：run 已占用/发布；接线层可用 List 查找同 run，不能再次覆盖。
- `ErrNotFound`：存储、交付或声明文件不存在；Read 不创建任何目录，List 对不存在的会话返回空列表。
- `ErrIntegrity`：manifest 或快照校验失败；不得回退为实时文件下载。

限制：1–256 个显式声明的文件，单文件不超过 256 MiB，总文件不超过 1 GiB；只接受规范的相对文件路径，拒绝绝对路径、反斜杠、路径穿越、重复声明和非普通文件。当前 Publish 同步执行，Read 返回完整字节切片；API 层应限制并发，并在完成生成后调用 Publish。它不是仍在写入中的多个文件的事务快照，也不替代业务验收。

验证：

```sh
go test -race ./orka_core/delivery -count=1
CODE_BWRAP_PATH=/usr/bin/bwrap ORKA_REQUIRE_SANDBOX_TEST=1 go test ./tools_server/runner -run TestSandboxCannotAccessFixedDeliveries -count=1
```
