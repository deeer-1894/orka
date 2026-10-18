# Long-task evaluation: GreenFork and StockPilot (2026-09-14)

扩店任务产出了完整文件，但独立验收发现工作簿联动和审计漏洞。编程任务首次及协助恢复均触及预算，尚未打包交付；最终源码独立HTTP检查34/35，默认自测43/43，但反序自测42/43。本次已修复并部署终端超时挂起和子进程回收，完整证据如下。

## Scope and evidence

Real runs submitted through the visible Orka browser while logged in as the test account. Model: doubao-seed-2.1-turbo (Auto). Branch: codex/long-task-optimization. Evidence and generated artifacts remain in ignored `.run/long-task-greenfork/` and `.run/long-task-stockpilot/`; no credentials are included in this report.

GreenFork conversation: 6955a8089a0cf1e1ab359a4b; run: run_291c51a4af2f8616206912ab. StockPilot was subsequently submitted in the same conversation: the UI New chat click did not change the active conversation. The two runs have separate local evidence directories. GreenFork outputs were downloaded before StockPilot could overwrite shared output names. This does not test isolation between two conversations.

## GreenFork result

Runtime reports done: 767.275 seconds, 35 tools, 647,922 reported tokens. These tokens are the run metric, not a claim that the model generated that many words. Ten required input/output files exist. Two web searches were executed. Constraints changed in stages inside the original prompt; this was not a live user interruption.

An independent Fraction-based oracle, never shown to the model, recomputed all 24 combinations. Both CSV files contain exactly 12 distinct complete combinations; profit, contribution, volume, investment, fixed costs, revenue and feasibility match within 0.011 yuan. Initial optimum A+C/S1: monthly profit 110,905.53 yuan; revised optimum B+C/S2: 68,671.08 yuan. All six spreadsheet stress-case profits match. The delivered standard-library audit script runs from an independent local copy and reports 6/6.

### Defects reproduced

1. **Input changes do not propagate into revised Excel scenarios.** Revised store/supplier sheets contain copies of original values instead of references. On a copied workbook, changing original B daily orders from 190 to 100 and S2 unit cost from 10 to 20 changes baseline B+C/S2 profit from 76,759.79 to -45,060.31, while revised B+C/S2 remains 68,671.08. Revised B daily orders remain 171 and revised S2 cost remains 10. This violates the requested editable model behavior.
2. **Summary formulas fail in a real spreadsheet engine.** The unmodified workbook has 395 formula cells without cached values. LibreOffice recalculation produces #VALUE! in E15/E16 on both baseline/revised sheets. Detail rows and all six stress cases calculate correctly. The summary uses whole-column MAX(IF(...)) as an ordinary formula; this is an observed LibreOffice compatibility failure, not a claim of testing every Excel version.
3. **Generated audit misses corrupt input.** On local CSV copies, replacing the last row with a duplicate of the first (12 rows but one missing combination), setting one profit to NaN, or clearing a valid payback all produce zero mismatches. Row count is insufficient to establish combination coverage, float comparisons do not reject nonfinite values, and empty payback values bypass the comparison. Original CSV values themselves are correct.
4. **Final answer omits an explicit requirement.** User requested file links plus exactly three risk sentences. The agent selects final_response=file_receipt; runtime replaces the response with a generic structural receipt. No three-sentence risk summary is delivered. Relevant implementation: orka_control_layer/service/delivery_response.go; mode choice is model-controlled in plan_tool.go.
5. **Plan renaming causes extra work.** Six original English steps become eleven after semantically equivalent new titles are appended. Current plan behavior intentionally keeps renamed/omitted obligations; it cannot identify stable step identity. This protects against dropping work but causes duplicates and repeated completion calls.
6. **Repeated loss of workspace context.** Two separate shell calls begin with cd /workspace and fail to locate generated files. Both recover after pwd/relative-path execution. Report generation also raises KeyError: title because sources.json uses Chinese field names. These are recovered errors but consume extra generations.
7. **Research quality remains uneven.** Source dates are now within the explicit window, and official CPI values were independently confirmed. However, broad industry claims rely on promotional brand-comparison articles without independently verified underlying study methods. A specific brand's 20–30 yuan price is generalized to an industry band; poultry cost direction is stated without support in the cited excerpt. Research needs claim-to-evidence review, not only a count of URLs. The national official series supports macro prices, not a particular store's acquisition costs.

### Verification limits

Local independent workbook recalculation does not repair the file delivered by Orka. The generated validation.json covers six CSV/business checks and does not cover spreadsheet recalculation, edited-input propagation, source strength or final-response requirements. check_delivery explicitly verifies structure and should not be reported as semantic verification. The report's numerical tables agree with the oracle; qualitative market claims are not all independently validated.

Useful independently checked references: [official CPI release](https://www.stats.gov.cn/xxgk/sjfb/zxfb2020/202607/t20260709_1964084.html), [brand-focused article](https://tidenews.com.cn/tmh_news.html?id=69db7c9238547700016fac51), [supplier publication](https://gaishi.cn/zxzx/2026-07-07/239.html). The latter two are context, not independent statistical validation.

## StockPilot

Initial run run_15b325a830804416ce2aaa51 stopped partial at the token budget: 1,146.287 seconds, 58 tools, 815,609 reported tokens. Standard-library Python/SQLite HTTP application with native HTML/CSS/JS. Required behavior: atomic CSV import, exact money, idempotent stock movements, persistence, versioned restocking rule, browser interactions, 15+ tests and clean-directory ZIP reproduction. Independent expected sample values: stock valuation 344.00 yuan; COF restock 18 units costing 340.20, OAT 20 costing 196.00; total purchase cost 536.20. The evaluator tests invalid cases independently and does not substitute fixes into the submitted package before grading it.

The initial source snapshot passed 25/35 independent HTTP checks. Failures: both /static asset requests returned404; sNaN/NaN123 prices, non-string csv, extra CSV columns, and array request_id disconnected requests; duplicate CSV headers were accepted; multiline/blank-line errors reported logical row3 instead of physical line5. The task's own43 tests ended with test_export_csv failing; independent import/export passed, so this is not evidence by itself of a broken export endpoint. ZIP and README were absent when budget ended. Recovery is a separate explicit follow-up with defect evidence and narrowed scope to StockPilot; it must not be described as an unassisted first-attempt success.

### Execution-layer defect repaired during the test

The generated smoke test launched buffered Python and blocked on stdout.readline. Its shell timeout killed the shell but left Python descendants holding capture pipes, keeping the tool pending for over six minutes. Only the identified StockPilot processes were terminated; the agent then resumed and eventually reached its token boundary.

Committed fix f736c57 isolates each Unix command process group, cancels the group, owns the output pipe independently of Cmd.Wait, preserves the remaining deadline for valid child output, and bounds draining after cancellation. WaitDelay retains direct-process cancellation when it changes groups. Non-Unix platforms retain direct-process cancellation; this is not a guarantee to terminate detached process trees on every OS. Successfully detached services with redirected streams remain supported.

Regression coverage includes timeout, explicit cancellation, successful/nonzero parent exit with inherited pipes, direct process changing group, ordinary output and delayed valid child output. All tools_server tests, go vet and targeted race checks passed; independent code review findings were reproduced and resolved. The final image was deployed preserving workspace and existing configuration. A real signed MCP shell call through the deployed gateway with timeout1 returned in1.004 seconds; its child was terminated (zombie state pending reaping), not left running. Binary hash in the container matched the locally built fix.

### Assisted recovery

Explicit recovery run run_07acb5faa7890dc28d2eb538 also stopped partial at the token budget:625.798 seconds,47 tools,815,789 reported tokens. Separate evidence directory: `.run/long-task-stockpilot-recovery/`. It performed14 file_read calls; server/app.py, core/csv_io.py and core/store.py were each read three times, in addition to shell reads. Source changes occurred between some reads, so this count alone does not prove every reread was redundant. Plan titles again expanded instead of updating stable identities.

Final source was downloaded through the same conversation's file API; no evaluator fixes were inserted. The model's43 tests pass independently in default order. Reversing test-method order causes test_import_and_list to fail: expected64,900 cents, actual62,355, demonstrating shared mutable HTTP fixture dependence. Independent HTTP checks improve from25/35 to34/35; remaining failure is physical CSV error line5 reported as2.

The actual line-number root cause is an interface mismatch: csv_io.py writes row_dict["_line_"] while store.py reads row.get("_line",2). The recovery's final diagnosis blamed blank-line tracking instead; the downloaded parser already updates prev_end before skipping blanks. This is an example of a plausible diagnosis not verified against the complete data flow. Validation errors also use a generic field="row" rather than reliably naming the affected field.

The final project still lacks the requested README, stockpilot-validation.json and ZIP. Therefore clean-directory ZIP reproduction was impossible and is not claimed. v1 has a historical comparison document, but evidence does not establish the required implement/test-v1-before-v2 sequence. The original run remains partial, not upgraded to successful because a subset of tests passes.

An independent real browser was opened against a local copy on a random port with an isolated SQLite database. Sample data plus a1-yuan script-text probe showed345.00 total. Searching COF produced one inventory row; adding two COF through the UI changed stock10→12, inventory value345.00→382.80 and reorder total536.20→196.00. Unknown SKU produced a readable error; import without selecting a file produced a prompt. The script-tag sample remained literal text with no script DOM elements in cells. After a mutation, the list resets to all rows while the search input still saysCOF; this is a remaining UI consistency issue. Browser file-upload, downloaded-file interaction, mobile layout and full browser security coverage were not verified. The local browser-test server was stopped after inspection. The visible Orka account was confirmed unchanged and no Stop button remained.

After both runs ended, docker-compose.tools.yml enabled init:true. The deployed gateway was tested again through real MCP:1.004-second response for a1-second deadline, child /proc entry gone. This supplements group termination with orphan reaping and avoids accumulation under the container PID limit.

## Optimization priorities

- Make task acceptance requirements explicit and testable across artifacts and final chat. File-only receipt mode must preserve required non-file content.
- Use stable plan step identifiers; title changes should update presentation without duplicating or dropping obligations.
- Carry the authoritative session working directory across every generation and recovery prompt.
- Add reusable artifact-specific checks: finite numeric fields, complete unique key sets, required null/value semantics, spreadsheet recalculation and input-change tests. Keep these separate from generic file-existence checks.
- Require source quality and claim coverage evidence when using search; avoid translating a media excerpt into stronger claims than it supports.
