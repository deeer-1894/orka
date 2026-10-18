# Long-task report binding, recovery and provider compatibility

## Problems and changes

- The previous refund report stated 17–19 orders per group although the verified CSV contained 18–19. `render_report` now generates Markdown placeholders from bounded CSV min/max/sum/count bindings using exact decimal arithmetic. Declared `.report.json` outputs participate in artifact verification. Modified bound values or edited report text invalidate the report; rerendering repairs it. Unsupported literal placeholders identify their line and snippet. Publication is atomic, confined to the session, and propagates cancellation.
- Restored research sessions previously lost their searchable evidence. Checkpoints now own scoped references including source, capture time, path, byte count and SHA-256. Restore verifies original files and rebuilds the searchable catalog. Corrupt/missing/out-of-scope evidence yields explicit degradation; old checkpoints do not infer provenance by scanning mutable files. Remote-call budget is preserved; request cache is not reconstructed.
- An explicit `render_report` lookup also enabled three unrelated tools. Exact registered tool names now take precedence over fuzzy description search; ordinary discovery remains supported.
- A real provider `AccountQuotaExceeded` response was treated as a transient 429. Shared transport/agent retry classification now distinguishes recognized exhausted-account errors from burst limits, stops ineffective retries and exposes a safe reset-time notice.

## Real run and independent replay

Refund run `run_11b97738bfe57e8692aa0b28`, conversation `fa63d8d3bfd97fe329a25cf1`, used `deepseek-v4-pro` and the previous 180-order input. It failed at the provider quota after 283,944 ms, 147,774 tokens and 13 tool calls. Provider reset time was 2026-09-14 20:30:54 +0800. Progress remains resumable. The planned model-driven corrupt/check/repair sequence did not complete.

The actual generated summary matched the independent precomputed oracle in all nine groups. Its final generated report specification was replayed with production renderer/checker against downloaded copies, without altering the original server artifacts. Results: 167 valid orders, nine groups, minimum 18, maximum 19, gross 1,743,492, refund 232,518, net 1,510,974. Editing the rendered minimum to 17 failed verification; editing a source aggregate failed verification; rerendering restored a valid report. This is an offline replay of real generated artifacts, not a completed model run.

## Validation and limits

Full tests and vet passed across all four Go modules, plus affected race tests for reporting, artifacts, tools, evidence recovery, quota handling and tool discovery. Evidence recovery has serialization, file-integrity, isolation, concurrency and actual ChatService initialization tests with a mock model. Live provider recovery after a process restart was subsequently verified with the newly supplied Ark configuration, as recorded below.

Bindings verify supported aggregate substitutions and report bytes, not business formulas, selection of the right sources, causal prose, literal numbers, or citation scope. Only declared report specifications trigger this contract. Unrelated source-byte changes that preserve all bound values may still pass; this is not source-version locking. Ordinary reports retain ordinary checks.

## Additional provider discovery issue

The user's exact Agent Plan URL (`https://ark.cn-beijing.volces.com/api/plan/v3`) returned 404 on `/models`, while a tiny `/chat/completions` request with `doubao-seed-2.0-pro` succeeded (200, returned model `doubao-seed-2-1-turbo-260628`). The key is not included in source, reports or logs. This distinguishes missing model-list support from an unusable credential or base URL.

The discovery backend now preserves real remote lists and returns explicitly unverified presets only on 404/405 from the exact official HTTPS Agent/Coding Plan endpoints. It never rewrites the URL or probes another billing route; authentication, quota, timeout and redirect errors remain failures. The frontend has separate Agent Plan/Coding Plan presets, retains manual entry and existing ordering, and displays discovery provenance. The 12 candidates were checked against [official Agent Plan docs](https://www.volcengine.com/docs/82379/2366394) and [Coding Plan docs](https://www.volcengine.com/docs/82379/1928261) on September 14. `ark-code-latest` follows the provider console choice, distinct from Orka's Auto-first-entry behavior.

The current official name `doubao-seed-2.1-turbo` also passed a tiny live request. The new key was saved through the user's model-settings API; no key was printed or committed. After rebuilding and restarting backend/frontend/tools, the user's existing logged-in browser successfully fetched all 12 candidates and saved them. Auto defaults to the verified turbo model. Seven model-settings browser tests, frontend unit tests/build, full Go tests/vet and provider/API race tests passed.


## Ark refund run and remaining execution issues

Run `run_0c08ef07bbf03b2190ea7a0d`, conversation `d3040c9932ca601290b22e1d`, completed with the new `doubao-seed-2.1-turbo` profile: **514,552 ms, 622,195 tokens, 37 tool calls**, six declared artifacts, no unfinished plan entries. Independent post-run execution of the delivered audit script reproduced all nine precomputed expected groups; all five manifest hashes matched. A genuine stale-report failure was observed and final rerender/check passed.

The first mutation was a no-op because the model forgot Markdown bold delimiters. It subsequently inspected the report, asserted the exact target string, changed 18 to 17, obtained the targeted stale-report failure and repaired it. Thus the negative test eventually passed, but the extra discovery/edit/check cycle cost time. Named `render_report` discovery enabled exactly one tool in this live run.

This does **not** show a latency/token improvement: this stricter task had more validation steps and used another model. Remaining issues found by independent review:

- The agent ran a separate verifier but did not itself rerun `audit.py` into an independent directory as explicitly requested. That rerun was performed by the external evaluator and succeeded; these are different claims.
- Some repeated counts remained literal template numbers despite available bindings. Current values were correct, but full requested binding coverage was not achieved.
- “Common ecommerce simulation test range” in one observation was unsupported. Numeric bindings do not establish the truth of such qualitative assertions.

## Live research checkpoint recovery

Conversation `76f6475bfcb6baca713e4a91`, initial run `run_41049c991fbb0eee63182bd4`, was deliberately cancelled immediately after three successful official Python-document captures (16,592 ms, 15,158 tokens, four tools). Cancellation retained a resumable checkpoint. Backend/frontend were restarted after the refund run completed; the same research run was then resumed.

Its first tool call was `search_evidence`: all three original IDs and timestamps matched the pre-restart captures exactly. The agent subsequently searched concrete CSV/Decimal/datetime excerpts without fetching the pages again. Captures concerned the fetched Python **3.14.7** documentation and retained that title/version, rather than inventing a version from the current calendar date. The resumed run `run_8fc6ed523a2b79bb4d4e83ee` completed in **235,168 ms, 376,701 tokens and 22 tool calls**, with six declared outputs and no unfinished plan entries. Downloaded `sources.json` retained all three original IDs/timestamps, the entire resumed trace contained zero refetch calls, and independent script execution reproduced all nine expected groups.

An additional invalid-refund fixture exposed a remaining generated-code issue: its audit script silently capped refund 150 to gross 100 and exited successfully. The actual supplied dataset contained no invalid refunds, so this did not alter the verified results. The script is not a general validator for unseen bad data; an execution status of done and correct results on one fixture do not prove that behavior. No generic application heuristic was added to pretend to certify arbitrary generated audit logic.


## Empty-history browser reattachment

The live browser could remain on the welcome screen while a background run was present. The initial history response in that particular browser was not captured, so its precise first-response cause is unconfirmed. Independent controlled reproduction established an actual empty-history gap: the first empty response was permanently marked loaded, and recovery required an existing run/trace message before attaching to an otherwise valid running record.

Empty results are now reloadable on selection. A completed empty-history load can attach read-only to the unique latest running record of that same owned conversation. Pending loads, local sends, unmarked saved user prompts, shared conversations and ambiguous records cannot trigger this fallback. It never resumes or reruns a task automatically. Existing hydration preserves newer streamed messages when late history arrives.

Validation covered empty-to-populated reselect, blank live attachment, pending-history/user-prompt exclusion, late-history preservation, cross-conversation identity and ambiguity, with 51 frontend unit tests and four added browser cases. The existing model-settings browser suite was also rerun against the final App change.
