# Chat recovery and session files: design and implementation plan

User-approved scope: only web/, no backend edits, browser/server operations, dependencies or commits.

## Design

Recovery uses a conversation-scoped current-run identity (message run_id, otherwise exact trace_id), never the most recent resumable historical run. Query all latest conversation runs, verify the identified run is also the latest, and recheck both latest-record and single-run APIs before resumeRun. Query versions fence late results; an immediate claim lock prevents double clicks. A successful resume attaches to its returned, validated conversation with no new user prompt. A refreshed conversation derives failure from persisted task events/API state. Ordinary rerun sends the current conversation's latest user message, clearly labelled separately.

Files use explicit, successful write tool outputs, current plan outputs, completed assistant Markdown file links, or labelled shell/Python output paths. Reasoning, streaming text, read/list output and generic filename mentions are not provenance. Normalize complete paths without basename aliases; list only the candidate parent directories, and intersect exact paths with actual non-directory entries. Absolute workspace paths require the current owner's exact storage path segment; other absolute paths are omitted. Shared conversations cannot use the viewer's own file-list endpoint. Conversation changes and new evidence invalidate old listing responses.

Prefer small pure modules plus thin hooks over embedding asynchronous identity logic in Thread or using a global unscoped runs cache. No extra frontend test framework: compile pure TypeScript with installed tsc, execute Node built-in tests.

## Implementation sequence

- [x] Write failing tests for current-run selection, refresh, stale requests, single-claim recovery, exact file paths and positive provenance.
- [x] Implement runRecovery/sessionFiles pure modules and thin hooks. Exercise deferred promises for races.
- [x] Wire Thread, App and API types; fence superseded SSE/history updates and separate resume from rerun.
- [x] Run npm test and npm run build; review web-only diff and record validation.

Important boundaries: legacy identity without run_id or trace_id fails closed; paused confirmation remains separate. Current plan declarations may name already-existing files, but never choose an identically named file in another directory. File existence does not prove content correctness. API/transport errors never silently turn recovery into a fresh run. Missing metadata or a delayed successor cannot reoffer an already accepted predecessor.

## Validation and settlement boundaries

`npm test`: 35 Node tests pass; `npm run build`: passes (existing large-chunk warning). Red-first evidence retained locally in tests/red*.log, final results in tests/green.log and tests/build.log; logs and temporary compilation directories are ignored. No browser or backend restart was performed.

Live task partial stops the streaming UI immediately; persisted partial is restored on reload. A task done event does not hide a matching database partial run. Polling and run events observe resumable flags saved after the terminal event. Resume requires the latest matching run plus fresh list/get preflight. Token exhaustion blocks continuing because the cumulative allowance survives; per-attempt step/time limits defer to the backend resumable flag and API. Ordinary done does not show the failure banner.

Coverage includes late queries, double clicks, conversation switches before/after acceptance, wrong returned conversation, reload attachment, delayed settlement, clarification pauses, exact path collisions, failed writes, deep directories, current plans, explicit script output, and reasoning exclusion. Hooks and UI are build-checked; browser interaction testing was explicitly excluded.

## Focused P2 follow-up

Six new regressions failed before these fixes (tests/red-review-p2.log); all 35 tests and the build now pass. Automatic attachment tracks the connection promise, retries transport failures after 3s/6s backoff, and stops after three rounds per run. Structured success=false/error receipts and explicit refusals cannot claim existing files. Late history merges by ID during and after streaming, keeps live execution status, pairs optimistic echoes one-to-one within their submission window, and preserves earlier repeated user requests. Stream generations fence queued state updates independently of AbortController cleanup. No changes outside web; G audit preparation remains paused.
