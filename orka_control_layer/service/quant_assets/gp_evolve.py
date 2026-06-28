#!/usr/bin/env python3
"""Phase-1 reference genetic-programming factor search (pure stdlib).

Evolves a seed factor expression toward higher backtest fitness by mutating
which base signals it combines, then re-scoring with backtest_runner. Prints the
best factor as a JSON object on the last stdout line:

    {"expression": "...", "fitness": ..., "metrics": {...}, "source": "python"}

Phase-2 upgrade: a real GP over a proper operator/operand tree against live data.
"""
import argparse, json, random, hashlib
from backtest_runner import backtest, SIGNAL_WEIGHTS

BASE = list(SIGNAL_WEIGHTS.keys())  # mom_20, roe, value, vol_20


def fitness(metrics: dict) -> float:
    # reward predictive power (IC/IR) and risk-adjusted return, penalize churn
    return round(8 * metrics["ic"] + 0.05 * metrics["sharpe"] - 0.5 * metrics["turnover"], 4)


def render(genes: list) -> str:
    # genes: list of (signal, weight) → a ranked linear combination
    parts = [f"{w:+.1f}*rank({s})" for s, w in genes]
    return " ".join(parts).lstrip("+ ")


def seed_genes(expr: str) -> list:
    e = expr.lower()
    genes = [(s, 1.0) for s in BASE if s in e]
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
    base = seed_genes(seed_expr)
    pop = [base] + [mutate(base, rng) for _ in range(pop_size - 1)]
    best, best_fit, best_m = base, -1e9, None
    for _ in range(generations):
        scored = []
        for genes in pop:
            m = backtest(render(genes))
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
