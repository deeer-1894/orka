#!/usr/bin/env python3
"""Phase-1 reference backtest harness (pure stdlib, no pandas/numpy).

It builds a deterministic synthetic equity panel in which a handful of base
signals (mom_20, value=-pe, roe, vol_20) carry known predictive power, then
backtests whichever of those signals the factor `--expression` references and
prints a metrics JSON on the LAST line of stdout:

    {"metrics": {"ic":..., "ir":..., "sharpe":..., "turnover":..., "max_dd":..., "periods":...}, "source":"python"}

Swap this for a real data-fed engine in Phase 2 — the contract (expression in,
metrics JSON out) is what the `backtest` tool depends on.
"""
import argparse, json, math, random, hashlib

TICKERS = [f"S{i:03d}" for i in range(40)]
DAYS = 180
# ground-truth predictive weight of each base signal on forward return
SIGNAL_WEIGHTS = {"mom_20": 0.9, "roe": 0.6, "value": 0.5, "vol_20": -0.4}


def _seed(expr: str) -> int:
    return int(hashlib.sha1(expr.encode()).hexdigest()[:8], 16)


def build_panel():
    """Returns days -> list of dict(ticker, signals..., fwd_ret)."""
    rng = random.Random(42)
    # persistent per-ticker signal levels with daily drift
    state = {t: {s: rng.gauss(0, 1) for s in SIGNAL_WEIGHTS} for t in TICKERS}
    panel = []
    for _ in range(DAYS):
        day = []
        for t in TICKERS:
            for s in SIGNAL_WEIGHTS:
                state[t][s] = 0.95 * state[t][s] + 0.05 * rng.gauss(0, 1)
            sig = state[t]
            fwd = sum(SIGNAL_WEIGHTS[s] * sig[s] for s in SIGNAL_WEIGHTS) * 0.01
            fwd += rng.gauss(0, 0.02)  # idiosyncratic noise
            row = {"ticker": t, "fwd_ret": fwd}
            row.update(sig)
            day.append(row)
        panel.append(day)
    return panel


def referenced_signals(expr: str):
    e = expr.lower()
    hits = [s for s in SIGNAL_WEIGHTS if s in e]
    if "pe" in e and "value" not in hits:
        hits.append("value")  # value ~ -pe
    return hits or None


def factor_value(row, signals):
    return sum(row[s] for s in signals) / len(signals)


def pearson(xs, ys):
    n = len(xs)
    if n < 2:
        return 0.0
    mx, my = sum(xs) / n, sum(ys) / n
    num = sum((x - mx) * (y - my) for x, y in zip(xs, ys))
    dx = math.sqrt(sum((x - mx) ** 2 for x in xs))
    dy = math.sqrt(sum((y - my) ** 2 for y in ys))
    return num / (dx * dy) if dx and dy else 0.0


def backtest(expr):
    panel = build_panel()
    signals = referenced_signals(expr)
    if not signals:
        # unknown expression: derive a weak, stable pseudo-signal from its hash
        rng = random.Random(_seed(expr))
        w = {s: rng.uniform(-0.3, 0.3) for s in SIGNAL_WEIGHTS}
        signals_fn = lambda row: sum(w[s] * row[s] for s in w)
    else:
        signals_fn = lambda row: factor_value(row, signals)

    ic_series, ls_returns, prev_top = [], [], set()
    turnovers = []
    for day in panel:
        fac = [(signals_fn(r), r["fwd_ret"], r["ticker"]) for r in day]
        xs = [f for f, _, _ in fac]
        ys = [y for _, y, _ in fac]
        ic_series.append(pearson(xs, ys))
        fac.sort(key=lambda z: z[0], reverse=True)
        k = max(1, len(fac) // 5)
        top = fac[:k]
        bot = fac[-k:]
        ls = sum(y for _, y, _ in top) / k - sum(y for _, y, _ in bot) / k
        ls_returns.append(ls)
        top_set = {t for _, _, t in top}
        if prev_top:
            turnovers.append(len(top_set ^ prev_top) / (2 * k))
        prev_top = top_set

    n = len(ic_series)
    ic = sum(ic_series) / n
    ic_std = math.sqrt(sum((x - ic) ** 2 for x in ic_series) / n) if n else 0
    ir = ic / ic_std if ic_std else 0.0
    mu = sum(ls_returns) / n
    sd = math.sqrt(sum((x - mu) ** 2 for x in ls_returns) / n) if n else 0
    sharpe = (mu / sd * math.sqrt(252)) if sd else 0.0

    # max drawdown of the cumulative long-short curve
    cum, peak, mdd = 0.0, 0.0, 0.0
    for r in ls_returns:
        cum += r
        peak = max(peak, cum)
        mdd = min(mdd, cum - peak)
    turnover = sum(turnovers) / len(turnovers) if turnovers else 0.0

    return {
        "ic": round(ic, 4), "ir": round(ir, 3), "sharpe": round(sharpe, 3),
        "turnover": round(turnover, 3), "max_dd": round(mdd, 3), "periods": n,
    }


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--expression", required=True)
    ap.add_argument("--horizon", default="1d")
    args = ap.parse_args()
    print(json.dumps({"metrics": backtest(args.expression), "source": "python"}))


if __name__ == "__main__":
    main()
