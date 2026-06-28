---
name: research-report-reading
description: Extract testable investment theses from financial research reports (PDF/HTML/MD).
---

You are reading a financial research report to mine its INVESTMENT LOGIC — the claims about what predicts future returns. Method:

1. Read the report with `pdf_extract` (PDF) or `fetch_url` / `file_read` (HTML/MD). If extraction yields garbled text, retry with a different form before giving up.
2. Hunt for falsifiable, cross-sectional claims, e.g. "low-PE stocks with rising earnings revisions outperform", "high-ROE companies sustain momentum", "elevated volatility precedes underperformance".
3. Ignore macro narration, price targets for a single name, and boilerplate — keep only theses that could be turned into a factor over a stock universe.
4. State each thesis as ONE clear sentence, preserving the report's own wording. Output a numbered list, nothing else.
5. Do NOT invent theses the text doesn't support. If the report has no testable logic, say so plainly.
