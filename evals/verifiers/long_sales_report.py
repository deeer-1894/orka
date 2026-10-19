"""Grades long_sales_report: the summary must agree with sales.csv and the report with the summary."""
import csv
import re
import sys

from grading import Grader, links

PNGS = ("trend.png", "seasonality.png")


def load_sales(g):
    """{product: {month: value}} from long (month,product,value) or wide (month + one column per product) CSV."""
    text = g.read("sales.csv")
    if text is None:
        return {}
    rows = list(csv.reader(text.splitlines()))
    if len(rows) < 2:
        g.fail("sales.csv has no data rows")
        return {}
    head = [h.strip().lower() for h in rows[0]]
    month = next((i for i, h in enumerate(head) if h in ("month", "date", "period", "月份", "日期")), 0)
    data = {}
    if len(head) == 3:
        product = next(i for i, h in enumerate(head) if i != month and not re.search(r"sales|qty|value|amount|销量", h))
        value = next(i for i in range(3) if i not in (month, product))
        for r in rows[1:]:
            if len(r) == 3:
                data.setdefault(r[product].strip(), {})[r[month].strip()] = float(r[value])
    else:
        for r in rows[1:]:
            for i, name in enumerate(rows[0]):
                if i != month and i < len(r) and r[i].strip():
                    data.setdefault(name.strip(), {})[r[month].strip()] = float(r[i])
    return data


def top_candidates(sales, months):
    """Top product under each reasonable YoY definition; any of them is an acceptable answer."""
    first, last = months[:12], months[12:]
    annual = {p: sum(s[m] for m in last) / sum(s[m] for m in first) - 1 for p, s in sales.items()}
    monthly = {p: sum(s[b] / s[a] - 1 for a, b in zip(first, last)) / 12 for p, s in sales.items()}
    latest = {p: s[months[-1]] / s[months[-13]] - 1 for p, s in sales.items()}
    return {max(metric, key=metric.get) for metric in (annual, monthly, latest)}


def named_top(node, products, path=""):
    """First product name stored under a key path mentioning 'top'."""
    if isinstance(node, dict):
        children = ((v, f"{path}/{k}".lower()) for k, v in node.items())
    elif isinstance(node, list):
        children = ((v, path) for v in node)
    else:
        return node if isinstance(node, str) and "top" in path and node in products else None
    return next((hit for v, p in children if (hit := named_top(v, products, p))), None)


def main(g):
    sales = load_sales(g)
    months = sorted({m for series in sales.values() for m in series})
    complete = len(sales) == 5 and len(months) == 24 and all(len(s) == 24 for s in sales.values())
    if sales and not complete:
        g.fail(f"sales.csv has {len(sales)} product lines x {len(months)} months, want a complete 5 x 24")

    summary = g.json("summary.json")
    top = named_top(summary, sales) if summary is not None else None
    if summary is not None and not top:
        g.fail("summary.json names no top-growth product line")
    elif top and complete and top not in (expected := top_candidates(sales, months)):
        g.fail(f"summary.json top product {top} is not the top under any YoY definition recomputed from sales.csv ({sorted(expected)})")

    report = g.read("report.md")
    if report is not None:
        if top and top not in report:
            g.fail(f"report.md never names the computed top product {top}")
        if len(links(report)) < 2:
            g.fail(f"report.md cites {len(links(report))} source links, want >= 2")
        for png in PNGS:
            if png not in report:
                g.fail(f"report.md does not reference {png}")
    for png in PNGS:
        g.expect_png(png)
    g.expect_tests("test_analyze.py", 3)


if __name__ == "__main__":
    grader = Grader(sys.argv[1])
    main(grader)
    grader.finish()
