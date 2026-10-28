import datetime as dt
import glob
import json
import os
import re
import statistics
import sys

sys.stdout.reconfigure(encoding="utf-8")
HOME = os.path.expanduser("~")
PROJ = os.path.join(HOME, ".claude", "projects")
BENCH = r"C:\kostja\GoProjects\cm-bench"
LOG = os.path.join(HOME, ".claude-manager", "logs", "app.log")


def find(sid):
    hits = glob.glob(os.path.join(PROJ, "*", sid + ".jsonl"))
    return hits[0] if hits else None


def ts(o):
    return dt.datetime.fromisoformat(o["timestamp"].replace("Z", "+00:00"))


def measure(paths):
    tool_d, gaps = [], []
    for p in paths:
        uses = {}
        last_result = None
        seen_msg = set()
        for line in open(p, encoding="utf-8", errors="replace"):
            try:
                o = json.loads(line)
            except Exception:
                continue
            if not o.get("timestamp"):
                continue
            m = o.get("message") or {}
            c = m.get("content")
            if not isinstance(c, list):
                continue
            t = ts(o)
            if m.get("role") == "assistant":
                first = False
                for b in c:
                    if b.get("type") == "tool_use":
                        uses[b["id"]] = (t, b.get("name"))
                        if not first and m.get("id") not in seen_msg:
                            first = True
                            seen_msg.add(m.get("id"))
                            if last_result is not None:
                                gaps.append((t - last_result).total_seconds())
            elif m.get("role") == "user":
                for b in c:
                    if b.get("type") == "tool_result":
                        last_result = t
                        u = uses.pop(b.get("tool_use_id"), None)
                        if u:
                            tool_d.append((t - u[0]).total_seconds())
    return tool_d, gaps


def stats(label, xs, cap):
    xs = sorted(x for x in xs if 0 <= x <= cap)
    if not xs:
        return
    p90 = xs[int(0.9 * (len(xs) - 1))]
    print(f"  {label:28s} n={len(xs):4d}  median {statistics.median(xs):5.1f} s  mean {statistics.mean(xs):5.1f} s  p90 {p90:5.1f} s")


a_paths = []
for t in ("T1", "T2", "T3", "T4", "T5"):
    for f in glob.glob(os.path.join(BENCH, "_logs", f"A-{t}-*.jsonl")):
        for line in open(f, encoding="utf-8", errors="replace"):
            try:
                o = json.loads(line)
            except Exception:
                continue
            if o.get("session_id"):
                p = find(o["session_id"])
                if p:
                    a_paths.append(p)
                break
b_sids = []
for line in open(LOG, encoding="utf-8", errors="replace"):
    if "bench-cm/Developer 1" in line and "msg=session.init" in line:
        m = re.search(r"cli_session_id=([0-9a-f-]+)", line)
        if m:
            b_sids.append(m.group(1))
b_paths = [p for p in (find(s) for s in b_sids[:11]) if p]

for label, paths in (("A", a_paths), ("B", b_paths)):
    tool_d, gaps = measure(paths)
    print(f"{label}: {len(paths)} sessions")
    stats("tool call, calls under 60 s", tool_d, 60)
    stats("tool call, calls under 5 s", tool_d, 5)
    stats("model gap per turn (<120 s)", gaps, 120)
