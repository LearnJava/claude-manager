"""LAB-01: where cache-write dollars go - session start or conversation growth.

Per model call (usage of a transcript line, deduplicated by message id like bench_measure.py) the cache write is
priced as (w5*1.25 + w1h*2.0) * input price and put into one bucket:
  a  first call of a session's main transcript (system prompt, CLAUDE.md, tools, the task prompt)
  s  calls of subagent transcripts (their own start + growth; reported apart, they are not the main conversation)
  d  re-write: a call that is not the first of its transcript, came after a pause longer than the 5-minute TTL
     and re-created at least half of the previous context (cache expired or prompt changed)
  b  calls before the first Edit/Write/MultiEdit/NotebookEdit (exploration)
  c  everything else (after the first edit)
Buckets are checked in the order a, s, d, b, c.

Usage (python -I):
  cache_write_split.py ab [path/to/cm-bench]     A/B pilot, reconciled against ab-pilot-tools/cost.py (bench.json)
  cache_write_split.py roles [days] [--json f]   all manager sessions of the last N days (default 30), by role
Reads ~/.claude/projects and ~/.claude-manager/history.db (read-only). Prints only totals.
"""
import collections
import datetime as dt
import json
import re
import sqlite3
import statistics
import sys
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
LAB = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(LAB / "claude-code-setup" / "tools" / "measure"))
from cost_attribution import READ, W1H, W5, price  # noqa: E402

PROJECTS = Path.home() / ".claude" / "projects"
DB = Path.home() / ".claude-manager" / "history.db"
EDIT_TOOLS = {"Edit", "Write", "MultiEdit", "NotebookEdit"}
TTL_S = 300
BUCKETS = ("a", "s", "d", "b", "c")


def ts_of(s):
    try:
        return dt.datetime.fromisoformat(s.replace("Z", "+00:00")).timestamp()
    except (ValueError, AttributeError):
        return None


def scan_file(path, sub):
    """Returns (list of call dicts in file order, first user prompt)."""
    calls, seen, edited, prev, first_prompt = [], set(), False, None, ""
    with path.open(encoding="utf-8", errors="replace") as fh:
        for line in fh:
            try:
                d = json.loads(line)
            except ValueError:
                continue
            msg = d.get("message") or {}
            content = msg.get("content")
            if d.get("type") == "user" and not first_prompt:
                t = content if isinstance(content, str) else " ".join(
                    b.get("text", "") for b in content or [] if isinstance(b, dict) and b.get("type") == "text")
                first_prompt = t.strip()[:300]
            if d.get("type") != "assistant":
                continue
            mid = msg.get("id") or d.get("requestId")
            u, pr = msg.get("usage"), price(msg.get("model"))
            if u and pr and mid not in seen:
                seen.add(mid)
                cc = u.get("cache_creation") or {}
                cw = u.get("cache_creation_input_tokens", 0) or 0
                w1h, w5 = cc.get("ephemeral_1h_input_tokens"), cc.get("ephemeral_5m_input_tokens")
                if w1h is None and w5 is None:
                    w1h, w5 = cw, 0
                w1h, w5 = w1h or 0, w5 or 0
                cr = u.get("cache_read_input_tokens", 0) or 0
                it = u.get("input_tokens", 0) or 0
                ot = u.get("output_tokens", 0) or 0
                t = ts_of(d.get("timestamp"))
                pin, pout = pr
                if sub:
                    bucket = "s"
                elif prev is None:
                    bucket = "a"
                else:
                    gap = (t - prev["t"]) if (t and prev["t"]) else 0
                    if gap > TTL_S and (w5 + w1h) >= 0.5 * prev["ctx"]:
                        bucket = "d"
                    else:
                        bucket = "c" if edited else "b"
                call = {"b": bucket, "t": t, "ctx": cr + w5 + w1h + it,
                        "w": (w5 * W5 + w1h * W1H) * pin / 1e6, "w1h": w1h, "w5": w5,
                        "r": cr * READ * pin / 1e6, "o": ot * pout / 1e6, "i": it * pin / 1e6}
                calls.append(call)
                prev = call
            for b in content if isinstance(content, list) else []:
                if isinstance(b, dict) and b.get("type") == "tool_use" and b.get("name") in EDIT_TOOLS:
                    edited = True
    return calls, first_prompt


def scan_session(files):
    """files = [main, *subagents]; all calls of one session."""
    calls, prompt = [], ""
    for i, f in enumerate(files):
        c, p = scan_file(f, sub=i > 0)
        if i == 0:
            prompt = p
        calls += c
    return calls, prompt


def summarize(calls):
    s = {"n": len(calls), "write": 0.0, "read": 0.0, "out": 0.0, "in": 0.0}
    for b in BUCKETS:
        s["w_" + b] = 0.0
        s["n_" + b] = 0
    for c in calls:
        s["write"] += c["w"]
        s["read"] += c["r"]
        s["out"] += c["o"]
        s["in"] += c["i"]
        s["w_" + c["b"]] += c["w"]
        s["n_" + c["b"]] += 1
    s["total"] = s["write"] + s["read"] + s["out"] + s["in"]
    return s


def add(dst, src):
    for k, v in src.items():
        dst[k] = dst.get(k, 0) + v


def line(name, s):
    w = s["write"] or 1e-9
    parts = " | ".join(f"{100 * s['w_' + b] / w:4.0f}% ${s['w_' + b]:.2f}" + (f" ({s['n_a']:.0f} сесс., ${s['w_a'] / (s['n_a'] or 1):.3f}/сесс.)" if b == "a" else "") for b in BUCKETS)
    return (f"| {name} | {s['n']} | ${s['total']:.2f} | ${s['write']:.2f} ({100 * s['write'] / (s['total'] or 1):.0f}%) | "
            f"{parts} |")


HEAD = ("| группа | вызовов | всего $ | запись в кэш $ (доля от всего) | a старт | s субагенты | d повторная | "
        "b до первой правки | c после правки |\n|---|---|---|---|---|---|---|---|---|")


def transcripts(d):
    out = {}
    for f in d.glob("*.jsonl") if d.is_dir() else []:
        out[f.stem] = [f] + sorted((d / f.stem / "subagents").glob("*.jsonl"))
    return out


def mode_ab(root):
    sys.path.insert(0, str(root / "_kit"))
    import bench_measure as bm  # the pilot's own attribution of transcripts to tasks
    res = {}
    for arm, fn in (("A", bm.arm_a), ("B", bm.arm_b)):
        groups, _ = fn()
        agg = {}
        for task, sess in groups.items():
            for files in sess:
                s = summarize(scan_session(files)[0])
                add(agg.setdefault(task, {}), s)
        res[arm] = agg
    print(HEAD)
    for arm in ("A", "B"):
        tot = {}
        for task in sorted(res[arm]):
            add(tot, res[arm][task])
            print(line(f"{arm} {task}", res[arm][task]))
        print(line(f"**{arm} всего**", tot))
        res[arm]["_tot"] = tot
    bench = json.load(open(LAB / "measurements" / "2026-10-28-ab-pilot" / "bench.json", encoding="utf-8"))
    print("\nСверка с bench.json (ab-pilot-tools/cost.py): запись в кэш $, моя сумма / cost.py")
    for arm in ("A", "B"):
        ref = 0.0
        for e in bench[arm].values():
            models = e.get("models") or {}
            n = sum(models.values()) or 1
            for m, k in models.items():
                p = price(m)
                if p:
                    ref += k / n * (e["w1h"] * W1H + e["w5"] * W5) * p[0] / 1e6
        mine = res[arm]["_tot"]["write"]
        print(f"- {arm}: {mine:.2f} / {ref:.2f}  (расхождение {100 * (mine - ref) / ref:+.1f}%)")


def role_of(session, prompt):
    if session == "Chat":
        return "Chat"
    return "разработчик/очередь"


def mode_roles(days, out_json):
    since = dt.datetime.now().astimezone() - dt.timedelta(days=days)
    con = sqlite3.connect(f"file:{DB.as_posix()}?mode=ro", uri=True)
    rows = con.execute("select project, session, cli_session_id, started_at from session_runs "
                       "where cli_session_id is not null and cli_session_id != ''").fetchall()
    known = {}
    for proj, sess, sid, started in rows:
        m = re.match(r"(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d)", started or "")
        if not m or dt.datetime.fromisoformat(m.group(1)).astimezone() < since:
            continue
        known[sid] = (proj, sess)
    index = {}
    for d in PROJECTS.iterdir():
        if d.is_dir():
            for sid, files in transcripts(d).items():
                index.setdefault(sid, files)
    per_role = collections.defaultdict(list)
    for sid, (proj, sess) in known.items():
        files = index.get(sid)
        if not files:
            continue
        calls, prompt = scan_session(files)
        if calls:
            per_role[role_of(sess, prompt)].append(summarize(calls))
    # background: manager-spawned one-shot transcripts (skill autopilot, journal) not in session_runs
    bg = []
    cut = since.timestamp()
    for sid, files in index.items():
        if sid in known or files[0].stat().st_mtime < cut:
            continue
        p = scan_file(files[0], False)[1]
        if re.match(r"\s*(Distill this recurring pattern|Review this skill draft)", p) or "journal" in p[:80].lower():
            calls, _ = scan_session(files)
            if calls:
                bg.append(summarize(calls))
    if bg:
        per_role["фоновые (distill/review/журнал)"] = bg
    print(HEAD.replace("группа", "роль (медианы по сессиям)"))
    out = {}
    for role, lst in sorted(per_role.items()):
        med = {}
        for k in ("n", "total", "write", "w_a", "w_s", "w_d", "w_b", "w_c"):
            med[k] = statistics.median(x[k] for x in lst)
        med["read"] = statistics.median(x["read"] for x in lst)
        med["out"] = statistics.median(x["out"] for x in lst)
        med["in"] = statistics.median(x["in"] for x in lst)
        tot = {}
        for x in lst:
            add(tot, x)
        # medians of per-session shares
        sh = {b: statistics.median((x["w_" + b] / x["write"]) if x["write"] else 0 for x in lst) for b in BUCKETS}
        print(line(f"{role} (n={len(lst)}) сумма", tot))
        print(f"| {role}: медиана сессии | {med['n']:.0f} | ${med['total']:.2f} | ${med['write']:.2f} | "
              + " | ".join(f"{100 * sh[b]:.0f}% (мед. доля)" for b in BUCKETS) + " |")
        out[role] = {"sessions": len(lst), "sum": tot, "median": med, "median_share": sh}
    if out_json:
        Path(out_json).write_text(json.dumps(out, ensure_ascii=False, indent=1), encoding="utf-8")


def main():
    a = sys.argv[1:]
    if a and a[0] == "ab":
        mode_ab(Path(a[1]) if len(a) > 1 else Path(r"C:\kostja\GoProjects\cm-bench"))
    elif a and a[0] == "roles":
        oj = None
        if "--json" in a:
            oj = a[a.index("--json") + 1]
            a = [x for x in a if x not in ("--json", oj)]
        mode_roles(int(a[1]) if len(a) > 1 else 30, oj)
    else:
        print(__doc__)


if __name__ == "__main__":
    main()
