# Long-task retrieval and final delivery

## Evidence

The SLA run `run_3f572a53ffc4c6d2f9bf9f25` produced independently correct CSVs and a report, then invented a denominator and source-row/state details in its final chat. Its fifth required official documentation read was denied after computation had spent half the total token allowance, despite the separate delivery phase beginning at three quarters.

## Changes

1. Use one cumulative token boundary for research admission and tool visibility: the last quarter remains reserved for verification and delivery. Keep the existing call count, caching, concurrent reservation and whole-run limits.
2. Add an explicit `update_plan.final_response` choice: `answer` (default) or `file_receipt`. File receipt is for requests whose final answer needs only file links and brief acceptance. It must not replace an independently requested substantive chat answer. Put detailed conclusions, actual task-specific verification evidence and limitations in the deliverables.
3. For opted-in main-agent final messages, inspect current files and original plan state before publishing a deterministic receipt. The receipt reports file checks, never semantic correctness. Hold content deltas before that boundary so replaced text cannot leak into SSE. Preserve ordinary chat, delegates, tool progress, partial/budget-limited responses and cancellation handling. Persist the same response in conversation history and recovery journal. Preserve the choice across checkpoints.
4. Test the observed incorrect response through the real runner, default/partial/changed-file/cancellation boundaries, escaping, and recovery. Run the full suite, build/restart, then rerun the same real SLA task with untouched input and independent checks.

## Limits

This removes an optional second statistical narrative; it does not verify arbitrary model prose or make report facts true. Normal `answer` mode still requires semantic checking. The run's plan declaration is model-owned, so completion is also independently audited during evaluation.
