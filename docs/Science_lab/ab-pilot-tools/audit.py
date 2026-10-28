import glob
import json
import os
import re
import statistics
import sys

sys.stdout.reconfigure(encoding="utf-8")
HOME = os.path.expanduser("~")
PROJ = os.path.join(HOME, ".claude", "projects")
import argparse

_ap = argparse.ArgumentParser()
_ap.add_argument("--bench-dir", default=r"C:\kostja\GoProjects\cm-bench", help="stand folder with _logs/")
_ap.add_argument("--project", default="bench-cm", help="manager project name of the stand")
_ap.add_argument("--clone-b", default=None, help="arm B clone folder (default <bench-dir>\b)")
_args = _ap.parse_args()
BENCH = _args.bench_dir
SESSION_TAG = _args.project + "/Developer 1"
LOG = os.path.join(HOME, ".claude-manager", "logs", "app.log")


def find(sid):
    hits = glob.glob(os.path.join(PROJ, "*", sid + ".jsonl"))
    return hits[0] if hits else None


def lines(path):
    for line in open(path, encoding="utf-8", errors="replace"):
        try:
            yield json.loads(line)
        except Exception:
            continue


def first_usage(path):
    for o in lines(path):
        m = o.get("message") or {}
        if m.get("role") == "assistant" and m.get("usage"):
            u = m["usage"]
            return (u.get("input_tokens", 0) + u.get("cache_creation_input_tokens", 0) + u.get("cache_read_input_tokens", 0),
                    u.get("cache_creation_input_tokens", 0), m.get("model"))
    return None


def first_user_text(path):
    for o in lines(path):
        m = o.get("message") or {}
        if m.get("role") == "user":
            c = m.get("content")
            if isinstance(c, str):
                return c
            if isinstance(c, list):
                for b in c:
                    if b.get("type") == "text":
                        return b.get("text", "")
    return ""


def blocked_results(path):
    n = 0
    for o in lines(path):
        m = o.get("message") or {}
        c = m.get("content")
        if m.get("role") == "user" and isinstance(c, list):
            for b in c:
                if b.get("type") == "tool_result":
                    t = b.get("content")
                    t = t if isinstance(t, str) else " ".join(x.get("text", "") for x in t if isinstance(x, dict)) if isinstance(t, list) else ""
                    if re.search(r"BLOCKED|hook error|cmdguard|blocked by|PreToolUse", t or ""):
                        n += 1
    return n


# session ids
a_sids = []
for t in ("T1", "T2", "T3", "T4", "T5"):
    for f in glob.glob(os.path.join(BENCH, "_logs", f"A-{t}-*.jsonl")):
        for o in lines(f):
            if o.get("session_id"):
                a_sids.append(o["session_id"])
                break
b_sids = []
for line in open(LOG, encoding="utf-8", errors="replace"):
    if SESSION_TAG in line and "msg=session.init" in line:
        m = re.search(r"cli_session_id=([0-9a-f-]+)", line)
        if m:
            b_sids.append(m.group(1))
b_dev = b_sids[:11]

for label, sids in (("A", a_sids), ("B dev", b_dev)):
    fu = [first_usage(find(s)) for s in sids if find(s)]
    fu = [x for x in fu if x]
    ctx = [x[0] for x in fu]
    print(f"{label}: first-call context per session (tokens): median {statistics.median(ctx):.0f}, min {min(ctx)}, max {max(ctx)}; "
          f"cache-written on the first call: median {statistics.median([x[1] for x in fu]):.0f}")
    blk = sum(blocked_results(find(s)) for s in sids if find(s))
    print(f"   tool results mentioning a hook/guard block: {blk}")

known = set(a_sids) | set(b_sids)
print("\nunmatched transcripts in the b project (background / other):")
for p in sorted(glob.glob(os.path.join(PROJ, re.sub(r"[^A-Za-z0-9]", "-", _args.clone_b or os.path.join(BENCH, "b")) + "*", "*.jsonl"))):
    sid = os.path.basename(p)[:-6]
    if sid in known:
        continue
    fu = first_usage(p)
    txt = first_user_text(p).replace("\n", " ")[:170]
    print(f"  {sid[:8]} model={fu[2] if fu else '?'} ctx={fu[0] if fu else '?'} | {txt}")
