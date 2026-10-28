"""Dollars by category (cache write / cache read / output) and calls by tool, arm A against arm B.

Reads bench.json (the output of cm-bench/_kit/bench_measure.py --json); the default is the pilot's copy.
Usage: python -I cost.py [--bench-json path/to/bench.json]
"""
import argparse
import collections
import json
import sys
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
LAB = Path(__file__).resolve().parents[1]  # docs/Science_lab
sys.path.insert(0, str(LAB / "claude-code-setup" / "tools" / "measure"))
import cost_attribution as ca  # noqa: E402

_ap = argparse.ArgumentParser()
_ap.add_argument("bench_json_pos", nargs="?", help="same as --bench-json")
_ap.add_argument("--bench-json", default=None, help="bench_measure.py --json output")
_args = _ap.parse_args()
bench = Path(_args.bench_json or _args.bench_json_pos or LAB / "measurements" / "2026-10-28-ab-pilot" / "bench.json")
d = json.load(open(bench, encoding="utf-8"))
print("PRICES:", {k: v for k, v in ca.PRICES.items() if "sonnet" in k or "opus" in k})


def split(entry):
    """dollars by category for one entry, priced per model of the entry (calls-weighted)."""
    models = entry.get("models") or {}
    total_calls = sum(models.values()) or 1
    cat = collections.Counter()
    for m, n in models.items():
        p = ca.price(m)
        if not p:
            continue
        w = n / total_calls
        pin, pout = p[0], p[1]
        cat["input"] += w * entry["in"] * pin / 1e6
        cat["output"] += w * entry["out"] * pout / 1e6
        cat["cache_read"] += w * entry["cread"] * pin * ca.READ / 1e6
        cat["cache_write"] += w * (entry["w1h"] * ca.W1H + entry["w5"] * ca.W5) * pin / 1e6
    return cat


for arm in ("A", "B"):
    tot = collections.Counter()
    tools = collections.Counter()
    sess = 0
    for task, e in d[arm].items():
        c = split(e)
        for k, v in c.items():
            tot[k] += v
        for k, v in (e.get("tools") or {}).items():
            tools[k] += v
        sess += e.get("sessions", 0)
    s = sum(tot.values())
    print(f"\n{arm}: {sess} sessions, priced ${s:.2f}: " + ", ".join(f"{k} ${v:.2f} ({100*v/s:.0f}%)" for k, v in tot.most_common()))
    print("  calls by tool:", dict(tools.most_common(9)))
    if arm == "B":
        for k in ("plan", "background", "other"):
            e = d["B"][k]
            c = split(e)
            print(f"  {k}: ${sum(c.values()):.2f} " + ", ".join(f"{a} ${b:.2f}" for a, b in c.most_common(3)), "| sessions", e.get("sessions"), "calls", e["calls"])
