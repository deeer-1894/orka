# Long-task Delivery Implementation Plan

**Goal:** Improve long-task completion reliability and measure the resulting complete delivery.

**Architecture:** Retain the Eino runner. Add independent artifact checks and recoverable execution state at its existing plan, finalization and journal seams; keep file validation separate from runtime policy.

**Tech Stack:** Go 1.25, Eino 0.9.19, existing native backend and Docker tools server.

## Constraints

Current branch only; no unrelated changes; same model and 800,000-token evaluation budget; new test conversation and output directory; do not mutate earlier evaluation outputs. Structural validation cannot certify business correctness.

## Execution checklist

- [x] Model adapter: add request capture tests for Generate, streaming and fallback; observe ignored MaxTokens failure; forward Model/Temperature/MaxTokens with request-time precedence. Add an actual summary middleware regression for bounded generation and truncated-summary fallback.
- [x] Plan and delivery: reproduce omitted pending steps; preserve original steps while accepting progress and additions. Add additive output requirements, bounded workspace-safe file validation, a visible check_delivery tool, and final fresh checks. Cover missing/empty files, duplicate CSV columns, malformed image references, invalid JSON/XML, ZIP corruption, symlink escape and changed files.
- [x] Recovery: reproduce partial journal removal; persist and restore plan/output requirements/cumulative tokens; retain completed parallel tool results and insert explicit unknown-result receipts for interrupted branches. Test atomic concurrent flush, failed-write retry and no token-budget reset.
- [x] Execution guidance: expose remaining allowance and timely generation/verification/packaging priorities. Test guidance after history replacement and enforce the existing retrieval cap.
- [ ] Validate: run focused tests first, then `go test ./orka_core/... ./orka_control_layer/... ./orka_middleware/... ./tools_server/...`, affected race checks and vet. Inspect final diff before commit.
- [ ] Evaluate: rebuild/restart only the backend, verify health, run the fixed long prompt with test ID D and a new output folder, archive events and telemetry, then run independent audits and record outcome and limitations.

Regression implementation proceeds test-first at each seam. The prior three overlay probes provide the initial failing examples; permanent tests also cover the public tool/runtime paths and compatibility.

Review expanded recovery coverage to clarification/confirmation, inherited crash state, full delegate archives, unknown tool outcomes and failed durable handoff. File/offer write failures fail closed; they must never restore an older spending allowance.
