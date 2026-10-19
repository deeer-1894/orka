"""Grades long_ratelimit_delivery: facts against PyPI, the report against bench.json and the xlsx."""
import ast
import json
import re
import sys
import urllib.request

from grading import Grader

CALLS = 100_000
FRAMEWORKS = ("FastAPI", "Flask", "Django")
ALL_ALLOWED = re.compile(r"所有请求均(通过|被允许)|全部请求均?(通过|被允许)|all (requests|calls) (were )?(allowed|accepted|passed)", re.I)


def pypi_version(project):
    """Live PyPI version, or None when the grader has no network."""
    try:
        with urllib.request.urlopen(f"https://pypi.org/pypi/{project}/json", timeout=20) as r:
            return json.load(r)["info"]["version"]
    except Exception as e:
        print(f"WARN: PyPI unavailable for {project}: {e}")
        return None


def check_comparison(g):
    """Returns {framework: version} as stated in comparison.xlsx."""
    rows = g.xlsx_rows("comparison.xlsx")
    versions = {}
    for name in FRAMEWORKS:
        row = next((r for r in rows if any(c.lower() == name.lower() for c in r)), None)
        version = next((c for c in row or [] if re.fullmatch(r"\d+\.\d+(\.\d+)?", c)), None)
        if not version:
            g.fail(f"comparison.xlsx has no {name} row with a version")
            continue
        versions[name] = version
        if not any(c.startswith("http") for c in row):
            g.fail(f"comparison.xlsx {name} row has no source link")
        live = pypi_version(name.lower())
        if live and live != version:
            g.fail(f"{name} version {version} in comparison.xlsx, PyPI says {live}")
    return versions


def check_bench(g):
    """Validates bench.json; returns whether any benchmark call was denied."""
    bench = g.json("bench.json") or {}
    denied_any = False
    for algo in ("token_bucket", "sliding_window"):
        for mode in ("single", "threads8"):
            cell = (bench.get(algo) or {}).get(mode)
            fields = [cell.get(k) for k in ("ops_per_sec", "allowed", "denied")] if isinstance(cell, dict) else []
            if len(fields) != 3 or not all(isinstance(v, (int, float)) for v in fields):
                g.fail(f"bench.json {algo}.{mode} needs numeric ops_per_sec, allowed, denied")
                continue
            ops, allowed, denied = fields
            if allowed + denied != CALLS:
                g.fail(f"bench.json {algo}.{mode}: allowed+denied={allowed + denied}, want {CALLS}")
            if ops <= 0:
                g.fail(f"bench.json {algo}.{mode}: ops_per_sec={ops}")
            denied_any = denied_any or denied > 0
    g.expect_png("bench.png")
    return denied_any


def check_report(g, versions, denied_any):
    report = g.read("report.md")
    if report is None:
        return
    for name, version in versions.items():
        if version not in report:
            g.fail(f"report.md does not state {name} {version} as in comparison.xlsx")
    if "bench.png" not in report:
        g.fail("report.md does not reference bench.png")
    if denied_any and not re.search(r"拒绝|denied|reject", report, re.I):
        g.fail("bench.json records denied calls but report.md never discusses rejections")
    if denied_any and ALL_ALLOWED.search(report):
        g.fail("report.md claims all benchmark calls were allowed, but bench.json records denied calls")
    if len(g.docx_text("report.docx")) < 500:
        g.fail("report.docx has under 500 characters of text")


def check_package(g):
    classes = set()
    for module in ("ratelimit/token_bucket.py", "ratelimit/sliding_window.py"):
        src, tree = g.python_ast(module)
        if tree is None:
            continue
        classes |= {n.name for n in ast.walk(tree) if isinstance(n, ast.ClassDef)}
        if "Lock" not in src:
            g.fail(f"{module} uses no lock; the limiter must be thread-safe")
    for want in ("TokenBucket", "SlidingWindow"):
        if want not in classes:
            g.fail(f"class {want} not defined in ratelimit/")
    tests = g.expect_tests("tests/test_ratelimit.py", 15)
    if tests is not None and not re.search(r"Thread|concurrent", tests):
        g.fail("tests/test_ratelimit.py has no concurrency test")


def main(g):
    versions = check_comparison(g)
    denied_any = check_bench(g)
    check_report(g, versions, denied_any)
    check_package(g)


if __name__ == "__main__":
    grader = Grader(sys.argv[1])
    main(grader)
    grader.finish()
