"""Cost vs session length (number of API calls) over Claude Code transcripts."""
import collections
import json
import re
import sys
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
sys.path.insert(0, str(Path(__file__).parent))
from cost_attribution import price, W1H, W5, READ  # noqa: E402

root, out = Path(sys.argv[1]), Path(sys.argv[2])
BUCKETS = [(1, 20), (21, 50), (51, 100), (101, 150), (151, 250), (251, 10**6)]
agg = {b: collections.Counter() for b in BUCKETS}
for f in root.glob("*.jsonl"):
    seen, cost, calls, ctx = set(), 0.0, 0, 0
    with f.open(encoding="utf-8", errors="replace") as fh:
        for line in fh:
            if '"assistant"' not in line or '"usage"' not in line:
                continue
            try:
                d = json.loads(line)
            except ValueError:
                continue
            msg = d.get("message") or {}
            mid = msg.get("id")
            if mid in seen:
                continue
            seen.add(mid)
            pr, u = price(msg.get("model")), msg.get("usage") or {}
            if not pr or not u:
                continue
            pin, pout = pr
            cc = u.get("cache_creation") or {}
            w1h, w5 = cc.get("ephemeral_1h_input_tokens"), cc.get("ephemeral_5m_input_tokens")
            cw = u.get("cache_creation_input_tokens", 0) or 0
            if w1h is None and w5 is None:
                w1h, w5 = cw, 0
            cr = u.get("cache_read_input_tokens", 0) or 0
            cost += ((u.get("input_tokens", 0) or 0) * pin + (w5 * W5 + w1h * W1H) * pin + cr * READ * pin
                     + (u.get("output_tokens", 0) or 0) * pout) / 1e6
            calls += 1
            ctx += cr + cw + (u.get("input_tokens", 0) or 0)
    if not calls:
        continue
    for b in BUCKETS:
        if b[0] <= calls <= b[1]:
            a = agg[b]
            a["sessions"] += 1; a["calls"] += calls; a["usd"] += cost; a["ctx"] += ctx
total = sum(a["usd"] for a in agg.values())
res = []
for (lo, hi), a in agg.items():
    if not a["sessions"]:
        continue
    res.append({"calls": f"{lo}-{hi if hi < 10**6 else '+'}", "sessions": a["sessions"],
                "usd_total": round(a["usd"], 1), "pct_of_spend": round(100 * a["usd"] / total, 1),
                "usd_per_session": round(a["usd"] / a["sessions"], 2),
                "usd_per_call": round(a["usd"] / a["calls"], 4),
                "avg_context_tokens_per_call": int(a["ctx"] / a["calls"])})
out.write_text(json.dumps(res, indent=1), encoding="utf-8")
for r in res:
    print(r)
