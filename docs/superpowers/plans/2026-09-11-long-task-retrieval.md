# Long-task retrieval implementation plan

**Goal:** Reduce repeated research and preserve usable evidence so long runs can reach execution and verified delivery.

**Architecture:** Independent HTTP document tools, a run-scoped evidence/retrieval module, and metered run accounting. Existing BaseTool and Eino middleware interfaces connect them.

**Tech stack:** Go, Eino ADK, MCP, existing filesystem backend; no new hosted services.

## Constraints

Only commit on `codex/long-task-optimization`. Preserve approval behavior and workspace isolation. Tests use local fixtures; live validation uses a new task directory. No unrelated refactoring.

## Tasks

- [x] Document tools (tools_server): write failing httptest cases for markdown/sitemap discovery and targeted reading beyond the old truncation boundary; implement and register `discover_docs` and `read_section`; run tools_server tests and race checks.
- [x] Accounting (control/service): reproduce omitted auxiliary usage and empty default model; use a single final accounting helper with metered usage and legacy fallback; run service tests.
- [x] Retrieval session (control/service): first reproduce global cache leakage via identical requests from independent contexts; replace adapter-global caching with run-scoped deduplication. Add public-interface tests for concurrent calls, errors, evidence persistence/search and budget reservation before implementing.
- [x] Model integration (control/service): attach the session before agent construction, make its tools available through the gate and researcher scope, add bounded live guidance after summarization, and test with the real Eino runner plus scripted model.
- [ ] Verify and evaluate: run all Go tests and vet, inspect diffs for boundary leaks, rebuild affected services, rerun the baseline with a new directory and compare actual artifacts/costs. Record results and commit reviewed changes on the current branch.

Each implementation task follows a failing-test → minimal implementation → passing-test cycle. Commands: `go test ./tools_server/...`, `go test ./orka_control_layer/...`, `go test -race ./tools_server/tools ./orka_control_layer/service`, followed by all four workspace module suites and vet. Final delivery includes commit IDs, verified changes and measured limitations.
