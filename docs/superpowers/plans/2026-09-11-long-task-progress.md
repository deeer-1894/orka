# Long Task Progress Implementation Plan

> **For agentic workers:** Use subagent-driven-development or executing-plans task-by-task, with focused review of each changed module.

**Goal:** Reduce model stagnation and recover useful long-task progress with accurate delivery UI.

**Architecture:** Service owns policy/status; llm owns wire protocol and accepted responses; frontend owns run-specific recovery and artifact association. Independent agents own web and service while the main agent owns llm and integration.

**Tech Stack:** Go/Eino; React/TypeScript; existing Python stdlib evaluation harness.

## Global Constraints

- Only codex/long-task-optimization; preserve all existing F evidence.
- No unbounded retry, deadline extension or execution of truncated tool calls.
- No hardcoded inventory formulas in runtime; semantic quality is measured by independent oracle.
- Reuse existing run checkpoints and cumulative budgets.

## Task 1: Model protocol and execution policy

Files: llm/llm.go, llm/eino_model.go, llm/openai.go, llm/call_limits.go; service/model_call_limits.go and their tests.

- [x] Add a transport regression feeding an assistant tool-call response with reasoning through Eino, then capture the next HTTP payload and assert reasoning_content survives with the same call ID; no tool/user reasoning leakage.
- [x] Run targeted Go test and record expected missing-field failure.
- [x] Add optional reasoning effort at request/call policy boundary; forward only nonempty parameters. Keep accepted reasoning on assistant messages through conversion.
- [x] Set low only for supported DeepSeek V4 execution models; prove unknown model omission, model override correctness, unchanged token/deadline behavior and truncation isolation.
- [x] Run llm/service model policy tests and race checks, review diff.

## Task 2: Progress guidance and recoverable limit outcomes

Files: service/adk_chat.go, service/research_guidance.go and focused new helpers/tests (agent Hegel).

- [x] Reproduce call-limit with tool progress and require partial plus honest unfinished items; cancellation and zero-progress controls.
- [x] Implement classifier/response using existing journal and delivery state; preserve error details, usage and resume behavior.
- [x] Add replaceable bounded runtime guidance for incremental work and early independent business assertions; test repeated rewrites and summary provenance.
- [x] Run service tests/race, report limits of guidance versus structural validation.

## Task 3: Resume and artifact UI

Files: web/src/App.tsx, components/Thread.tsx, small pure helpers/tests (agent Kuhn).

- [x] Reproduce cross-directory basename collision and current-run resume selection with deterministic tests.
- [x] Connect resume to existing API with duplicate-action and stale-response protection; keep explicit rerun distinct.
- [x] Match only supported current-session artifact claims to existing exact paths, include current plan outputs, exclude uncreated claims and thinking-only mentions.
- [x] Run frontend tests, typecheck/build, review async behavior.

## Task 4: Integration and G evaluation

- [x] Review independent diffs and run all appropriate module tests/vet/race plus frontend build.
- [x] Build exact reviewed backend, preserve environment, restart only that backend, confirm health.
- [ ] Submit F prompt with only G identity/new directory replacements via logged-in browser. Freeze production code while benchmark runs.
- [ ] Capture terminal run and telemetry; compare every business field against existing independent oracle, audit required files, verify/reproduce isolated copies, inspect UI.
- [ ] Record actual outcomes and unresolved limitations; commit reviewed changes only on current branch.

## Review-driven integrity checks

- [x] Reject incomplete SSE/JSON responses without exposing partial tools or reasoning; preserve parsed usage on rejected responses.
- [x] Feed bounded structural diagnostics for changed declared JSON/CSV/SVG/HTML files into mutating tool observations; defer undisplayed failures instead of caching them.
- [x] Cover malformed SVG dimensions, actual HTTP tool-roundtrip reasoning, concurrent model overrides and short response bodies.
- [x] Close cancellation-during-final-check and decorated failed-tool checkpoint recovery review cases.
