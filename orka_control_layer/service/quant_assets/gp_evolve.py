#!/usr/bin/env python3
"""Phase-1 reference genetic-programming factor search (pure stdlib).

Evolves a seed factor expression toward higher backtest fitness by mutating
which base signals it combines, then re-scoring with backtest_runner. Prints the
best factor as a JSON object on the last stdout line:

    {"expression": "...", "fitness": ..., "metrics": {...}, "source": "python"}

Phase-2 upgrade: a real GP over a proper operator/operand tree against live data.
"""
import argparse, json, random, hashlib
from backtest_runner import backtest, get_panel, SIGNAL_WEIGHTS

BASE = list(SIGNAL_WEIGHTS.keys())  # mom_20, roe, value, vol_20


def fitness(metrics: dict) -> float:
    # reward predictive power (IC/IR) and risk-adjusted return, penalize churn
    return round(8 * metrics["ic"] + 0.05 * metrics["sharpe"] - 0.5 * metrics["turnover"], 4)


def render(genes: list) -> str:
    # genes: list of (signal, weight) → a ranked linear combination
    parts = [f"{w:+.1f}*rank({s})" for s, w in genes]
    return " ".join(parts).lstrip("+ ")


# Analysts write "pe_ttm", "vol_20d", "roe_ttm" — none of which are substrings of
# the base signal names, so a literal match silently fell through to a RANDOM
# signal and collapsed unrelated theses onto the same factor. Map the vocabulary
# explicitly, and honour a leading minus (cheap = high value, low vol = good).
SIGNAL_ALIASES = {
    "mom_20": ["mom_20", "mom20", "momentum", "ret_20", "动量"],
    "roe": ["roe", "roa", "profitab", "quality", "盈利", "质量"],
    "value": ["value", "pe", "pb", "ep", "bp", "valuation", "估值", "价值"],
    "vol_20": ["vol_20", "vol20", "volatility", "std_20", "波动"],
}
# Signals whose ATTRACTIVE direction is the negative of the raw field.
INVERTED_FIELDS = ("pe", "pb", "vol", "std", "波动")


def seed_genes(expr: str) -> list:
    e = expr.lower()
    genes = []
    for sig, aliases in SIGNAL_ALIASES.items():
        hit = next((a for a in aliases if a in e), None)
        if not hit:
            continue
        # "rank(-pe)" / "-vol_20" already encode "lower is better"; so does an
        # inverted field name on its own. Fold that into the weight's sign.
        idx = e.find(hit)
        negated = "-" in e[max(0, idx - 3):idx]
        inverted = hit.startswith(INVERTED_FIELDS) or any(hit.startswith(f) for f in INVERTED_FIELDS)
        w = -1.0 if (negated != inverted) and sig in ("value", "vol_20") else 1.0
        genes.append((sig, w))
    if not genes:
        rng = random.Random(int(hashlib.sha1(expr.encode()).hexdigest()[:8], 16))
        genes = [(rng.choice(BASE), 1.0)]
    return genes


def mutate(genes: list, rng: random.Random) -> list:
    g = list(genes)
    op = rng.random()
    if op < 0.4 and len(g) < 4:                      # add a signal
        avail = [s for s in BASE if s not in {x[0] for x in g}]
        if avail:
            g.append((rng.choice(avail), round(rng.choice([0.2, 0.5, 1.0]), 1)))
    elif op < 0.7 and len(g) > 1:                    # drop a signal
        g.pop(rng.randrange(len(g)))
    else:                                            # tweak a weight
        i = rng.randrange(len(g))
        s, w = g[i]
        g[i] = (s, round(max(-1.0, min(1.0, w + rng.choice([-0.5, -0.2, 0.2, 0.5]))), 1))
    return g


def evolve(seed_expr: str, generations: int, pop_size: int):
    rng = random.Random(7)
    panel, _ = get_panel()  # fetch real data ONCE; every candidate reuses it
    base = seed_genes(seed_expr)
    pop = [base] + [mutate(base, rng) for _ in range(pop_size - 1)]
    best, best_fit, best_m = base, -1e9, None
    for _ in range(generations):
        scored = []
        for genes in pop:
            m = backtest(render(genes), panel)
            f = fitness(m)
            scored.append((f, genes, m))
            if f > best_fit:
                best_fit, best, best_m = f, genes, m
        scored.sort(key=lambda z: z[0], reverse=True)
        parents = [g for _, g, _ in scored[: max(2, pop_size // 2)]]
        pop = parents + [mutate(rng.choice(parents), rng) for _ in range(pop_size - len(parents))]
    return render(best), best_fit, best_m


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--seed-expression", required=True)
    ap.add_argument("--generations", type=int, default=6)
    ap.add_argument("--pop-size", type=int, default=8)
    args = ap.parse_args()
    expr, fit, metrics = evolve(args.seed_expression, args.generations, args.pop_size)
    print(json.dumps({"expression": expr, "fitness": fit, "metrics": metrics, "source": "python"}))


if __name__ == "__main__":
    main()
