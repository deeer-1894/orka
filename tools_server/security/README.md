# tools 容器的 seccomp 例外

`seccomp-bwrap.json` 基于 [Moby profiles seccomp/v0.2.1 的默认 allowlist](https://github.com/moby/profiles/blob/seccomp/v0.2.1/seccomp/default.json)，保留默认 `SCMP_ACT_ERRNO`、原有参数限制和 capability 条件。随附 Apache-2.0 许可证。与上游相比仅追加：

- `clone` 参数精确为 `0x7e020011` 或 `0x7c020011`：SIGCHLD 和新的 user/mount/PID/net/IPC/UTS 命名空间，可选 cgroup。不放行任意 clone flags，也不额外允许 clone3。
- `unshare(CLONE_NEWUSER)`：bwrap 的第二层 UID 映射，不能加入已有命名空间。`setns` 保持默认限制。
- `mount`、`pivot_root`：在新命名空间内构造文件系统；seccomp 不能检查指针指向的路径，实际对象权限继续由内核的 namespace/capability 与 bwrap 配方约束。
- `umount2(..., MNT_DETACH)`：拆除旧根；其余 flags 不额外允许。

必须同时使用非 root 的工作区 UID/GID、`cap_drop: ALL`、`no-new-privileges:true`。没有给外层容器 SYS_ADMIN，没有移除整个 seccomp 策略。新增 namespace/mount 系统调用仍增加内核可达面，需要维持宿主内核安全更新。runner 在执行用户代码前清空 capabilities，只挂载当前会话，并为每次调用探测完整配方。

本机 Docker 29.1.3 / Linux amd64 的默认 seccomp 会拒绝 bwrap；此 profile 已通过目标 Debian bookworm 镜像 bubblewrap 0.8.0 的实际会话/网络/交付隔离测试和办公格式转换。此宿主未启用 AppArmor；启用 AppArmor/SELinux、禁止 userns 的宿主必须独立验收，失败时维持拒绝，不能切换 unconfined、privileged 或 unsafe-dev。s390 的 clone 参数顺序未适配，严格模式会拒绝而不会无隔离执行。

复现入口：`bash tools_server/test-image.sh`。该脚本使用临时假账号、临时工作区和独立测试镜像，不读取 .env，不传真实凭据，不调用模型，不修改运行中的服务。
