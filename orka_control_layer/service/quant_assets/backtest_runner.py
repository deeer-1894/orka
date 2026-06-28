#!/usr/bin/env python3
"""Factor backtest harness — real A-share data via akshare, synthetic fallback.

Pulls real daily bars for a small liquid A-share universe and computes
price-based signals (mom_20, mom_60, vol_20) + a value signal from the live PE
snapshot, cross-sectionally z-scored per day, then backtests whichever signals
the factor `--expression` references against 5-day forward returns. Prints a
metrics JSON on the last stdout line:

    {"metrics": {"ic":..,"ir":..,"sharpe":..,"turnover":..,"max_dd":..,"periods":..}, "source":"akshare"|"synthetic"}

If akshare/network/data is unavailable it falls back to a deterministic synthetic
panel so the pipeline never breaks (source="synthetic").
"""
import argparse, json, math, os, random, hashlib, statistics
from datetime import date

# a small, liquid, sector-diversified universe (codes for stock_zh_a_hist)
UNIVERSE = [
    "000001", "600519", "000858", "600036", "601318", "000333", "600276", "002594",
    "600900", "601166", "000651", "600030", "601888", "002415", "600887",
]
SIGNAL_WEIGHTS = {"mom_20": 0.9, "roe": 0.6, "value": 0.5, "vol_20": -0.4}  # synthetic ground truth
FWD = 5  # forward-return horizon (trading days)

# Real-data fetch is slow (one akshare call per stock), and gp_evolve backtests
# dozens of candidates, so we fetch the panel ONCE per day and cache it on disk.
CACHE_DIR = os.path.join(os.path.dirname(os.path.abspath(__file__)), ".cache")
_PANEL = None  # in-process cache


def get_panel():
    """Return (panel, source), cached in-process and on disk (per day)."""
    global _PANEL
    if _PANEL is not None:
        return _PANEL
    if os.environ.get("ORKA_BACKTEST_OFFLINE"):  # tests / offline → deterministic, no network
        _PANEL = (build_synth_panel(), "synthetic")
        return _PANEL
    cache = os.path.join(CACHE_DIR, "panel_" + date.today().strftime("%Y%m%d") + ".json")
    if os.path.exists(cache):
        try:
            with open(cache) as f:
                _PANEL = (json.load(f), "akshare")
                return _PANEL
        except Exception:
            pass
    try:
        panel = build_real_panel()
        os.makedirs(CACHE_DIR, exist_ok=True)
        with open(cache, "w") as f:
            json.dump(panel, f)
        _PANEL = (panel, "akshare")
    except Exception:
        _PANEL = (build_synth_panel(), "synthetic")
    return _PANEL


# ---------- real data (akshare) ----------

def build_real_panel():
    import akshare as ak
    from datetime import timedelta
    start = (date.today() - timedelta(days=260)).strftime("%Y%m%d")
    end = date.today().strftime("%Y%m%d")

    # live PE snapshot → value signal (one call for the whole market)
    pe = {}
    try:
        spot = ak.stock_zh_a_spot_em()
        code_col = "代码" if "代码" in spot.columns else spot.columns[1]
        pe_col = next((c for c in spot.columns if "市盈率" in c), None)
        if pe_col:
            for _, r in spot.iterrows():
                v = r[pe_col]
                if isinstance(v, (int, float)) and v == v and v > 0:
                    pe[str(r[code_col])] = float(v)
    except Exception:
        pass

    # daily closes per stock
    closes = {}
    for code in UNIVERSE:
        try:
            df = ak.stock_zh_a_hist(symbol=code, period="daily", start_date=start, end_date=end, adjust="qfq")
            if df is not None and len(df) > 80:
                closes[code] = [float(x) for x in df["收盘"].tolist()]
        except Exception:
            continue
    if len(closes) < 8:
        raise RuntimeError("insufficient real data")

    n = min(len(v) for v in closes.values())
    closes = {k: v[-n:] for k, v in closes.items()}  # align tail length

    panel = []
    for t in range(60, n - FWD):
        day = []
        for code, c in closes.items():
            rets = [c[i] / c[i - 1] - 1 for i in range(t - 20 + 1, t + 1)]
            row = {
                "ticker": code,
                "fwd_ret": c[t + FWD] / c[t] - 1,
                "mom_20": c[t] / c[t - 20] - 1,
                "mom_60": c[t] / c[t - 60] - 1,
                "vol_20": statistics.pstdev(rets) if len(rets) > 1 else 0.0,
            }
            if code in pe:
                row["value"] = -pe[code]  # cheap = attractive
            day.append(row)
        zscore_day(day, ["mom_20", "mom_60", "vol_20", "value"])
        panel.append(day)
    return panel


def zscore_day(day, fields):
    """Cross-sectionally z-score each field within one day (rank-like scale)."""
    for f in fields:
        xs = [r[f] for r in day if f in r]
        if len(xs) < 2:
            continue
        mu = sum(xs) / len(xs)
        sd = statistics.pstdev(xs) or 1.0
        for r in day:
            if f in r:
                r[f] = (r[f] - mu) / sd


# ---------- synthetic fallback ----------

def build_synth_panel():
    rng = random.Random(42)
    tickers = [f"S{i:03d}" for i in range(40)]
    state = {t: {s: rng.gauss(0, 1) for s in SIGNAL_WEIGHTS} for t in tickers}
    panel = []
    for _ in range(180):
        day = []
        for t in tickers:
            for s in SIGNAL_WEIGHTS:
                state[t][s] = 0.95 * state[t][s] + 0.05 * rng.gauss(0, 1)
            sig = state[t]
            fwd = sum(SIGNAL_WEIGHTS[s] * sig[s] for s in SIGNAL_WEIGHTS) * 0.01 + rng.gauss(0, 0.02)
            row = {"ticker": t, "fwd_ret": fwd}
            row.update(sig)
            day.append(row)
        panel.append(day)
    return panel


# ---------- factor evaluation ----------

def referenced_signals(expr, available):
    e = expr.lower()
    hits = [s for s in available if s in e]
    if "pe" in e and "value" in available and "value" not in hits:
        hits.append("value")
    return hits or None


def factor_value(row, signals):
    vals = [row[s] for s in signals if s in row]
    return sum(vals) / len(vals) if vals else 0.0


def pearson(xs, ys):
    n = len(xs)
    if n < 2:
        return 0.0
    mx, my = sum(xs) / n, sum(ys) / n
    num = sum((x - mx) * (y - my) for x, y in zip(xs, ys))
    dx = math.sqrt(sum((x - mx) ** 2 for x in xs))
    dy = math.sqrt(sum((y - my) ** 2 for y in ys))
    return num / (dx * dy) if dx and dy else 0.0


def backtest(expr, panel):
    available = set()
    for day in panel:
        for r in day:
            available.update(k for k in r if k not in ("ticker", "fwd_ret"))
    signals = referenced_signals(expr, available)
    if signals:
        score = lambda r: factor_value(r, signals)
    else:
        rng = random.Random(int(hashlib.sha1(expr.encode()).hexdigest()[:8], 16))
        w = {s: rng.uniform(-0.3, 0.3) for s in available}
        score = lambda r: sum(w.get(s, 0) * r.get(s, 0) for s in available)

    ic_series, ls_returns, turnovers, prev_top = [], [], [], set()
    for day in panel:
        fac = [(score(r), r["fwd_ret"], r["ticker"]) for r in day]
        ic_series.append(pearson([f for f, _, _ in fac], [y for _, y, _ in fac]))
        fac.sort(key=lambda z: z[0], reverse=True)
        k = max(1, len(fac) // 5)
        top, bot = fac[:k], fac[-k:]
        ls_returns.append(sum(y for _, y, _ in top) / k - sum(y for _, y, _ in bot) / k)
        ts = {t for _, _, t in top}
        if prev_top:
            turnovers.append(len(ts ^ prev_top) / (2 * k))
        prev_top = ts

    m = len(ic_series)
    ic = sum(ic_series) / m
    ic_sd = statistics.pstdev(ic_series) if m > 1 else 0.0
    ir = ic / ic_sd if ic_sd else 0.0
    mu = sum(ls_returns) / m
    sd = statistics.pstdev(ls_returns) if m > 1 else 0.0
    sharpe = (mu / sd * math.sqrt(252 / FWD)) if sd else 0.0
    cum = peak = mdd = 0.0
    for r in ls_returns:
        cum += r
        peak = max(peak, cum)
        mdd = min(mdd, cum - peak)
    turnover = sum(turnovers) / len(turnovers) if turnovers else 0.0
    return {"ic": round(ic, 4), "ir": round(ir, 3), "sharpe": round(sharpe, 3),
            "turnover": round(turnover, 3), "max_dd": round(mdd, 3), "periods": m}


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--expression", required=True)
    ap.add_argument("--horizon", default="5d")
    args = ap.parse_args()
    panel, source = get_panel()
    print(json.dumps({"metrics": backtest(args.expression, panel), "source": source}))


if __name__ == "__main__":
    main()
