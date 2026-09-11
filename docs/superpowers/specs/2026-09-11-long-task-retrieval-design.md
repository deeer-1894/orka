# Long-task retrieval design

The baseline on `codex/long-task-optimization` made 109 tool calls, exhausted its 800k token budget and delivered only a research note. Repeated retrieval, loss of usable evidence after summarization, guessed documentation URLs and incomplete usage reporting are the scope of this change.

## Decisions

Keep the existing search providers. Changing providers alone does not make a run converge. Adding a crawler service or vector database would introduce deployment and storage complexity before the observed failure is fixed. Instead, add bounded document navigation and a run-scoped research session around existing tools.

- The tools server owns HTTP retrieval, documentation-index parsing and relevant-section extraction. `discover_docs` returns actual links from an index; `read_section` reads the parts relevant to a question. They do not know about conversations, budgets or MongoDB.
- The control layer owns a research session per execution. It normalizes retrieval arguments, coalesces duplicate concurrent requests, stores successful observations with source metadata under the user's workspace and exposes `search_evidence`. Failed requests are not cached as successful evidence. No cache is shared between users or executions.
- The session bounds external retrieval and reserves half of the run's token budget for synthesis, execution and verification. Local evidence reads and non-retrieval tools remain available. Limits are explicit and return an honest explanation; they never mark work complete. A compact live research/plan reminder is added after history compression so the state does not depend on the model remembering it.
- The final run record reads usage from the same metered budget used for enforcement, including auxiliary model calls, with a legacy fallback where no metered usage exists.

## Invariants

Persist before advertising an evidence path. Use the existing workspace path guard, deterministic IDs and bounded stored/output text. Cancellation must propagate; concurrent duplicate callers must be able to cancel while waiting. Keep errors and unavailable searches out of the successful cache. Budget checks must happen before external calls, not after a large parallel batch. Do not cache side effects or general HTTP requests. Existing human-approval gates remain in force.

Tools and model middleware use the same session object, but HTTP tools do not depend on it. The implementation belongs in focused files for evidence storage/search, retrieval orchestration and model guidance; the Eino adapter retains adaptation/retry/error responsibilities.

## Verification

Use local HTTP fixtures for document discovery and section reading. Exercise the public tool interface for cache isolation, concurrent deduplication, retryable failure, durable evidence lookup and budget reservation. Use a scripted Eino model to prove tool visibility and preserved guidance after context replacement. Run all Go modules' tests, race checks on changed packages and vet; build the frontend only if changed. Rebuild/restart affected services and repeat the baseline task with a fresh output directory; independently check generated files and deterministic metrics. Record incomplete results honestly.

All changes and commits stay on `codex/long-task-optimization`; do not merge or push another branch.

## Review-driven refinements

Live B revealed that remote-call convergence alone can shift the loop into repeated local evidence reads. Keep the catalog compact and shorten only verified identical reads of original saved evidence. Per-agent read history preserves each consumer's first full read; modified evidence remains fully readable rather than redirecting to an obsolete source snapshot. File argument aliases are parsed in a small shared core module so execution and the control policy cannot drift.

DeepAgent reuses middleware instances between the orchestrator and general worker. Registered tool catalogs must therefore belong to the agent invocation, while unlock choices can be shared across compatible catalogs. Specialist agents receive the same live research reminder while retaining their own final budget guard. Attachment prepasses and the fast path must carry the run's usage context just like ordinary agent calls.
