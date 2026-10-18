# Long-task delivery and recovery

The user approved continuing the preceding analysis on `codex/long-task-optimization` and running another full evaluation. This increment keeps the existing runtime and adds explicit, testable execution state.

* Forward supported Eino model options to the existing LLM request. Bound summary output and reject truncated summaries without replacing the original history.
* Preserve omitted plan steps. Register required workspace-relative output paths additively through `update_plan`; check actual files through `check_delivery`, independently of plan status. Recheck at finalization so later mutations cannot preserve a stale success. These checks establish file integrity, not semantic correctness or completeness of natural-language requirement extraction.
* Keep recovery journals for partial runs. Persist plan, output requirements and cumulative budget consumption; restore them without resetting the token allowance. Preserve completed tool results and explicitly identify uncertain calls so recovery does not blindly repeat side effects.
* Add budget-aware execution guidance, including early generation of independent deliverables and a verification/delivery reserve. The existing retrieval limit remains enforced. A complete phase scheduler and parallel admission controller are outside this increment.
* Run focused regressions, full Go checks, rebuild the backend, then execute the unchanged long-task prompt in a new conversation/output directory. Independently audit delivered data, sources, visuals, verifier behavior and packaging. Report failures honestly.

Artifact inspection belongs in a focused module with a small read-only interface. Runtime policy and journal state remain in the control layer. No benchmark-specific formulas belong in the runtime. No new dependency or database migration is required. All modifications remain on the current branch.

## Follow-up: bound the work before each tool action

D's first completion took 506.17s with 39,231 output tokens (29,704 reasoning), before executing any tool. The run-wide allowance and summary cap do not constrain this wait. Add an explicit, immutable model-call policy at the service composition boundary: first action 4,096 output tokens, later actions 8,192, one 180-second deadline shared across a maximum of two length attempts. Explicit smaller request caps remain effective. Fresh input without assistant/tool history gets the first-action cap; no mutable per-model counter is shared between runs.

The adapter retains streaming transport and live reasoning telemetry, but only exposes complete accepted assistant content/tool calls to Eino. A length response is discarded and retried once with the original input plus a request for one small complete action. No truncated arguments or fabricated tool receipts enter history. Repeated truncation or the private deadline is a typed terminal call-limit error: transport/ADK retries and tier failover must not multiply it. Provider-reported usage from discarded attempts still goes through the existing meter; unreported canceled usage is not invented. Unbounded adapter instances remain compatible; summary limits remain separately configured.

This bounds a generation attempt and its length recovery, not total run duration or successful delivery. Phase guidance asks the first turn to plan and perform one small action; larger files can be built in separate steps. Compare D and E with the unchanged task fixture and independently audit final products.


## Follow-up: original requirements survive work compression

E produced incorrect data formulas after summary generation. A separate offline probe establishes a concrete retention defect (not exclusive causation for E): Eino DefaultFinalize substitutes original user text only when generated XML markers exist, and later summaries exclude previous synthetic summaries from original-user extraction. Runtime user-role notices can also displace actual requests.

Tag human chat messages at the service ingestion boundary; distinguish injected digest context by an internal action. Preserve each original user message, including repeated text and later corrections, unchanged and in order. Custom finalization keeps system messages, one assistant work summary, then those exact requests. It does not depend on generated XML tags or Eino-private metadata. Journal/checkpoint serialization preserves the service-owned provenance metadata. Histories without provenance stay intact instead of guessing from role or dropping their input. Existing delegate context handling uses reduction, not this summarizer.

Avoid repeated summaries caused by retained requirements alone: count compressible work, and retain the total-token trigger when immutable input is below its threshold. When immutable input alone exceeds the threshold, wait for substantial new work before summarizing again; never silently trim requirements. Provider context and total run allowances still apply. This prevents deterministic loss of requirements; it cannot guarantee that a model follows them or that a final artifact is semantically correct.


Final review tightened provenance to positive `human_input` markers at authenticated new-message/history/clarification entry, with separate runtime markers. Any ambiguous legacy user entry keeps the entire history intact, even after a new trusted reply. Runtime journal/budget/archive notices retain their classification across serialization. The wrapper owns the compressible-message count; normal token-trigger decisions delegate directly to Eino's provider-usage and tool-schema accounting. Regression probes for mixed old clarification history and provider usage above 120k pass.
