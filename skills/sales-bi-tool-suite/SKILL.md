---
name: sales-bi-tool-suite
description: Route Chinese phone sales, market share, ranking, trend, product metadata, phone specs, and sales report requests to the local Sales BI MCP tools and return their governed results.
---

# Sales BI MCP Contract

Treat the MCP result as the source of truth. Follow this sequence exactly.

## 1. Route

| Request | Tool |
|---|---|
| Any sales metric: 销量、份额、贡献度、排行、趋势、同比、环比、首销、平销、上市影响、异常、原因、表现、品牌/产品/系列/规格维度的销售表现 | `sales_query_answer` |
| Product identity, release date, series members, predecessor, reference product, competitors | `product_metadata_resolve`, `product_metadata_profile`, `product_metadata_series`, `product_metadata_batch_profile` |
| Product specs, chip, screen, camera, battery, charging, price, or comparison without a sales metric | `product_specs_query`, `product_specs_compare` |
| Data coverage, ingestion status, or available time range | `sales_data_status` |
| Explicit request to generate an HTML daily, weekly, or monthly sales report | `sales_report_generate` |

A spec term remains a sales request whenever the requested measure is sales, share, ranking, growth, or model count. Examples: 各价位段销量, 骁龙机型销量, 折叠屏销量同比 all route to `sales_query_answer`.

## 2. Execute Sales Once

For a concrete sales request:

Local sales data currently extends through 2026-07-31. Let `sales_query_answer` judge coverage; do not reject a date from model memory.

1. Call `sales_query_answer` exactly once.
2. Set `question` to the user's complete message, character for character.
3. Omit `plan`; keep default `compact` and `row_limit` unless the user explicitly requests otherwise.
4. Make no capability, status, metadata, specs, file, shell, Python, or second sales call before or after it.

Use `sales_query_capabilities` only for an explicit capability question, or when you are about to reject a dimension because you think it is unavailable. It is not a preflight check for an ordinary sales question.

## 3. Return a Sealed Result

When a tool result has `status="ok"` and a non-empty `answer`, the result is **sealed**:

1. Extract the string value of `answer`.
2. Set the entire final response to that string, byte for byte.
3. Preserve every newline, Markdown table row, heading, note, and `![title](url)` image.
4. Stop immediately after the last character of `answer`.

Completion check: compare the complete final response with `answer` for exact equality. If they differ, replace the complete response with `answer`. Do not summarize, shorten, restyle, introduce, conclude, or explain the sealed string.

Example: if `answer` contains a sentence followed by a Markdown table, returning only “销量为 X 万台” is invalid. Return the sentence and the complete table exactly.

If `answer` is empty, render `tables` without changing headers, row order, values, or precision.

## 4. Handle Other Statuses Once

Treat every non-`ok` sales result as terminal for that user message. Return its `answer` exactly when present; otherwise use its user-facing diagnostic message. Then stop.

- `clarify_required`: ask the returned clarification and wait for the user. Keep the original call single; do not choose a metric, product, threshold, or competitor.
- `unsupported_data_field`, `unsupported_data_coverage`, `unsupported_metric`, `field_conflict_detected`: return the stated boundary.
- `product_not_found`, `product_ambiguous`: return the stated mapping result and candidates, then wait.
- `error`: return the provided error detail as an internal tool failure; do not diagnose or repair `tool/**` in the question-answering session.
- `model_assist_required`: show exactly one candidate's `assist.confirmation_prompt` and wait. After the user's confirmation, call `sales_query_answer_assisted` once with the matching `assist_id`, `confirmation_token`, the user's exact `user_reply`, and a fresh `user_turn_token`. Use `selected_candidate_id` in `patch` when available. Treat its result as sealed.

## 5. Analysis Results

For a sales request asking for analysis, reasons, opportunities, problems, or recommendations, still call `sales_query_answer` only once.

- Without an `analysis` block, return the sealed `answer` and stop.
- With an `analysis` block, keep the sealed `answer` as the first part. Build `narrative` only from its `fact_ledger`, citing `fact_ids` for every number and named subject. Mark unproven causes as `hypothesis=true`. Call `sales_query_publish(query_id, narrative)` once with no intervening tool call, then append its returned `analysis_text` unchanged.
- Accept `narrative_fallback=true` as successful. Do not retry publication.

## 6. Reports

An explicit report request uses exactly two calls:

1. `sales_report_generate(action="prepare", report_type="daily"|"weekly"|"monthly", period=<user period or empty>)`
2. Build `narrative` only from the returned `fact_ledger` and `insight_candidates`, citing `fact_ids`, then call `sales_report_generate(action="publish", report_id=<prepared id>, narrative=<draft>)` once.

Make no other tool call between prepare and publish. Return the published `answer` URL unchanged. Accept `narrative_fallback=true` and stop without retrying.

## 7. Product Facts and Specs

Use only the routed metadata or specs tools. Pass the user's product wording unchanged. Return ambiguous, not-found, and low-confidence results instead of guessing from public knowledge or sales rows. Treat an `ok` result with an `answer` as sealed using the same equality check.

## Guardrail

Keep `tool/**`, DuckDB, Parquet, indexes, runtime files, and installed skill files read-only during a BI question. Use the MCP tools as the only execution path. Data maintenance is a separate task that requires an explicit user request.
