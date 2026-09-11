# Long-task delivery and recovery

The user approved continuing the preceding analysis on `codex/long-task-optimization` and running another full evaluation. This increment keeps the existing runtime and adds explicit, testable execution state.

* Forward supported Eino model options to the existing LLM request. Bound summary output and reject truncated summaries without replacing the original history.
* Preserve omitted plan steps. Register required workspace-relative output paths additively through `update_plan`; check actual files through `check_delivery`, independently of plan status. Recheck at finalization so later mutations cannot preserve a stale success. These checks establish file integrity, not semantic correctness or completeness of natural-language requirement extraction.
* Keep recovery journals for partial runs. Persist plan, output requirements and cumulative budget consumption; restore them without resetting the token allowance. Preserve completed tool results and explicitly identify uncertain calls so recovery does not blindly repeat side effects.
* Add budget-aware execution guidance, including early generation of independent deliverables and a verification/delivery reserve. The existing retrieval limit remains enforced. A complete phase scheduler and parallel admission controller are outside this increment.
* Run focused regressions, full Go checks, rebuild the backend, then execute the unchanged long-task prompt in a new conversation/output directory. Independently audit delivered data, sources, visuals, verifier behavior and packaging. Report failures honestly.

Artifact inspection belongs in a focused module with a small read-only interface. Runtime policy and journal state remain in the control layer. No benchmark-specific formulas belong in the runtime. No new dependency or database migration is required. All modifications remain on the current branch.
