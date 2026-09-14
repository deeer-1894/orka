# Backend session workspace isolation — verification report

Completed against the approved 2026-09-14 design. No commit, browser operation, service restart, or container recreation was performed by this task. Model configuration and frontend changes belong to the parallel work.

## Implemented

- Workspaces resolve to `{userRoot}/sessions/{conversationID}`. Missing or malformed owner/conversation context fails closed; existing files at the old user root are preserved and are not exposed through a session fallback.
- File APIs accept `conv` in the query and `conversation_id` in JSON/multipart bodies, rejecting conflicts. Conversation ownership/shares are checked before storage access. Owners/editors can write; viewers can read; unknown, unauthenticated and unauthorized requests fail. The persisted owner determines the root for shared conversations.
- Upload, list, download, URL generation, delete, version history and restore use authorized session roots. File API operations use `os.Root` directory handles for containment; deletion cannot target the session root. Generated URLs encode both the conversation and file path.
- Chunk uploads and progress are keyed by authenticated caller + conversation + upload ID. Index/total and filename consistency are validated; assembly happens under the manager lock.
- Signed MCP tokens include `ConversationID`. Gateway identity ignores unsigned identity/conversation headers. Connection pool entries are bound to owner + conversation, and owner invalidation retires all corresponding entries while preserving active leases.
- Local filesystem fallback, gateway file/office/memory/version tools, shell/Python working directory and HOME, attachments, offload/research archives and delivery checks all use the session context. Python script paths retain subdirectories.
- Quant harnesses, compute caches and report inputs are session-scoped. The pipeline discovers reports in an authorized source conversation and copies the selected report into each new report conversation. The logical factor and portfolio library intentionally remains per user.
- Forks copy current regular files into an independent new session, using rooted handles; symlinks and special files are skipped. Copy failure removes the newly created partial workspace. Forks snapshot current files, not historical file state at the selected message.
- Inline Markdown local links in artifacts become authenticated downloads for the source conversation; cross-conversation local download links are rejected. Artifact ownership is checked on local publish/get. Public artifact sharing does not grant access to workspace bytes. HTML blocks remain self-contained; resource snapshots, reference-style Markdown link rewriting and relative resources inside raw HTML are not implemented.
- Chat run, attach, cancel, resume, confirmation and task creation/scheduling validate conversation access. Blocking confirmation IDs must belong to the authorized conversation.
- Fixed an execution-status defect exposed by stricter context validation: tool discovery errors no longer mark a successful tool-free answer as failed. Regression tests separately exercise this degradation case.

## Permissions and isolation boundary

Workspace creation uses directory mode `0775` and file mode `0664` to preserve write masks inherited from the deployment's default ACL for host UID 1000 and container UID 10001. Existing data permissions and mount ACLs were not changed.

A live probe used the actual Go `EnsureSession` helper under the mounted storage base. Container UID 10001 successfully appended to a host-created file and created its own file/directory; the host successfully appended to the container-created file and wrote inside the container-created directory. Probe data was removed afterward. A comparison probe confirmed that `0755` masks out container write access on this deployment.

This is conversation root selection and file API containment, not a new OS sandbox. Shell/Python can still address the tools container's entire existing mounted workspace using arbitrary code. The native quant runner retains its existing host process model. External third-party MCP connectors retain their own server-side access boundaries.

## Verification

All commands completed successfully on the final source:

```sh
go test ./orka_core/... ./orka_middleware/... ./tools_server/... ./orka_control_layer/...
go vet ./orka_core/... ./orka_middleware/... ./tools_server/... ./orka_control_layer/...
go test -race ./orka_control_layer/service ./orka_control_layer/api ./tools_server/server ./orka_core/pathsafe -run 'TestSession|TestGatewaySession|TestCopySession|TestReduction|TestRunAccountingFastPath' -count=1
git diff --check
CGO_ENABLED=0 go build -o /tmp/orka-session-tools ./tools_server
go build -o /tmp/orka-session-backend ./orka_control_layer
```

Logs: `.run/session-backend-full-tests.log`, `.run/session-backend-race-tests.log`.

The tools binary is statically linked; the backend binary is a native Linux executable. Both include this task's final production changes. Deployment/restart and live browser E2E remain with the user; any image built before these final changes must be rebuilt or supplied with the final binary.
