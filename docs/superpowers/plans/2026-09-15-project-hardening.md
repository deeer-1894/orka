# Project hardening implementation plan

> For agentic workers: use subagent-driven-development / executing-plans per independently testable workstream. The user approved the assessment design and all fixes on 2026-09-15; no further design approval is pending.

**Goal:** Implement the full accepted project assessment while preserving Auto/manual model selection and maintainable, cohesive module ownership.

**Architecture:** Run identity/lifecycle, model connection/usage, workspace publication, and tool execution own their respective state. Transport and UI entry points consume shared contracts. Keep existing services; avoid new services solely for code organization.

**Tech stack:** Go workspace, React/TypeScript, Python/FastAPI/Playwright, Mongo/Redis, Docker.

## Global constraints

- Only codex/long-task-optimization. Preserve existing account data and credentials. Do not publish keys.
- Preserve 2M-token / 2h / 300-iteration defaults, make policy configurable.
- A disconnected UI is not a failed execution; partial/paused are not success.
- Normal and fallback tools obey identical contracts. Unavailable capabilities fail honestly.
- Tests use temporary workspaces and fake providers; real account tests are bounded and disclosed.
- Scope ownership below prevents parallel workers editing the same files.

## Workstreams and acceptance

- [x] Runtime: api/chat.go, api/stream_hub.go, service run lifecycle. Unique execution identities, atomic conversation admission and independent workflow step cancellation; buffered stream gaps require snapshot reconciliation. Tests: duplicate start, old finish/new start, stop failure, resume exclusivity, stale cursor.
- [x] Workspace: orka_core shared write/publication module, middleware/local/filesystem, tools_server, api/file_versions.go. One create/append/replace contract, backward readable versions, immutable delivery manifests, isolated code process environment and workspace mounts. Tests: fallback parity, concurrent create, backup restore, hostile relative paths, cross-session dummy file inaccessible, snapshot remains unchanged.
- [x] Frontend: web only. Shared run actions, stopping/error states, per-turn plans, per-session drafts and complete retry inputs, live files/deep references, history pagination/needs-attention filter, defensive model loading, task details in the workbench (persistent chat TaskSummary removed), XLSX preview, merged output entry and advanced feature folding, automatically generated, cached followups (restored by explicit user instruction), lazy heavy panels. Tests through real component entry points and simulated API/browser.
- [x] GUI: gui_agent + connectors/gui*.go. Trusted owner/conversation/run identity, isolated persistent contexts, bounded queue, separate queue/execution timeout, request-scoped model config, real usage events, asynchronous cancellation for all planners, disable unsafe macro replay. Tests: independent session storage, cancelled queue, usage forwarding, missing vision credentials, no fake success.
- [x] Workflow: service/workflow.go, scheduled_task, api/workflow.go, new db workflow/schedule files. Validate unique names/known dependencies/acyclic DAG, explicit terminal states and parent lifecycle, partial/paused block downstream, atomic schedule claims and non-overlap policy. Tests deterministic graph and concurrent claim behavior.
- [x] Model: modelsettings, service/model_settings.go/model_call_limits.go, llm, core model invocation context. Named profiles with migration, explicit protocol/capabilities and probe, request snapshot and unified invocation metering; remove internal primary/secondary distinction. Tests private snapshots, no secret export, empty/discovery failures, model capability mismatch, budgeted usage.
- [x] Budget: configurable run policy + daily in-flight reservations and usage settlement, shared parent allowance; expose run progress/usage without creating conflicting state.
- [x] Skills/tool identity: per-owner registry and read-only builtins, source-qualified tool IDs/conflict rejection, opt-in quant capabilities, production GUI unavailable instead of mock.
- [x] Acceptance: revisioned requirements/check evidence records, user constraint amendments, task-specific verification distinguishes checked/unverified, immutable manifests bind accepted versions. Integration tests on fixture projects and report aggregation scope changes.
- [x] Evaluation/operations: repair make eval and current conversation scope, fixed fixtures + CI, readiness/version endpoint, scoped process control and reproducible deployment.
- [x] Integration: reconcile frontend/model/GUI interfaces, run go tests/race relevant packages, frontend tests/build/browser tests, GUI tests and two-session smoke tests. Review all changes before commit.
- [x] Delivery: restart updated local stack when compatible, verify account-visible paths, document coverage and remaining external/provider limits; commit only current branch.

Each workstream starts with a failing boundary regression, implements the smallest shared contract that fixes it, and runs its targeted checks. Parent integrates across module seams and records evidence below; no workstream is considered complete based solely on a worker message.

## Evidence log

Checkmarks cover module contracts; final deployment and account-visible acceptance remain separately tracked.

- Full Go suite and go vet passed after integration. Relevant API, MCP, filesystem, acceptance and delivery race suites passed; workflow, scheduler, profile and budget race suites were independently checked. Real Mongo fixtures covered lease contention/expiry, stale release and schedule claims.
- Shared write and publication tests cover fallback parity, old backup names, exact decimal acceptance, inherited resume criteria, immutable snapshot hashes and referenced-input completeness. Final publication validates copied bytes before committing its manifest.
- Strict Docker runner passed real namespace, environment, filesystem isolation and office-format checks, with per-task code selection required. User explicitly approved persistent local CODE_EXECUTION migration after automatic review required clarification; migration completed without broadening default task scopes.
- GUI Python tests and real browser-context isolation passed; authenticated replacement GUI container is running. Real GLM-5.3-Flash text, image and tool probes all passed on 2026-09-15.
- Initial frontend verification passed 56 unit and 40 simulated browser cases plus production build. Additional session recovery, layout and style changes are being verified against the user's latest directions below.
- A real stop/start exposed TIME_WAIT false occupancy and transient pre-exec identity capture in the launcher. Regressions were added and independently passed (9 launcher tests); strict live-listener and process identity checks remain enforced.
- Live NimbusAudit task and its repair/packaging rounds completed under real@test.com with glm-5.3-flash. Independent XLSX/ZIP review drove additional repairs; final run_555771d7e0a8988bbd63f2bb published17 files with13 acceptance assertions. See the final report for exact scope and limitations.

## User refinements during validation

- Move chat budget controls and execution statistics to the existing workbench/metrics entry; do not introduce duplicate statistics panels.
- All new actions must match the existing compact, icon-led, thin-border pill style (the existing “查看” control).
- Remove the newly introduced “生成追问建议” button and explanatory row; restore automatic followups after the answer, with caching, isolation and accounting retained.

## Final integration

Frontend63unit/54browser/build, Go tests/vet/eval, GUI69Python+15UI-TARS, and11launcher/script tests passed. User refinements, explicit CODE_EXECUTION migration, real account tasks and deployment completed. Final evidence: [验收记录](../reports/2026-09-15-browser-and-hardening.md).
