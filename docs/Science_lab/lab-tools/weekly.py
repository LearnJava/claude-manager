"""LAB-03: weekly passive report on real work, from data the manager already recorded (read-only, free).

Usage (python -I):
  weekly.py [--since YYYY-MM-DD] [--weeks N] [--project NAME ...] [--out DIR]

  --since    start of the first reported week (default: 28 days before now; weeks are 7-day windows from it)
  --weeks    number of weeks to report (default: until now, at least 1)
  --project  substring of session_runs.project (case-insensitive), repeatable; default: every project
  --out      output folder (default: measurements/<today>-weekly next to this tool's Science_lab folder)

Sources (every metric in RESULTS.md names its table):
  session_runs       cost, minutes (duration_ms = model-active time), turns, tokens, one row per CLI run
  action_signatures  task_ptr links a run to a task (the task is the most frequent task_ptr of the run);
                     repeat reads, empty searches, errors
  gate_runs          gate count and green share (only rows the manager wrote)
  app.log            gate duration = session.process_exit -> session.run.task_done of the same session
                     (as ab-pilot-tools/analyze_speed.py)
  skills             background spend of the experience layer (cost_usd by created_at)
A week is compared with the previous one; a change above 25% is marked "!" (pilot noise was 20-30%).
Reads ~/.claude-manager/history.db and ~/.claude-manager/logs/app.log.
"""
import argparse
import collections
import datetime as dt
import re
import sqlite3
import statistics
import sys
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
LAB = Path(__file__).resolve().parents[1]
HOME = Path.home() / ".claude-manager"
DB = HOME / "history.db"
APP_LOG = HOME / "logs" / "app.log"
NOISE = 0.25
EMPTY_MAX_CHARS = 40  # a Grep/Glob result this short is "No matches found" or similar
SEARCH_TOOLS = {"Grep", "Glob", "search_files"}
READ_TOOLS = {"Read", "read_file"}
TS_RE = re.compile(r"(\d{4}-\d\d-\d\d) (\d\d:\d\d:\d\d)(?:\.\d+)? ([+-])(\d\d)(\d\d)")


def parse_ts(s):
    """'2026-10-28 20:57:31.5 +0300 MSK m=+12' or '... +0000 UTC' -> aware datetime (UTC), None if unparsable."""
    m = TS_RE.match(s or "")
    if not m:
        return None
    off = dt.timedelta(hours=int(m.group(4)), minutes=int(m.group(5)))
    if m.group(3) == "-":
        off = -off
    t = dt.datetime.fromisoformat(f"{m.group(1)}T{m.group(2)}")
    return (t - off).replace(tzinfo=dt.timezone.utc)


def pct(xs, p):
    xs = sorted(xs)
    if not xs:
        return None
    k = (len(xs) - 1) * p
    lo = int(k)
    hi = min(lo + 1, len(xs) - 1)
    return xs[lo] + (xs[hi] - xs[lo]) * (k - lo)


def med(xs):
    return statistics.median(xs) if xs else None


def fmt(v, kind):
    if v is None:
        return "-"
    if kind == "usd":
        return f"${v:.2f}"
    if kind == "share":
        return f"{v * 100:.0f}%"
    if kind == "int":
        return f"{v:.0f}"
    return f"{v:.1f}"


def cell(cur, prev, kind):
    s = fmt(cur, kind)
    if cur is None or prev is None or prev == 0:
        return s
    d = (cur - prev) / abs(prev)
    arrow = "↑" if d > 0.005 else "↓" if d < -0.005 else "→"
    mark = " !" if abs(d) > NOISE else ""
    return f"{s} ({arrow}{abs(d) * 100:.0f}%{mark})"


def load_gate_durations(log_path):
    """[(project, end_time, seconds)] from app.log: process_exit -> task_done of one session id."""
    out, exit_at = [], {}
    if not log_path.exists():
        return out
    pat = re.compile(r"time=(\S+) level=\S+ msg=(session\.process_exit|session\.run\.task_done|session\.init|session\.spawned) id=\"([^\"]+)\"")
    with log_path.open(encoding="utf-8", errors="replace") as fh:
        for line in fh:
            if "msg=session." not in line:
                continue
            m = pat.search(line)
            if not m:
                continue
            try:
                t = dt.datetime.fromisoformat(m.group(1)).astimezone(dt.timezone.utc)
            except ValueError:
                continue
            kind, sid = m.group(2), m.group(3)
            if kind == "session.process_exit":
                exit_at[sid] = t
            elif kind == "session.run.task_done":
                t0 = exit_at.pop(sid, None)
                if t0 is not None and t >= t0:
                    out.append((sid.split("/")[0], t, (t - t0).total_seconds()))
            else:
                exit_at.pop(sid, None)
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--since")
    ap.add_argument("--weeks", type=int)
    ap.add_argument("--project", action="append")
    ap.add_argument("--out")
    a = ap.parse_args()

    now = dt.datetime.now(dt.timezone.utc)
    if a.since:
        start = dt.datetime.fromisoformat(a.since).replace(tzinfo=dt.timezone.utc)
    else:
        start = now - dt.timedelta(days=28)
    nweeks = a.weeks or max(1, int((now - start).total_seconds() // (7 * 86400)) + (1 if (now - start).total_seconds() % (7 * 86400) else 0))
    wk0 = start - dt.timedelta(days=7)  # one extra week before, only for the comparison arrow
    edges = [wk0 + dt.timedelta(days=7 * i) for i in range(nweeks + 2)]

    def widx(t):
        if t is None or t < edges[0] or t >= edges[-1]:
            return None
        return int((t - edges[0]).total_seconds() // (7 * 86400))

    out_dir = Path(a.out) if a.out else LAB / "measurements" / f"{now.date()}-weekly"
    out_dir.mkdir(parents=True, exist_ok=True)

    if not DB.exists():
        sys.exit(f"no {DB}")
    db = sqlite3.connect(f"file:{DB.as_posix()}?mode=ro", uri=True)
    wanted = [p.lower() for p in a.project] if a.project else None

    def match(p):
        return wanted is None or any(w in (p or "").lower() for w in wanted)

    projects = sorted({r[0] for r in db.execute("select distinct project from session_runs") if match(r[0])})
    gate_secs = load_gate_durations(APP_LOG)

    header = "| метрика | источник | " + " | ".join(f"нед. с {edges[i + 1].date():%m-%d}" for i in range(nweeks)) + " |"
    sep = "|---|---|" + "---|" * nweeks
    md = [f"# Недельный отчёт по реальной работе ({now.date()})", "",
          f"Окно: {edges[1].date()} .. {edges[-1].date()}, {nweeks} нед. по 7 дней, время UTC. Проекты: "
          f"{', '.join(projects) or 'нет'}. Стрелка — к предыдущей неделе; `!` — изменение больше {NOISE * 100:.0f}% "
          f"(шум пилота 20–30%). Малое n недель даёт шум: смотри колонку n.", ""]
    stdout = [f"weekly {edges[1].date()}..{edges[-1].date()} ({nweeks} wk), projects: {', '.join(projects) or '-'}"]

    for proj in projects:
        runs = []
        for r in db.execute(
                "select id, started_at, total_cost_usd, duration_ms, num_turns, input_tokens, cache_read_tokens, "
                "cache_creation_tokens, status from session_runs where project=? and finished_at is not null", (proj,)):
            t = parse_ts(r[1])
            if widx(t) is not None:
                runs.append(dict(id=r[0], w=widx(t), cost=r[2] or 0.0, ms=r[3] or 0, turns=r[4] or 0, inp=r[5] or 0,
                                 cr=r[6] or 0, cc=r[7] or 0, status=r[8]))
        if not runs:
            continue
        ids = {r["id"] for r in runs}
        # task of a run = its most frequent task_ptr
        cnt = collections.defaultdict(collections.Counter)
        for rid, ptr in db.execute("select run_id, task_ptr from action_signatures where project=? and task_ptr is not null "
                                   "and task_ptr<>''", (proj,)):
            if rid in ids:
                cnt[rid][ptr] += 1
        for r in runs:
            r["task"] = cnt[r["id"]].most_common(1)[0][0] if cnt[r["id"]] else None
        tasks = collections.defaultdict(list)
        for r in runs:
            if r["task"]:
                tasks[r["task"]].append(r)
        per_w = collections.defaultdict(list)  # week -> list of per-task dicts
        for ptr, rs in tasks.items():
            tot = sum(x["inp"] + x["cr"] + x["cc"] for x in rs)
            per_w[min(x["w"] for x in rs)].append(dict(
                cost=sum(x["cost"] for x in rs), min=sum(x["ms"] for x in rs) / 60000, turns=sum(x["turns"] for x in rs),
                runs=len(rs), rd=sum(x["cr"] for x in rs) / tot if tot else None,
                wr=sum(x["cc"] for x in rs) / tot if tot else None))
        bg = collections.defaultdict(list)
        for r in runs:
            if not r["task"]:
                bg[r["w"]].append(r)

        # waste from action_signatures
        waste = collections.defaultdict(lambda: dict(calls=0, repeat=0, empty=0, err=0))
        seen = set()
        for rid, tool, arg, err, rc, ts in db.execute(
                "select run_id, tool, arg, is_error, result_chars, ts from action_signatures where project=? order by id", (proj,)):
            w = widx(parse_ts(ts))
            if w is None:
                continue
            x = waste[w]
            x["calls"] += 1
            if err:
                x["err"] += 1
            if tool in SEARCH_TOOLS and (rc or 0) <= EMPTY_MAX_CHARS and not err:
                x["empty"] += 1
            if tool in READ_TOOLS:
                if (rid, arg) in seen:
                    x["repeat"] += 1
                seen.add((rid, arg))

        gates = collections.defaultdict(lambda: [0, 0])
        for passed, ts in db.execute("select passed, ts from gate_runs where project=?", (proj,)):
            w = widx(parse_ts(ts))
            if w is not None:
                gates[w][0] += 1
                gates[w][1] += 1 if passed else 0
        gdur = collections.defaultdict(list)
        for p, t, s in gate_secs:
            w = widx(t)
            if p == proj and w is not None:
                gdur[w].append(s / 60)
        skill_cost = collections.defaultdict(float)
        for cu, ts in db.execute("select cost_usd, created_at from skills where project=?", (proj,)):
            w = widx(parse_ts(ts))
            if w is not None:
                skill_cost[w] += cu or 0.0

        rows = []  # (label, source, kind, fn(week)->value)
        def tk(key, f):
            return lambda w: f([t[key] for t in per_w.get(w, []) if t[key] is not None])
        rows += [
            ("задач (первый запуск в неделе)", "session_runs+action_signatures.task_ptr", "int", lambda w: len(per_w.get(w, [])) or None),
            ("$ на задачу, медиана", "session_runs.total_cost_usd", "usd", tk("cost", med)),
            ("$ на задачу, p90", "session_runs.total_cost_usd", "usd", tk("cost", lambda x: pct(x, 0.9))),
            ("минуты на задачу, медиана", "session_runs.duration_ms", "num", tk("min", med)),
            ("минуты на задачу, p90", "session_runs.duration_ms", "num", tk("min", lambda x: pct(x, 0.9))),
            ("ходы на задачу, медиана", "session_runs.num_turns", "int", tk("turns", med)),
            ("ходы на задачу, p90", "session_runs.num_turns", "int", tk("turns", lambda x: pct(x, 0.9))),
            ("сессий на задачу, медиана", "session_runs (по task_ptr)", "num", tk("runs", med)),
            ("сессий на задачу, p90", "session_runs (по task_ptr)", "num", tk("runs", lambda x: pct(x, 0.9))),
            ("доля чтения кэша, медиана", "session_runs.cache_read_tokens", "share", tk("rd", med)),
            ("доля записи кэша, медиана", "session_runs.cache_creation_tokens", "share", tk("wr", med)),
            ("ворота: запусков", "gate_runs", "int", lambda w: gates[w][0] if w in gates else None),
            ("ворота: доля зелёных", "gate_runs.passed", "share", lambda w: gates[w][1] / gates[w][0] if gates.get(w, [0])[0] else None),
            ("ворота: минуты, медиана", "app.log process_exit→task_done", "num", lambda w: med(gdur.get(w, []))),
            ("ворота: n измерений времени", "app.log", "int", lambda w: len(gdur.get(w, [])) or None),
            ("фон: запусков без задачи", "session_runs без task_ptr", "int", lambda w: len(bg.get(w, [])) or None),
            ("фон: $ запусков без задачи", "session_runs.total_cost_usd", "usd", lambda w: sum(x["cost"] for x in bg.get(w, [])) if w in bg else None),
            ("фон: $ навыков", "skills.cost_usd", "usd", lambda w: skill_cost.get(w) or None),
            ("вызовов инструментов", "action_signatures", "int", lambda w: waste[w]["calls"] if w in waste else None),
            ("повторные чтения / 1000 вызовов", "action_signatures (Read, тот же arg в запуске)", "num",
             lambda w: waste[w]["repeat"] * 1000 / waste[w]["calls"] if waste.get(w, {}).get("calls") else None),
            ("пустые поиски / 1000 вызовов", f"action_signatures (Grep/Glob, result_chars≤{EMPTY_MAX_CHARS})", "num",
             lambda w: waste[w]["empty"] * 1000 / waste[w]["calls"] if waste.get(w, {}).get("calls") else None),
            ("ошибки / 1000 вызовов", "action_signatures.is_error", "num",
             lambda w: waste[w]["err"] * 1000 / waste[w]["calls"] if waste.get(w, {}).get("calls") else None),
        ]
        md += [f"## {proj}", "", header, sep]
        stdout.append(f"[{proj}]")
        for label, src, kind, fn in rows:
            vals = [fn(edges_i) for edges_i in range(nweeks + 1)]  # index 0 = extra week before the window
            cells = [cell(vals[i + 1], vals[i], kind) for i in range(nweeks)]
            md.append(f"| {label} | {src} | " + " | ".join(cells) + " |")
            if label in ("задач (первый запуск в неделе)", "$ на задачу, медиана", "минуты на задачу, медиана",
                         "ходы на задачу, медиана", "сессий на задачу, медиана", "ворота: доля зелёных",
                         "фон: $ навыков", "повторные чтения / 1000 вызовов", "ошибки / 1000 вызовов"):
                stdout.append(f"  {label}: " + " | ".join(cells))
        md.append("")

    md += ["## Как читать", "",
           "- Задача = самый частый `task_ptr` запуска; неделя задачи = неделя её первого запуска. Запуски без `task_ptr` — фон.",
           "- Минуты — `duration_ms` (время работы модели в запуске), не часы на часах; ожидание пользователя и ворот сюда не входит.",
           "- Ворота: `gate_runs` пишет только часть запусков (в базе на 2026-10-29 — 11 строк проекта bench-cm), поэтому "
           "доля зелёных по реальным проектам часто пуста; время ворот берётся из `app.log` и покрывает окно, "
           "которое хранит лог.",
           "- «Повторное чтение» — `Read` с тем же `arg` в том же запуске; «пустой поиск» — Grep/Glob с ответом не длиннее "
           f"{EMPTY_MAX_CHARS} символов без ошибки. Это оценка по подписям, а не точный подсчёт.",
           "- Первая колонка — только сравнение с предыдущей неделей; данные недели до окна в таблицу не выводятся.", ""]
    path = out_dir / "RESULTS.md"
    path.write_text("\n".join(md), encoding="utf-8")
    stdout.append(f"-> {path}")
    print("\n".join(stdout[:30]))


if __name__ == "__main__":
    main()
