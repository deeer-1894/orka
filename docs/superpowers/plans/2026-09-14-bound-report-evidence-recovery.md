# Bound report values and evidence recovery

The previous real refund report said 17–19 orders per group although the verified CSV contained only 18–19. During SLA recovery, search_evidence returned no results despite saved source files. Both failures are reproducible and independent.

Implement two bounded capabilities on the existing optimization branch:

1. Restore saved evidence metadata and bodies from checkpoint-owned references in the same session filesystem. Preserve original source URLs, retrieval times and usage; do not scan other runs or infer provenance from edited files. Missing, corrupt or changed captures produce explicit degradation rather than invented evidence.
2. Render Markdown report placeholders from CSV aggregates in a small pure Go package. A declared `.report.json` file contains a template and named bindings (CSV, column, min/max/sum/count, exact string filters). Use exact decimals, confined filesystem reads and bounded inputs; fail on missing columns, invalid numbers, unresolved placeholders or empty min/max groups. The tools server exposes `render_report`; existing artifact delivery checks rerender the declared spec and compare the report bytes, catching stale output after source edits. Ordinary prose and literal numbers remain outside this limited guarantee.

Validation: reproduce the 17-versus-18 failure, source-change invalidation, large integer/decimal arithmetic, cancellation, session isolation, atomic output publication, serialized evidence recovery and corrupt/missing evidence. Run full tests, vet and affected race checks, restart services and tools, then repeat the previous refund task with bound report metrics and run a different task that exercises public research/recovery. Preserve model-generated outputs and independently verify them.

Report spec example (all paths relative to the session root):

```json
{"kind":"orka.report/v1","output":"outputs/report.md","template":"每组订单 {{low}}～{{high}} 笔，共 {{count}} 组。","bindings":{"low":{"csv":"outputs/summary.csv","column":"orders","op":"min"},"high":{"csv":"outputs/summary.csv","column":"orders","op":"max"},"count":{"csv":"outputs/summary.csv","op":"count"}}}
```

Declare both `outputs/summary.report.json` and `outputs/report.md` as required outputs. Use `render_report` with the spec path. After changing CSVs or the template, rerender; create any file-hash manifest last. Numbers not represented by supported bindings still need independent assertions. The renderer has no expression evaluator or network access.
