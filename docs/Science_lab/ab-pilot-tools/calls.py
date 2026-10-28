import collections
import glob
import json
import os
import re
import sys

sys.stdout.reconfigure(encoding="utf-8")
HOME = os.path.expanduser("~")
PROJ = os.path.join(HOME, ".claude", "projects")
BENCH = r"C:\kostja\GoProjects\cm-bench"
LOG = os.path.join(HOME, ".claude-manager", "logs", "app.log")

CATS = [
    ("tests/builds", re.compile(r"\bgo (test|build|vet|run)\b|gofmt|npm |npx |playwright|wails")),
    ("git", re.compile(r"\bgit\b")),
    ("queue/protocol files", re.compile(r"STATUS-P1|BENCH-TASKS|\.claude/(skills|worktrees)|worktree")),
    ("file writes via shell", re.compile(r"cat\s*>|sed -i|<<'?EOF|python")),
]
EDITS = {"Edit", "Write", "MultiEdit", "NotebookEdit"}


def find(sid):
    hits = glob.glob(os.path.join(PROJ, "*", sid + ".jsonl"))
    return hits[0] if hits else None


def calls_of(path):
    out = []
    ids = set()
    with open(path, encoding="utf-8", errors="replace") as f:
        for line in f:
            try:
                o = json.loads(line)
            except Exception:
                continue
            m = o.get("message") or {}
            c = m.get("content")
            if m.get("role") != "assistant" or not isinstance(c, list):
                continue
            for b in c:
                if b.get("type") == "tool_use":
                    i = b.get("input") or {}
                    out.append((b.get("name"), str(i.get("command") or i.get("skill") or i.get("file_path") or "")))
    return out


def classify(name, cmd):
    if name in EDITS:
        return "edit/write tool"
    if name == "Skill":
        return "skill (" + cmd.split()[0] + ")" if cmd else "skill"
    if name != "Bash":
        return "read/search tools"
    # heredoc writes are file writes whatever else the command contains
    if re.search(r"cat\s*>|<<'?EOF|sed -i", cmd):
        return "file writes via shell"
    for label, rx in CATS:
        if rx.search(cmd):
            return label
    return "other shell"


# arm A sessions
a_sids = []
for t in ("T1", "T2", "T3", "T4", "T5"):
    for f in glob.glob(os.path.join(BENCH, "_logs", f"A-{t}-*.jsonl")):
        for line in open(f, encoding="utf-8", errors="replace"):
            try:
                o = json.loads(line)
            except Exception:
                continue
            if o.get("session_id"):
                a_sids.append(o["session_id"])
                break
# arm B developer sessions (first 11 inits)
b_sids = []
for line in open(LOG, encoding="utf-8", errors="replace"):
    if "bench-cm/Developer 1" in line and "msg=session.init" in line:
        m = re.search(r"cli_session_id=([0-9a-f-]+)", line)
        if m:
            b_sids.append(m.group(1))
b_sids = b_sids[:11]


def report(arm, sids):
    cats = collections.Counter()
    before, after, total = [], [], 0
    for sid in sids:
        p = find(sid)
        if not p:
            continue
        cs = calls_of(p)
        total += len(cs)
        for n, c in cs:
            cats[classify(n, c)] += 1
        idx = [i for i, (n, c) in enumerate(cs) if n in EDITS or re.search(r"cat\s*>|<<'?EOF|sed -i", c)]
        if idx:
            before.append(idx[0])
            after.append(len(cs) - 1 - idx[-1])
    n = len(before) or 1
    print(f"\n{arm}: {len(sids)} sessions, {total} calls ({total/len(sids):.1f} per session)")
    for k, v in cats.most_common():
        print(f"  {k:28s} {v:4d}  ({100*v/total:.0f}%)")
    print(f"  calls before the first write: avg {sum(before)/n:.1f} per session; after the last write: avg {sum(after)/n:.1f}")


report("A", a_sids)
report("B (Developer 1 sessions)", b_sids)
