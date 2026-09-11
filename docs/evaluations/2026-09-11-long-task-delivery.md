# Long-task delivery regression D

Status: D and E independently audited; both FAIL. First-action latency improved; original-requirement preservation was subsequently repaired and tested offline. No end-to-end success claim.

## Configuration and method

- Branch: `codex/long-task-optimization`; implementation commit: `ae1d391d4c4ce750b0529a7e1bad2536fc200700`.
- Model: `deepseek-v4-pro`; summary model: `deepseek-v4-flash`; single agent; configured cumulative token budget: 800,000.
- Same fixed Chinese prompt as A/B/C; only test ID and delivery directory changed to D / `long_eval_20260911_d`.
- Run: `run_996cb79fb8507c5c0bc26f18`; conversation: `261422fbb27e934604c3e4d1`.
- Backend restarted with the preceding process configuration; frontend and Docker tools retained. Backend/frontend health: HTTP 200.
- Full Go suite: 22 tested packages pass. Race: artifacts, LLM and service pass. Vet: all four modules pass. Review findings reproduced offline and repaired before this run.

## Implemented scope

Supported Eino model options reach the wire, including explicit temperature zero. Summaries have an 8192-token output cap and reject truncation. Plan omission preserves outstanding steps; additive output declarations feed read-only structural file checks, repeated at finalization. Recovery preserves requirements, cumulative consumption and full delegate archives across attempts, clarification and confirmation. Unknown tool outcomes remain explicit; storage/offer failures cannot reset an allowance. Live guidance prioritizes production, verification and delivery as budget is consumed.

This is soft stage guidance, not a complete phase scheduler or atomic model-call reservation system. Structural checks do not establish business correctness, citation support, manifest semantics or completeness of model-declared requirements. The independent task audit checks these separately.

## Evidence

Local artifacts under `.run/long-task-delivery`: build.json, prompt.txt, tests.log, race.log, vet.log, review-red.log, latest.json, telemetry.json, and the run capture after completion. Previous delivery directories are read only; copied script execution and mutations remain in the new audit directory.

## D terminal observation

D ended `partial` after 1,232.492 seconds, consuming 831,098 reported tokens (741,793 input; 89,305 output). The budget is checked at call boundaries, so it can overshoot 800,000; per-call output limits do not constitute token reservation. There were 102 tool calls and 23 output files. `manifest.json` and `deliverables.zip` were not produced. Reports and README exist; correctness is audited independently, not inferred from the model's final message.

The first model call took 506.1712 seconds: 39,231 output tokens, including 29,704 reasoning tokens. No tools executed until this completed. Summaries improved relative to C: 2 calls / 43,880 tokens / 45.209 seconds, versus C's 3 calls / 95,730 tokens / 187.077 seconds. These savings did not overcome the slow initial generation and remaining repeated context reads.

## E latency intervention

Implementation: `0db34bb986d11ca0efae0c82860d20e941cd43af`, same model, budget and prompt (only test ID/output directory changed). Run `run_2df48762e7b43b561748ce71`; conversation `86427fac52a0cee08a4d992b`. Build and observer evidence are under `.run/long-task-latency`.

Agent calls now have 4,096 first-turn and 8,192 later-turn output limits. Generation and one length retry share a 180-second deadline. Rejected content/tool arguments do not enter execution/history; repeated truncation is an error, bypassing outer retries and failover. Reported usage from both attempts is counted. A UI reset clears rejected transient thinking before retry. Policy is composed in one service factory; generic LLM transport and file validation remain independent.

E first model call: 33.7377 seconds, 2,554 output tokens including 1,850 reasoning; first tool result at 33.996 seconds. This is approximately 93% less first-action waiting than D. The first response completed normally with tool calls, not truncation. This single paired observation does not establish reliable overall task speed, cost or delivery quality; terminal quality findings follow below.

Verification: whole repository Go suite passed; affected LLM/service suites and race tests rerun after the retry UI hook passed; vet and build passed. Tests reproduce the original missing output cap and false acceptance of truncation, exercise actual Eino tool execution, bound retries/deadlines, preserve request isolation, and account discarded usage. Focused independent code review found no P1/P2 blockers.

## Independent D audit

Verdict: **FAIL / incomplete delivery**. The original 23 files were hash checked before and after a read-only audit; execution/mutations used inspected isolated script copies.

- All 65,196 raw/clean/rejected field comparisons, fixture order, CSV structure, quality counts and required metrics in 42 groups passed. Four SVGs and the offline HTML passed 526 numeric/resource checks.
- Actual substantive official page reads: 17 (Eino 4, LangGraph 4, CrewAI 5, AutoGen 4); 18 listed source URLs include two pure indexes. Page coverage passes, but none of the 24 comparison cells has its required direct source citation.
- The research misreads saved evidence, particularly CrewAI persistence/resume support, and uses unsupported comparative superlatives. Reading enough pages did not guarantee accurate synthesis.
- Original verifier reports 30/30 PASS. Independent mutation tests detect 12 of 17 defects; the verifier's standalone helper tests do not exercise the analysis implementation's SLA/P95 definitions. Original metrics are correct; verifier adequacy is not.
- `manifest.json` and `deliverables.zip` are absent. Executing the packaging helper only in an audit copy exposes a further missing manifest entry in the archive. README has runnable individual steps but omits the requested single combined reproduction command.

Detailed local audit: `.run/long-task-delivery/independent/independent_report.md`, `independent_summary.json` and the source/script/visual audit JSONs. Core mathematics and CLI isolation were regression checked without weakening the prompt. The prompt does not explicitly require SLA columns in every non-priority grouped CSV, so their absence was not mislabeled as a failure.


## E terminal audit and the next repaired defect

E ended `partial` after **952.390 seconds / 819,324 tokens / 86 tool calls**. All 24 required products exist (25 files including notes), with four SVGs in a subdirectory. There were no length finishes or model-call errors. Compared with D, startup latency improved, but **the task still fails acceptance**.

The generator replaced the prompt's formulas and labels: clean data contains 3,636 rows with 36 duplicates, 396 missing CSAT values, first-response mean 715.26 and P95 1,368. The correct fixture requires 3,600 unique rows, 514 missing CSAT, mean 119.5 and P95 227. The verifier reproducibly reports 50/50 PASS while enforcing the wrong rules. Independent mutation testing catches 12 of 18 defects. Charts and tables are internally consistent with the wrong data; that does not establish correctness.

Research page coverage and citation positioning pass (16 substantive official pages; all 24 comparison cells trace to sources), but seven content findings remain. Manifest covers the expected files but contains stale README and verification hashes. The ZIP contains the manifest and matches current member bytes. README still lacks the requested single reproduction command. Detailed evidence: `.run/long-task-latency/independent/independent_report.md` and `independent_summary.json`.

Investigation reproduced a deterministic original-request retention defect in the summary boundary. It is a credible contributor to drift, not proof of the sole cause of E. The repair assigns human provenance at chat ingestion, keeps exact requests and later corrections outside the model-written work summary, and excludes synthetic memory notices. Original messages survive JSON journal/checkpoint serialization. Legacy histories lacking provenance remain intact; retained requests alone do not repeatedly trigger compression. No generated XML tag or rewritten formula controls what survives.

Offline validation exercises 120 actual adapter inputs with 5 summaries, plus the real Eino runner with 90 tool executions, 91 model generations and 3 summaries. Every generation retains the exact original task; later corrections and intentional duplicate user messages survive repeated compression. Full Go tests, affected race checks, vet and build pass. These are deterministic runtime tests with scripted model responses, **not a third live end-to-end benchmark**. A new live benchmark is needed before claiming the requirement-retention repair resolves final delivery quality.

Remaining priorities are explicit phase handoff and delivery reservation, claim-to-source semantic checking, and independent acceptance checks that cannot be rewritten to bless an incorrect implementation. Structural file checks and generation limits alone cannot certify those properties.


Final review reproduced and repaired two compatibility boundaries: old clarification history must not promote an unmarked synthetic digest after a new human reply, and debug byte estimates must not suppress Eino's provider-reported context threshold. Explicit positive provenance and native token-trigger delegation address both. Focused independent rereview found no remaining P1/P2 blockers; full tests, service race checks, vet and build passed on the final implementation.


Final requirement-retention implementation: `9c78fde863dd67eecef41cbab29adc25a0cbd770`. Backend rebuilt and restarted after E finished, retaining the existing process configuration. Source/binary metadata: `.run/long-task-requirements/build.json`.

Final browser smoke check on the deployed requirements patch: `run_446864cda8f3f1c151839d5e`, status `done`, 5.568 seconds, 1,185 reported tokens; response `检查通过`. The stored user event has `action=human_input`. Backend and frontend returned HTTP 200. This checks deployment and input provenance, not long-task acceptance.
