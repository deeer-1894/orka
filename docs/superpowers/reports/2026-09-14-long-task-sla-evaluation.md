# Long task SLA audit evaluation

Branch: `codex/long-task-optimization`. Previous feature commit: `ea29593` (pushed).

## Workload and independent oracle

The real `deepseek-v4-pro` model received the same Chinese task and synthetic CSV in fresh, isolated conversations. It had to clean timestamped ticket versions, calculate priority SLA metrics, deliver three CSV files, an executable standard-library audit script, an offline dashboard, a report, and evidence from official GitHub/GitLab API documentation. The task required actual script reruns and reconciliation. No model or total run budget increase was made.

Input SHA-256: `ce772607f742e334b3669b5ff6e0b41f06d9fd36631359ce0f8727383fec6cdd`.

An independent calculation found 193 input rows = 163 valid + 30 exceptions. Every exception row/reason and all 21 numeric priority summary fields were compared, rather than accepting the model's own validator as proof. Downloaded audit scripts were also executed independently and their three CSV outputs compared byte-for-byte.

| Priority | Valid | Closed | Open | Closed breached | Open overdue | Closed breach rate | Closed median hours |
|---|---:|---:|---:|---:|---:|---:|---:|
| P0 | 55 | 43 | 12 | 38 | 12 | 0.8837 | 58.00 |
| P1 | 56 | 46 | 10 | 32 | 10 | 0.6957 | 39.50 |
| P2 | 52 | 41 | 11 | 11 | 11 | 0.2683 | 34.00 |

## Baseline and first intervention

| Run | Duration | Tokens | Tool calls | Required files | Recorded outcome |
|---|---:|---:|---:|---:|---|
| Baseline `run_b80fba3571e000ce0446ac1b` | 555.2 s | 842,752 | 28 | 6/7 | partial; token budget; missing report |
| Attachment fix `run_4cdbcbbc2122c685475472b5` | 547.8 s | 784,627 | 54 | 7/7 | partial; original plan steps still outstanding |
| Final backend `run_3f572a53ffc4c6d2f9bf9f25` | 504.8 s | 728,784 | 46 | 7/7 | done; matching SSE; no outstanding steps |

The first model request fell from 15,909 to 6,413 input tokens. This is a single paired evaluation, not a statistical latency benchmark. End-to-end improvement was much smaller than the first-request saving because the second run spent additional calls on research, retries and repeated verification.

## Confirmed issues and changes

- Text attachments were appended to the authenticated human request. Summary preservation therefore kept large raw CSV data as immutable instructions. Attachment previews now have separate runtime provenance; CSV/TSV previews are bounded to 2,048 bytes and text contents share a 20,000-byte request budget. Full session files remain intact. Reads are bounded and UTF-8-safe.
- The model guessed `/workspace`, although shell calls start at the conversation directory. Tool and system descriptions now explicitly share the file-tool root and advise relative paths.
- A real provider response used `finish_reason=tool_calls` while exhausting 8,192 output tokens (6,835 reasoning tokens), returning truncated write arguments alongside a valid plan call. The model adapter now checks the entire batch's JSON before exposing any call, discards malformed batches atomically, and permits one recovery within the original deadline. Discarded attempts remain metered; transient UI content is reset.
- Known documentation URLs were unnecessarily sent to index discovery, which returned unrelated links or no index. Guidance now prefers direct page retrieval and records endpoint, version, authentication and time-boundary scope with API claims.
- Correct CSV statistics did not ensure correct prose or chart rendering. Guidance now requires narrative claims to match the exact source rows, separates observations from causal hypotheses, and calls for a common chart scale and rendered inspection when available.

- The model split/renamed checklist steps and could no longer see the original outstanding obligations because `update_plan` returned only an acknowledgement. It now returns the authoritative cumulative plan and explicitly omitted unfinished items. `check_delivery` separately reports file validity and plan completion. Original requirements cannot be erased by omission or fuzzy title matching.
- Normal completion published SSE `done` before durable finalization detected `partial`. All terminal paths now publish and persist the same outcome snapshot; unresolved obligations produce a partial notice and matching SSE state. Tests replay the real seven plan updates and cover checkpoint restoration, explicit closure and cancellation races.

## Artifact quality findings

The first intervention's CSV values and independent script rerun passed. Its source manifest avoided unsupported precise rate-limit numbers and had four actual official-page fetches. However, the report blurred retained Escalation versions with discarded original-team versions, asserted an unproven data-entry cause, and confused an already-closed population with the open backlog. Its dashboard's flex layout visually distorted some bar proportions. File existence/format checks do not establish semantic or visual correctness; these generated artifacts were preserved as evaluation evidence, not manually repaired to make the run pass.

## Final run and remaining issues

The final run completed all seven files, preserved and closed all twelve cumulative plan steps, and emitted `task done` matching the durable `done` record. The final `check_delivery` showed that only the final acceptance step remained; the model explicitly closed it afterward. Independent data reconciliation and byte-identical script reruns passed again. Rendered dashboard bars now use a common scale and were visually checked. The report no longer confused retained/discarded teams or asserted the previous unsupported data-entry cause.

Compared with the baseline, this run used 13.5% fewer tokens and finished 9.1% sooner. These are single-run measurements with a nondeterministic provider, not a general performance guarantee.

Remaining problems are explicit rather than hidden behind the successful runtime status:

- **Final chat factual drift:** despite correct CSVs and report, the model wrote `38/42` instead of `38/43`, described a P2 median of 34h as exceeding its 72h threshold, and described T0068 as an open record at source row 106. T0068 is actually closed, at source row 74, with elapsed time exactly 72h and breached=0. Source row 106 is T0098. Tool/file validity cannot validate final prose. A next iteration should tie final factual summaries to the verified result representation and check those summaries separately.
- **Research admission depends on earlier non-research spend:** the 50% global-token research cutoff blocked the required GitHub rate-limit page after data processing. This was not an unreachable website. The model disclosed the gap instead of inventing a fetch, but the requested documentation comparison remains incomplete. A next iteration should reserve task-aware allowances for required sources and delivery without increasing the total run budget.
- **Evidence metadata precision:** the manifest's top-level retrieval time reused the analysis snapshot (00:00Z), while its four per-source timestamps correctly record 08:19:47–08:19:53Z. One GitLab sentence treats `all` as a record state rather than a filter option.
- **Auxiliary output quality:** a previous run's generated sidebar title contained a provider protocol marker. Title/follow-up validation is still a separate improvement area.

## Frontend delivery links

Real UI inspection found that final relative Markdown links, such as `outputs/audit.py`, navigated to the web app path rather than the scoped file API. The generic Markdown component now accepts an optional link resolver; the assistant renderer supplies the current conversation resolver using the existing path normalization and download API. External links remain external, and rejected/foreign paths never acquire a scoped download token. Browser regression reproduced the old wrong URL and verifies corrected targets, Unicode filenames and conversation switching. This frontend-only correction was made after the final backend run and does not affect its measured tokens or duration.

## Frontend measurement

An isolated headless replay loaded the real hook and Thread with 6,200 captured SSE events. Burst and 20x-timed replays matched expected messages and reasoning; no browser errors or tasks over 50 ms were observed. Maximum React render duration was 4.0 ms and 2.6 ms, with at most 23 messages / 199 DOM nodes. Reset/final replacement cases passed synthetic boundary tests. This tests the hook/Thread replay, not the entire user's live browser.

Local raw captures and artifacts are under `.run/long-task-sla/`, `.run/long-task-sla-fixed/` and `.run/long-task-sla-final/`; they are intentionally excluded from Git. The repository contains regression tests for attachment provenance, UTF-8 and budget boundaries, repeated summaries, malformed response rejection, bounded recovery and actual agent tool execution.

## Verification

All four Go modules passed `go test` and `go vet`. Relevant service/API and LLM race checks passed, along with independent code review of the attachment, response-integrity and terminal-state changes. Frontend: 47 unit tests, 7 browser tests and the production build passed. The browser link regression was observed failing before the renderer fix and passing afterward. Backend and frontend are running locally with the updated code; the tools container includes the conversation-directory guidance.
