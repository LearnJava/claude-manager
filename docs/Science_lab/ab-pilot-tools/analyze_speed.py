"""Where does the time go: per task, tool time vs model time, arm A vs arm B (read-only)."""
import datetime as dt
import glob
import json
import os
import re
import sys

sys.stdout.reconfigure(encoding="utf-8")
HOME = os.path.expanduser("~")
PROJ = os.path.join(HOME, ".claude", "projects")
import argparse

_ap = argparse.ArgumentParser()
_ap.add_argument("--bench-dir", default=r"C:\kostja\GoProjects\cm-bench", help="stand folder with _logs/")
_ap.add_argument("--project", default="bench-cm", help="manager project name of the stand")
_ap.add_argument("windows", nargs="*", help="e.g. A:T5 B:T5.3 (default A:T2 B:T2)")
_args = _ap.parse_args()
BENCH = _args.bench_dir
SESSION_TAG = _args.project + "/Developer 1"
LOG = os.path.join(HOME, ".claude-manager", "logs", "app.log")
B_TASKS = ["T1", "T2", "T3.1", "T3.2", "T4.1", "T4.2", "T4.3", "T5.1", "T5.2", "T5.3", "T5.4"]
HEAVY = 60.0  # seconds: a tool call longer than this is a build/test/wait


def parse_ts(s):
    return dt.datetime.fromisoformat(s.replace("Z", "+00:00"))


def union(iv):
    iv = sorted(iv)
    total, cur_s, cur_e = 0.0, None, None
    for s, e in iv:
        if cur_e is None or s > cur_e:
            if cur_e is not None:
                total += (cur_e - cur_s).total_seconds()
            cur_s, cur_e = s, e
        else:
            cur_e = max(cur_e, e)
    if cur_e is not None:
        total += (cur_e - cur_s).total_seconds()
    return total


def analyze(path):
    uses, ivs, heavy_iv, ids, calls = {}, [], [], set(), []
    t0 = t1 = None
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            try:
                o = json.loads(line)
            except Exception:
                continue
            if not o.get("timestamp"):
                continue
            ts = parse_ts(o["timestamp"])
            t0 = ts if t0 is None else min(t0, ts)
            t1 = ts if t1 is None else max(t1, ts)
            m = o.get("message") or {}
            c = m.get("content")
            if not isinstance(c, list):
                continue
            if m.get("role") == "assistant":
                if m.get("id"):
                    ids.add(m["id"])
                for b in c:
                    if b.get("type") == "tool_use":
                        i = b.get("input") or {}
                        cmd = i.get("command") or i.get("file_path") or i.get("pattern") or b.get("name")
                        uses[b["id"]] = (ts, b.get("name"), str(cmd).replace("\n", " ")[:90])
            elif m.get("role") == "user":
                for b in c:
                    if b.get("type") == "tool_result" and b.get("tool_use_id") in uses:
                        s, name, cmd = uses.pop(b["tool_use_id"])
                        ivs.append((s, ts))
                        d = (ts - s).total_seconds()
                        if d > HEAVY:
                            heavy_iv.append((s, ts))
                        calls.append((d, name, cmd))
    if t0 is None:
        return None
    calls.sort(reverse=True)
    return {"span": (t1 - t0).total_seconds(), "tool": union(ivs), "heavy": union(heavy_iv),
            "turns": len(ids), "top": calls[:3], "n_tools": len(calls)}


def find(sid):
    hits = glob.glob(os.path.join(PROJ, "*", sid + ".jsonl"))
    return hits[0] if hits else None


# arm A: session id of each task from its stream log
a_by_task = {}
for t in ("T1", "T2", "T3", "T4", "T5"):
    sid = None
    for f in glob.glob(os.path.join(BENCH, "_logs", f"A-{t}-*.jsonl")):
        with open(f, encoding="utf-8", errors="replace") as fh:
            for line in fh:
                try:
                    o = json.loads(line)
                except Exception:
                    continue
                if o.get("session_id"):
                    sid = o["session_id"]
                    break
    p = find(sid) if sid else None
    a_by_task[t] = analyze(p) if p else None

# arm B: one run per sub-task, in order; gate = process_exit -> task_done
runs, cur, last_exit = [], None, None
inits = []
with open(LOG, encoding="utf-8", errors="replace") as f:
    for line in f:
        if SESSION_TAG not in line:
            continue
        m = re.match(r"time=(\S+) level=\S+ msg=(\S+)", line)
        if not m:
            continue
        t = dt.datetime.fromisoformat(m.group(1))
        k = m.group(2)
        if k == "session.init":
            sid = re.search(r"cli_session_id=([0-9a-f-]+)", line)
            cur = {"sid": sid.group(1) if sid else None, "spawn": None, "exit": None, "done": None}
            runs.append(cur)
        elif k == "session.spawned" and cur and cur["spawn"] is None:
            cur["spawn"] = t
        elif k == "session.process_exit" and cur and cur["exit"] is None:
            cur["exit"] = t
        elif k == "session.run.task_done" and cur and cur["done"] is None:
            cur["done"] = t
b_by_task = {}
for i, r in enumerate(runs[: len(B_TASKS)]):
    p = find(r["sid"]) if r["sid"] else None
    a = analyze(p) if p else None
    gate = (r["done"] - r["exit"]).total_seconds() if r["done"] and r["exit"] else None
    b_by_task[B_TASKS[i]] = (a, gate)


def mins(x):
    return "   - " if x is None else f"{x/60:5.1f}"


groups = {"T1": ["T1"], "T2": ["T2"], "T3": ["T3.1", "T3.2"], "T4": ["T4.1", "T4.2", "T4.3"],
          "T5": ["T5.1", "T5.2", "T5.3", "T5.4"]}
print("minutes: span = transcript first..last line; tools = union of tool-call time; heavy = calls > 60 s")
print("task | A span  tools heavy turns | B span  tools heavy turns  gate  B span+gate | B/A")
for g, subs in groups.items():
    a = a_by_task.get(g)
    bs = [b_by_task[s] for s in subs if s in b_by_task and b_by_task[s][0]]
    if not a or not bs:
        print(g, "no data", bool(a), len(bs))
        continue
    bsp = sum(x[0]["span"] for x in bs)
    btl = sum(x[0]["tool"] for x in bs)
    bhv = sum(x[0]["heavy"] for x in bs)
    btn = sum(x[0]["turns"] for x in bs)
    bgt = sum(x[1] or 0 for x in bs)
    part = "" if len(bs) == len(subs) else f" (only {len(bs)}/{len(subs)} subtasks)"
    print(f"{g:4s} | {mins(a['span'])} {mins(a['tool'])} {mins(a['heavy'])} {a['turns']:5d} | "
          f"{mins(bsp)} {mins(btl)} {mins(bhv)} {btn:5d} {mins(bgt)} {mins(bsp + bgt)}    | {(bsp + bgt) / a['span']:.1f}x{part}")

# longest tool calls: `python -I analyze_speed.py A:T5 B:T5.1 B:T5.3` (default: A:T2 B:T2)
for w in _args.windows or ["A:T2", "B:T2"]:
    arm, _, name = w.partition(":")
    x = a_by_task.get(name) if arm.upper() == "A" else (b_by_task.get(name) or (None,))[0]
    if x:
        print(f"\n{arm.upper()} {name}: {x['span']/60:.1f} min, {x['n_tools']} tool calls, {x['turns']} turns; longest tool calls")
        for d, name_, cmd in x["top"]:
            print(f"  {d/60:5.1f} min  {name_}: {cmd}")
