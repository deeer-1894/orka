# Long-task recovery and refund audit — 2026-09-14

## Runtime changes

- Research admission and tool visibility now use the same cumulative 75% token boundary. The last quarter remains reserved for delivery. The 40-call research limit and 800,000-token task allowance remain unchanged. Checkpoints retain consumed usage and reserved calls; cached and local evidence remain available after remote admission stops.
- `update_plan.final_response` supports `answer` (default) and explicit `file_receipt`. The latter is only for file/link/brief-acceptance requests; substantive findings and limitations belong in the files. Main-agent content is buffered until final vs tool progress is known. A completed plan plus fresh structural file checks produces a deterministic receipt, shared by SSE, stored chat, run output and journal. Normal chat, delegates, unfinished work, cancellation and exhausted-budget responses retain their existing behavior. This does not establish semantic correctness.
- Unknown tool names now return a failure receipt via Eino's native handler. No alias is guessed, no rejected arguments execute, and valid sibling calls can proceed. All four agent builders use this behavior. Cancellation still propagates; the diagnostic does not echo arguments and bounds the tool name.
- Auxiliary titles reject incomplete responses, structured tool calls and recognizable DSML/XML tool-protocol text, preserving the original snippet instead of exposing a protocol token as the conversation title. No extra model retry is added. Existing historical titles are preserved.

## Real executions

All used the logged-in test account and actual `deepseek-v4-pro`; no fixture model responses. Raw captures and unchanged model-generated artifacts remain under `.run/` (ignored).

| Case | Run | Duration | Tokens | Tools | Recorded outcome |
| --- | --- | ---: | ---: | ---: | --- |
| Same SLA task, new research boundary | `run_6500ee11699defd1c4381503` | 541.775 s | 695,132 | 42 | failed: nonexistent `read_file` |
| Resume existing SLA work after unknown-tool fix | `run_fadd6d3bdb3470b4dc9ba908` | 90.611 s | 106,654 | 12 | partial: cumulative token allowance |
| Different task: 180 simulated refund orders | `run_a96c816ab2879cd949fbe2ca` | 150.282 s | 202,151 | 18 | done |

The SLA attempt successfully read the previously denied rate-limit documentation. It produced seven files before the unknown-tool error; this error was reproduced in real-runner tests and fixed before resuming. Recovery reused the same session and artifacts. Its combined 801,786 tokens crossed the existing limit at a completed generation; it was not granted a fresh allowance or relabeled successful. The SLA prompt did not select `file_receipt`, so that run does not validate automatic adoption of the new mode. The refund prompt explicitly requested it and the real final response used it.

These cases are not equivalent complexity. The refund duration is not a performance improvement percentage over SLA.

## Independent results

SLA: all seven files downloaded. Independent oracle comparison confirms 193 raw = 163 valid + 30 exceptions, exact exception row/reason assignments, retained records, durations, breach flags and all priority summary fields. Independently rerunning `audit.py` yields byte-identical CSVs. Report statistics match the oracle, including P0 38/43 and T0068 closed at exactly 72h without breach.

Refunds: all five required files downloaded. All nine `(month, channel)` groups match expected aggregates computed before execution. The input has 13 cancelled and 167 paid orders; paid full refunds = 15, partial refunds = 22. Totals are gross 1,743,492 cents, refunds 232,518, net 1,510,974. An independent execution of `audit.py` produces byte-identical `summary.csv`. All four manifest SHA256 entries match the downloaded files; the timestamp falls inside the run, and the manifest does not hash itself.

Browser: actual final links resolve through the session-scoped download API for conversation `521699cf1a08f098487993a0`. CSV preview displays all nine groups. Offline dashboard rendering has no JavaScript errors, one SVG with 27 bars, common scales, and no page-level horizontal overflow at 1366px. The screenshot was visually inspected.

## Remaining observed problems

- Refund report section 7 says group order counts are “17~19”; the actual range is 18–19. Structured data and totals are correct. A deterministic final receipt prevents a second chat narrative but cannot fix false prose inside the report.
- SLA source prose still needs semantic scope checks: `sort` versus `direction`, unsupported “most endpoints” generalization, project-issue scope for GitLab keyset claims, Self-Managed scope and periods for rate-limit defaults. The report also misnames Link pagination as rate limiting. Actual page retrieval alone does not validate every claim.
- On recovery, `search_evidence` returned an empty in-memory index even though original evidence files survived. The model recovered by reading those files. Rehydrating evidence indexes would avoid this detour; it is not implemented in this change.
- The first SLA verifier made an incorrect assumption about an exact-threshold sample, then repaired its assertion. Repeated verification and large source reads remain measurable budget costs.

No generated report was silently edited to make the evaluation pass. These are reliability limits, not successful semantic validation.

## Verification

- Full tests and vet across all four Go modules.
- Race checks for delivery response, unknown tool handling, research admission, title validation and checkpoints.
- Red/green real-runner tests for the observed wrong-statistic response and unknown-tool failure; default-answer, pending/missing/mutated files, cancellation, budget, URL escaping and serialized recovery coverage.
- Independent code review of receipt mode and publication boundaries; post-file-check cancellation/budget recheck added after review.
