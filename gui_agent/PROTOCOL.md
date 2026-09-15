# GUI execution contract and implementation plan

Goal: isolate GUI state by trusted owner and conversation; retain a visible active window, bounded waiting, cancellable model calls and actual usage.

Only gui_agent/** and connectors/gui*.go are in scope. No deployment or commits.

## Trusted control-layer integration

`agent.MetaFrom(ctx)` supplies trusted `UserEmail`, `ConversationID`, `RunID`. For adapters without Meta, `connectors.WithGUIIdentity(ctx, GUIIdentity{OwnerID, ConversationID, RunID})` attaches identity outside tool arguments. Meta is authoritative when present. `modelprofile.FromContext(ctx)` supplies the current run snapshot (`Protocol == modelprofile.OpenAICompatible`). The connector maps it to `GUIModelConfig{BaseURL, APIKey, Model, VisionVerified}` only at the trusted transport boundary. Missing identity/configuration fails closed; environment credentials are never fallbacks.

`llm.ReportExternalUsage(callCtx, llm.Usage)` forwards each exchange exactly once into the external-call collector; the final tool JSON also contains totals. When call accounting is installed, it settles the reservation without charging the parent legacy usage sink again. Without that hook, the existing usage sink remains supported. Unknown usage is not zero usage. Before dispatch, `llm.BeginExternalCall` reserves `MaxSteps * (32768 + effective policy.max_tokens)` tokens (4096 output tokens by default). Every exchange reports against its returned callCtx; the detached finish callback settles once and propagates accounting failures. Incomplete/unknown calls retain their conservative reservation.

Authenticated private-network WS request:

```json
{"type":"run","session_id":"unique-tool-call-id","identity":{"owner_id":"fake-owner","conversation_id":"conversation","run_id":"run"},"model_config":{"base_url":"http://provider.internal/v1","api_key":"<memory-only>","model":"selected-model","vision_verified":true},"instruction":"task","max_steps":10,"queue_timeout":30,"execution_timeout":90}
```

Configuration must never appear in logs, browser frames, summaries or persisted macros. WS requires a shared bearer token and a private/loopback transport peer; browser-origin requests are rejected. The caller cannot select CDP endpoints. GUI_PLANNER is the sole input-mode setting (vlm by default; llm, uitars, rule are explicit). Visual modes require vision_verified. No automatic fallback to another mode or model.

Browser contexts live in memory, keyed by (owner_id, conversation_id), including cookies, local storage and current page. run_id scopes execution and accounting, not browser storage. The pool retains at most 16 contexts and expires contexts idle for 30 minutes on the next acquisition; session frames following started explicitly state whether the session resumed. Restart/eviction loses browser state; no storage-state files are written. bring_to_front selects the active context's visible page. Access control for the shared noVNC display is outside this transport's scope.

Queue frames: queued, started, error(phase=queue), done(outcome=partial, phase=execution). One invocation executes at a time, with at most 8 waiting invocations. Defaults are 30 seconds queue time and 90 seconds execution time. Each deadline includes setup in its respective phase. Client disconnect cancels waiting/execution and releases its slot. Terminal outcomes retain evidence and usage.

Usage frame: type=usage, call_id, model, known, prompt_tokens, completion_tokens, total_tokens. Provider-absent values are null/unknown, never estimated. Totals include known receipts with unknown_calls separately reported. A provider call cancelled before returning usage is unknown.

Automatic macro replay and disk macro recording are disabled in the execution path, including legacy persisted macros. Old requests without trusted identity/model configuration are intentionally rejected.

## Implementation sequence

- [x] Red tests for identity/auth, isolated contexts and continuation, bounded queue/cancellation, provider cancellation/usage, no credential fallback/persistence.
- [x] Session pool and execution queue; connect them to WS run lifecycle.
- [x] Explicit ModelConfig, one async provider adapter and usage ledger; one planner input mode.
- [x] Trusted connector identity/model transport and separate deadlines; usage callback and final evidence.
- [x] Run real-browser fake-account isolation, mock-provider usage/cancellation and Go connector regressions; review interfaces and limits.

## Runtime and validation handoff

`GET /health` returns HTTP 200 with `{"status":"ok"}`. This is liveness only: it does not start Chromium, verify a model account, or certify authentication/configuration readiness. Execution uses `/api/v1/exec/gui/ws` with the bearer/private-network requirements above. No compose, main, config or web changes are included.

The existing `orka-gui:flash-async` image supplies Python 3.12 at `/usr/local/bin/python`, FastAPI, LangGraph, OpenAI SDK, Playwright and Pillow. Its Chromium executable is `/root/.cache/ms-playwright/chromium-1234/chrome-linux64/chrome`; this image-specific path is diagnostic, not hardcoded in the implementation. Headful operation uses the existing Xvfb + fluxbox stack; x11vnc/noVNC/websockify expose the shared display. The Dockerfile already installs these requirements; no additional production package was introduced. Dependencies remain unpinned as before, so rebuilding can resolve different versions.

No host GUI virtual environment was created. The host `/usr/bin/python3` lacks GUI dependencies, and the existing image lacks pytest. The container interpreter path must not be used as a host `GUI_PYTHON`. The offline unittest command is:

```sh
docker run --rm --network none -v /home/aibox/orka/gui_agent:/app:ro --entrypoint python orka-gui:flash-async -m unittest discover -s tests -p 'test_*.py' -v
```

Final validation on 2026-09-15:
- Full unittest discovery: 40 tests, 39 passed and the headful-only case skipped.
- Separate Xvfb + fluxbox run of `test_runtime.BrowserSessionTests`: all 4 passed, including visible active window/screenshot, same-session state continuation, different owner/conversation cookie and localStorage isolation, bounded eviction and protection of an existing external context. Tests use fake accounts in a shared real Chromium process, never user cookies.
- The 15 legacy pytest-style UI-TARS test functions passed using an offline function runner (they are not discovered by unittest).
- `go test -race ./connectors -count=1`: passed, including reservation refusal before dispatch, actual-usage settlement, no duplicate legacy billing, incomplete usage, accounting error propagation and cancellation after dispatch with retained action/usage evidence and unknown settlement. Execution-scope tests reject foreign/untagged frames and verify that 80 buffered screenshots do not reach SSE after cancellation; Python callback tests cover both normal completion and cancellation.
- Provider tests exercise the actual async SDK against an in-container fake HTTP provider for both planners. No remote model tests, service restarts or deployment occurred.

Compatibility: callers must provide trusted identity and the selected model snapshot, plus a shared GUI bearer token. Missing configuration and old unauthenticated requests now fail closed. Browser state lasts only while its context remains in this process; eviction/restart yields `resumed=false`. Isolation applies to browser state and authenticated control requests; access to the shared noVNC display still requires the outer web/proxy access policy. The GUI result summary remains a model claim, not acceptance evidence.

## Execution-scoped screenshot SSE

Every executor frame after request validation carries both `run_id` (trusted execution ID) and `session_id` (unique GUI invocation ID). The connector rejects missing/mismatched scope before buffering, forwarding or usage accounting. SSE browser messages carry the trusted Meta (`run_id`, `conversation_id`, owner and existing trace metadata); payload scope must agree. Screenshots use `type=browser`, `action=screenshot`, `payload.data` as base64 PNG. Action receipts use `action=action` and the existing evidence payload.

Cancellation closes the WS, joins the connector reader and discards buffered screenshots; only matching action/observation evidence and usage are retained for the partial result. Old Python graph emit callbacks stop writing after completion/cancellation, including on a reused WS. Previously delivered client events are not recalled by this transport: the UI must scope its screenshot state to the active conversation/run and clear it on cancellation/new execution. The shared noVNC iframe must not be the default per-user view.

Compose may set `GUI_PLANNER=vlm`, `MACRO_ENABLE=0` and require `GUI_AUTH_TOKEN`, while removing `OPENAI_*`, `MODEL`, and `VLM_*` credentials/settings from the GUI environment. No model/account environment fallback remains. Both control layer and GUI need the same nonempty service token. The selected request model must have verified vision for vlm. The existing headful display/runtime settings remain needed for visible browser windows.

## Multi-stage task observations and trusted provider policy

The 2026-09-15 live run demonstrated a contract gap, not missing screenshots. Its first GUI invocation executed navigate/read/type/read/apply/reset/type/apply/read/reset, with three DOM reads in ten actions. The existing stop rule only detects three consecutive identical actions, so it did not match that cycle. Later read-only invocations extracted the current Canvas values; one returned all changed values in a single done response. VLM previously received only the current screenshot and bounded action/DOM evidence, and had no field to save earlier visual readouts before changing views.

SoM action objects may now include one optional checkpoint:

```json
{"action":"click","mark":2,"progress":{"goal":"initial reading","status":"complete","observation":"Observed value 17; unit uncertain"}}
```

`goal` is a stable task-phase label; `status` is pending/complete/blocked. The checkpoint describes the current view BEFORE the accompanying action. The graph supplies `observed_step`, `observation_seq` and `screenshot_sha256` (SHA-256 of the exact base64 screenshot string sent in SSE); it never accepts a model-supplied source. An unchanged observation retains its original source when its goal status is updated. This is a model claim, not verified browser output or acceptance proof. Old marks/positions are never replayed as current targets.

`task_memory` holds at most 8 latest goal records, with labels limited to 96 characters and observations to 1800 characters (plus an explicit truncation marker), and reports omitted goals. It is independent of the 24-event execution evidence window. The next planner input contains this memory and the current screenshot. Read is explicitly DOM-only; visual readouts must come from the already provided screenshot and be saved before leaving a view. Done should return requested findings and uncertainty, including earlier phases.

A scoped `progress` WS/SSE frame carries the current `task_memory` snapshot. Normal, partial, error, timeout and cancelled connector results retain the latest received memory separately from execution receipts. Memory uses the same input-secret scrubber, ignores checkpoints on sensitive input actions, and re-scrubs final snapshots when later inputs are classified. Go applies a separate field allowlist and bounds. No checkpoint is written to a GUI disk store. Memory belongs to one run_agent invocation; the parent agent must retain returned findings across different invocations. This does not fix parent-agent context compaction. SoM supports checkpoint generation; UI-TARS retains its existing screenshot/reply history format.

The model wire also accepts `policy` with `first_max_tokens`, `max_tokens`, `timeout_seconds` and optional `reasoning_effort`, derived only from `modelprofile.Snapshot.Policy`. GUI normalizes to a universal 4096 output-token and 45-second cap; explicit smaller limits win. The first generation may use a smaller first_max_tokens. Empty reasoning_effort is omitted; explicit values including none pass through. No provider/model-name inference remains. The connector reserves the same effective max_tokens sent on the wire; Python independently enforces the same hard cap. Missing or partial usage sets `llm.Usage.Incomplete` and cannot release a reservation as complete.

New regression seams: graph checkpoint continuity/bounds/privacy; exact screenshot-source attribution; real Chromium Canvas phase changes with an in-container mock HTTP provider; selected policy through both async planners; terminal/timeout/cancellation memory transport; policy reservation equivalence. These tests verify the memory and budget contracts. A short real-model multi-stage acceptance run remains necessary to evaluate model behavior after deployment, under the parent's deployment schedule.
