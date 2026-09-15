# Go Browser Tool Implementation Plan

> **For agentic workers:** Use subagent-driven-development with the ownership below. The user explicitly approved implementation on 2026-09-15 after reviewing feasibility; execute in this branch without another design gate.

**Goal:** Give the main Orka model a built-in Go browser tool for bounded DOM observation and CSS/JavaScript/CDP actions, sharing the existing GUI browser session.

**Architecture:** The Python browser runtime remains the sole owner of Chromium, contexts, pages and the visible execution queue. A private authenticated CDP lease exposes only the current owner/conversation page. Go owns the browser tool, observation format, reference validation, interaction semantics and publication of files; no second planner/model runs inside this tool.

**Tech Stack:** Go 1.25.5-compatible code, pinned cdproto `v0.0.0-20250724212937-08a3db8b4327` (upstream go.mod requires Go 1.23), existing gorilla/websocket, FastAPI/Playwright Chromium, React tool catalog.

## Global constraints

- Work only on `codex/long-task-optimization`; keep ongoing hardening and real-account repair validation intact. No implementation goes into another branch.
- One operation lease spans all of its CDP commands and cleanup. Browser and GUI share the same queue and SessionPool; no lease across main-model thinking.
- Identity comes from trusted execution context, never tool arguments. No root CDP endpoint, default user browser profile, model key, cross-context cookie API or host filesystem access is exposed.
- The user explicitly wants page JavaScript: bounded `evaluate` is supported, subject to the existing risky-action confirmation policy. It is not a code/shell sandbox permission.
- Navigation accepts HTTP(S). Downloads also support blob URLs belonging to the current page; local file, javascript and data navigation are rejected. Page fetch honors browser origin/CORS/CSP rules; failures stay explicit.
- Default queue timeout 30s, execution timeout 45s (max60s); at most256 commands per lease. Public snapshots <=64KiB, <=200 element refs, <=12,000 text characters by default. Evaluation output <=64KiB; script <=16KiB. Files <=16MiB.
- Main-document and open-shadow DOM actions are first-version scope. Frame boundaries are reported; unsupported cross-process frame/popup actions fail explicitly and may use GUI. No fake success for unsupported automation.
- Snapshot refs bind page ID, page epoch, run and snapshot nonce; stale/detached/retargeted refs fail. CSS selectors must uniquely identify an element.
- Screenshot/download output uses the existing per-conversation workspace and shared write contract: create by default, explicit replace with history. No partial files on failure.
- No new top-level UI buttons or panels. Add the tool to the existing picker, with the user's compact pill style preserved.

## Interface contracts

### Private bridge

WS `/api/v1/browser/cdp/ws`, same server token/private-peer/no-Origin authentication as GUI. Acquire fields:

```json
{"type":"acquire","identity":{"owner_id":"owner","conversation_id":"conv","run_id":"run"},"operation_id":"random-id","queue_timeout":30,"execution_timeout":45}
```

Server emits queued then acquired with `lease_id`, `page_id`, `page_epoch`. Each command carries the same identity/operation/lease plus increasing `id`, `method`, `params`. Replies carry matching scope and current page epoch. Release/cancel are idempotent; disconnect runs identical cleanup. One in-flight command per connection. Ordinary private CDP replies are capped at128 KiB; model-visible snapshots/evaluation remain capped at64 KiB. File exports read80 KiB raw-byte chunks (about107 KiB base64) so16 MiB fits within256 commands, including setup and cleanup. Unknown methods, root Browser/Target methods, forged scope, expired or foreign leases are rejected before dispatch.

Allowed methods cover Page enable/getFrameTree/createIsolatedWorld/navigate/captureScreenshot, Runtime enable/evaluate/callFunctionOn/getProperties/releaseObject/releaseObjectGroup, DOM getDocument/querySelector/describeNode/resolveNode/scrollIntoViewIfNeeded/getBoxModel, and Input dispatchMouseEvent/dispatchKeyEvent/insertText. Parameter validation forbids universal access and execution outside the page's approved isolated worlds. Runtime.terminateExecution is cleanup-only. Do not forward arbitrary command names from the model.

### Go transport and engine

Create `orka_control_layer/connectors/browser_cdp_types.go` with these shared contracts (transport owner may refine internal implementation without changing callers):

```go
type BrowserPageInfo struct {
    LeaseID string `json:"lease_id"`
    PageID string `json:"page_id"`
    PageEpoch int64 `json:"page_epoch"`
}
type BrowserLease interface {
    Execute(context.Context, string, any, any) error // cdp.Executor
    Info() BrowserPageInfo
    Close() error
}
type BrowserDialer interface {
    Acquire(context.Context, GUIIdentity, time.Duration, time.Duration) (BrowserLease, error)
}
```

Identity-only extraction is shared with GUI without requiring a model snapshot. `browsertool.New(dialer, baseStorage)` returns an `agent.BaseTool` named `browser`, group `browser`; it is immutable and safe across users. Tool invocation creates one lease and binds a per-invocation artifact writer. The model-facing action enum is `open,snapshot,click,fill,select,press,scroll,wait,evaluate,screenshot,download`.

Relevant arguments are `url`, `ref`, `snapshot_id`, `selector`, `text`, `value`, `key`, `direction`, `amount`, `condition`, `expression`, `path`, `mode`, `timeout_ms`. Reject unknown/irrelevant combinations, conflicting ref/selector, non-finite numbers and over-limit strings. `ref` requires `snapshot_id`; selectors must be unique. Existing default tool discovery and confirmation remain the only user controls.

Results are structured JSON: `ok,action,page_id,page_epoch,url,title,snapshot,files,elapsed_ms` as relevant. Errors include stable codes (`stale_ref`, `ambiguous_selector`, `not_interactable`, `script_error`, `timeout`, `outcome_unknown`, `unsupported_frame`, `output_limit`, `file_exists`) and do not imply rollback. Main-model planning accounts for tokens through the normal run pipeline; browser actions themselves perform zero LLM calls.

## Task 1: Shared browser runtime and private CDP lease

**Owner:** Pauli. **Files:** new `gui_agent/service/browser_runtime.py`, `gui_agent/service/browser_bridge.py`, bridge tests; modify `service/web_socket/server.py` and `operators/sessions.py` only as needed to share ownership and epoch.

- [x] Add a failing async test that holds a GUI lease while a browser acquire waits, then verifies both obtain the same owner/conversation Page without overlapping actions.
- [x] Add forged-scope, root-method rejection, disconnect, expired lease, command/message limit and in-flight cancellation tests. A cancelled or unconfirmed command must be terminated/settled or its page quarantined before queue release.
- [x] Implement `BrowserRuntime.lease(identity, operation_id, queue_timeout, execution_timeout, kind)` over the existing queue/pool; GUI consumes it too. Page reconstruction, main navigation and GUI takeover invalidate refs via epoch.
- [x] Implement the bounded bridge contract above, persistent page CDPSession ownership, parameter allowlist and scope-matched replies. No planner or provider configuration is loaded on this path.
- [x] Run Python protocol/runtime tests plus real Chromium two-session isolation and cancellation. Preserve existing GUI task-memory/policy regressions.

## Task 2: Go CDP transport

**Owner:** Sagan. **Files:** new `connectors/browser_cdp_types.go`, `browser_cdp.go`, `browser_cdp_test.go`; identity-only helper in `gui_context.go`; pinned control-layer go.mod/go.sum updates.

- [x] Write WS fixture tests requiring acquire before dispatch, multiple commands on the same lease, identity and response-ID matching, bounded reads, queue timeout, cancellation and release.
- [x] Add the pinned generated protocol dependency; do not upgrade the project Go baseline. Implement `cdp.Executor` with existing websocket transport and validated envelopes.
- [x] Ensure cancelled/failed side-effecting commands are never automatically replayed. Wait for bounded cleanup acknowledgement or return unknown outcome; do not convert disconnect to success.
- [x] Verify no model snapshot is required using a context containing only trusted owner/conversation/run.
- [x] Run connector tests and race tests against the agreed Python protocol; report a concrete example acquire/command/release transcript without secrets.

## Task 3: Go DOM snapshot and action engine

**Owner:** Darwin. **Files:** new `orka_control_layer/browsertool/engine.go`, `snapshot.go`, `actions.go`, `scripts/*.js`, `engine_test.go`, `snapshot_test.go`; coordinate public types before writes. Tool schema/registration is Task5.

- [x] Test a fixture containing buttons, controlled inputs, password fields, tables, a canvas, open shadow root, frame and duplicate selectors. Expected snapshot includes useful text/roles/states and bounded refs, excludes password values and raw full HTML, and signals omitted/frame/pixel content.
- [x] Implement isolated-world snapshot/ref storage with run/page/epoch/nonce and target fingerprint. Click/fill/press/select validate live node identity and interactability. Return an updated snapshot after changes; action receipts do not assert business success.
- [x] Implement navigation with explicit readiness conditions; wait uses bounded condition polling, never arbitrary long sleep or endless networkidle waits.
- [x] Implement bounded JavaScript expression evaluation and propagate actual exceptions. Do not execute scripts in Go or a host shell. Keep the user's requested CSS/evaluate capability available.
- [x] Test removed/replaced/renamed elements, duplicate CSS, navigation and GUI invalidation, reactive input events, blocked/disabled/covered targets, JS exceptions and output limits through the engine interface.
- [x] Run real Chromium contract tests via the bridge after Task1/2; no provider calls required.

## Task 4: Browser files and binary publication

**Owner:** Tesla. **Files:** new `browsertool/files.go`, `files_test.go`; extend `orka_core/workspaceio/generated.go` only if needed for shared atomic create/replace binary publication.

- [x] Add regression tests for concurrent create, overwrite refusal, explicit replace history, traversal/symlink rejection, cancellation and no partial file.
- [x] Screenshot uses Page.captureScreenshot and writes only validated PNG bytes into the authenticated conversation.
- [x] Download runs a fixed bounded page-fetch helper through CDP: credentials stay in the browser; collect response chunks with a 16MiB hard stop, abort on over-limit, report actual HTTP/CORS/CSP failures. Support same-origin authenticated and page-owned blob exports. Return bytes internally, never base64 in model output.
- [x] Publish via shared workspaceio and return path/mime/size/SHA256. Verify exported bytes and that one conversation cannot choose another root.
- [x] Test actual Chromium cookie-protected and blob downloads, over-limit failure and fixed delivery compatibility.

## Task 5: Builtin integration and existing UI

**Owner:** Zeno (Go service/main); Hooke (web after current UI completion). **Files:** `browsertool/tool.go` and tests; `service/tools_provider.go`, `confirm.go`, discovery helpers, `main.go`; `web/src/lib/toolGroups.ts` and existing catalog tests.

- [x] Add catalog/discovery tests that list browser metadata without opening a page or creating a fake workspace; selected group/name restricts capabilities normally.
- [x] Add provider options for built-in tools, shared across normal and fallback providers. Do not add another mutable package global; migrate GUI injection into the same option where needed and keep caller defaults compatible.
- [x] Wire the browser endpoint from the configured GUI service origin and existing auth token. Missing configuration means an honest unavailable capability; no implicit model or browser fallback.
- [x] Register the single `browser` tool with operation-specific confirmation: mutating interactions, evaluate and authenticated download follow existing risky-action policy; snapshots/waits do not acquire code:execute.
- [x] Redact browser fill text and evaluate expressions from persisted action argument summaries; preserve non-sensitive read-after evidence. Tests verify secrets are not duplicated in browser snapshots/tool display logs.
- [x] Add browser group to existing picker, distinguish DOM browser from visual GUI, preserve current pill style. No new main-page counters, banners or manual followup buttons.

## Task 6: Integration, review and real account acceptance

**Owner:** Parent.

- [x] Finish the ongoing NimbusAudit repair and independent Excel/ZIP/GUI verification before replacing running services.
- [x] Run complete Go tests/vet, relevant race suites, Python bridge/runtime tests, frontend tests/browser tests/build, and strict binary publication checks.
- [x] Use a local deterministic multi-page fixture to prove DOM extraction, ref/CSS interactions, state-changing JS, waits, screenshots, authenticated/blob downloads, stale refs, owner isolation and browser↔GUI handoff. No model is needed for most contracts.
- [x] Rebuild/restart only this project's backend/GUI after no active task, verify protocol readiness, then run a real `real@test.com` task using `glm-5.3-flash` through browser actions and a short GUI handoff. Verify actual downloaded artifacts and operation logs independently.
- [x] Review interfaces and failure paths, document first-version iframe/popup/download-policy limits, complete the evidence log and commit current-branch work. Never claim a returned screenshot or success flag proves all business requirements.

## Self-review and evidence

The plan covers DOM-to-model observations, ref/CSS/CDP actions, page JavaScript, shared GUI session ownership, bounded execution, outputs, model-free contracts and real execution. It deliberately uses a Go protocol layer over the existing browser runtime, not a second standalone Chromium manager. Cross-process frames and popup management are explicit first-version limits rather than silently approximated automation.

Implementation and test evidence will be recorded here as each task completes.

## Final evidence

Implemented and verified through independent Chromium, full Go/frontend/Python suites and a real account Browser↔GUI task. See [验收记录](../reports/2026-09-15-browser-and-hardening.md). Cross-process iframe/popup management and unrestricted cross-origin downloads remain explicit first-version limits.
