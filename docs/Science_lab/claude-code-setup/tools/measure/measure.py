"""Measure the effect of the claude-code-setup changes: snapshot before, snapshot after, compare.

Run with the full path of a real python.exe. The procedure is in docs/Science_lab/MEASURE.md.

  measure.py snapshot <project path | transcripts dir> [--since YYYY-MM-DD] [--until YYYY-MM-DD]
                      [--label NAME] [--out DIR]
      Cost metrics of the project's Claude Code sessions started in [since, until) (until is
      exclusive). Runs cost_attribution.py and session_length_cost.py from this folder over the
      selected transcripts, adds per-session and per-role numbers, the garbled-output rate and
      claude-manager's tasks_done, and writes <out>/<label>/ (metrics.json, summary.md, cost.json,
      by_length.json, sessions.csv). Transcripts live in %USERPROFILE%\\.claude\\projects\\<project
      path with every non-alphanumeric character replaced by '-'>. Claude Code deletes them after
      about 30 days, so take the "before" snapshot before the change, not after it.

  measure.py compare <before dir> <after dir>
      Before/after table of the key metrics (markdown), also written to <after dir>/compare-<before>.md.

  measure.py speed [--runs N]
      Process start time and loop speed of the runtimes installed here. Free, under a minute.

  measure.py ab-script <file> "<question>" [--runs N] [--budget USD] [--model NAME] [--out FILE]
      The same question about a file answered by `claude -p` twice: with one Python script, and with
      Read/Grep/Glob only. Turns, seconds and dollars come from the CLI's own result. Spends real
      tokens; each run is capped by --budget.
"""
import argparse
import base64
import collections
import csv
import datetime as dt
import json
import os
import re
import shutil
import sqlite3
import statistics
import subprocess
import sys
import tempfile
import time
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))
from cost_attribution import READ, W1H, W5, price  # noqa: E402

PROJECTS = Path.home() / ".claude" / "projects"
MANAGER = Path.home() / ".claude-manager"
LONG = 100  # API calls; in the study such sessions were 21% of sessions and 55% of spend
WATCH = ["Bash: git pull", "Bash: grep", "Bash: sed", "Bash: cat", "Read: *.md", "Read: *.rs", "Read: *.go",
         "Bash: python"]
# UTF-8 Cyrillic shown as cp1251 ("РџСЂРёРІРµС‚"), runs of U+FFFD (cp866 bytes read as UTF-8), encode errors.
_CP1251_TAIL = ("Ё-Џё-џҐґ -¿–—‘-•…"
                "‰‹›€№™")
MOJIBAKE = re.compile(rf"(?:[РС][{_CP1251_TAIL}]){{3,}}|�{{2,}}|Unicode(?:En|De)codeError")
DATE = re.compile(r"^\d{4}-\d{2}-\d{2}$")


def win_path(arg):
    """Accept Git Bash paths (/c/x/y) as well as Windows ones."""
    m = re.match(r"^/([a-zA-Z])(/.*)?$", arg)
    if m and os.name == "nt":
        arg = f"{m.group(1).upper()}:{m.group(2) or '/'}"
    return Path(arg)


def transcripts_dir(arg):
    p = win_path(arg)
    if p.is_dir() and any(p.glob("*.jsonl")):
        return p, None
    proj = p.resolve()
    mangled = re.sub(r"[^A-Za-z0-9]", "-", str(proj))
    candidates = [PROJECTS / mangled]
    if PROJECTS.is_dir():
        candidates += [c for c in PROJECTS.iterdir() if c.name.lower() == mangled.lower()]
    for d in candidates:
        if d.is_dir():
            return d, proj
    sys.exit(f"нет транскриптов для {arg}: ожидалась папка {PROJECTS / mangled}")


def default_out():
    kit = HERE.parent.parent
    if (kit / "SETUP.md").exists():  # running from the kit folder inside docs/Science_lab
        return kit.parent / "measurements"
    return Path.home() / ".claude" / "measurements"


def local_day(ts):
    try:
        return dt.datetime.fromisoformat(ts.replace("Z", "+00:00")).astimezone().date().isoformat()
    except (ValueError, AttributeError):
        return None


def call_cost(u, pr):
    """Same formula as cost_attribution.py: USD of one API call and the context it sent."""
    pin, pout = pr
    cc = u.get("cache_creation") or {}
    w1h, w5 = cc.get("ephemeral_1h_input_tokens"), cc.get("ephemeral_5m_input_tokens")
    cw = u.get("cache_creation_input_tokens", 0) or 0
    if w1h is None and w5 is None:
        w1h, w5 = cw, 0
    cr = u.get("cache_read_input_tokens", 0) or 0
    it = u.get("input_tokens", 0) or 0
    ot = u.get("output_tokens", 0) or 0
    usd = (it * pin + ((w5 or 0) * W5 + (w1h or 0) * W1H) * pin + cr * READ * pin + ot * pout) / 1e6
    return usd, cr + cw + it


def command_kind(tool, cmd):
    if tool == "PowerShell":
        return "PowerShell (инструмент)"
    c = re.sub(r"^(cd\s+\S+\s*(&&|;)\s*)+", "", (cmd or "").strip())
    if re.search(r"\b(tasklist|taskkill|netstat|schtasks|wmic)\b", c):  # before python: `tasklist ... python.exe`
        return "tasklist/taskkill/netstat"
    if re.search(r"(^|[\s/\\\"'=])(python3?|py)(\.exe)?([\s\"']|$)", c):
        return "python"
    if re.search(r"\b(powershell|pwsh)(\.exe)?\b", c):
        return "powershell из Bash"
    return "прочие команды"


def result_text(content):
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return " ".join(b.get("text", "") or "" for b in content if isinstance(b, dict))
    return ""


def scan(f):
    s = {"file": f.name, "start": None, "title": None, "entry": None, "prompt": "", "calls": 0, "usd": 0.0,
         "ctx": 0, "runs": collections.Counter(), "garbled": collections.Counter()}
    seen, tools = set(), {}
    with f.open(encoding="utf-8", errors="replace") as fh:
        for line in fh:
            try:
                d = json.loads(line)
            except ValueError:
                continue
            if s["start"] is None and d.get("timestamp"):
                s["start"] = d["timestamp"]
            s["title"] = d.get("customTitle") or d.get("agentName") or s["title"]
            s["entry"] = s["entry"] or d.get("entrypoint")
            msg = d.get("message") or {}
            content = msg.get("content")
            if d.get("type") == "assistant":
                for b in content if isinstance(content, list) else []:
                    if isinstance(b, dict) and b.get("type") == "tool_use" and b.get("name") in ("Bash", "PowerShell"):
                        tools[b.get("id")] = (b["name"], (b.get("input") or {}).get("command", ""))
                mid = msg.get("id") or d.get("requestId")
                u, pr = msg.get("usage"), price(msg.get("model"))
                if not u or not pr or mid in seen:
                    continue
                seen.add(mid)
                usd, ctx = call_cost(u, pr)
                s["calls"] += 1
                s["usd"] += usd
                s["ctx"] += ctx
            elif d.get("type") == "user":
                if isinstance(content, str):
                    s["prompt"] = s["prompt"] or content
                    continue
                for b in content if isinstance(content, list) else []:
                    if not isinstance(b, dict):
                        continue
                    if b.get("type") == "text" and not s["prompt"]:
                        s["prompt"] = b.get("text", "") or ""
                    elif b.get("type") == "tool_result" and b.get("tool_use_id") in tools:
                        kind = command_kind(*tools.pop(b["tool_use_id"]))
                        s["runs"][kind] += 1
                        if MOJIBAKE.search(result_text(b.get("content"))):
                            s["garbled"][kind] += 1
    s["prompt"] = " ".join(s["prompt"].split())[:80]
    return s


def pct(part, whole):
    return round(100 * part / whole, 1) if whole else None


def manager_tasks(proj, since, until):
    """Runs and tasks_done of the project in claude-manager's history.db (None when unavailable)."""
    cfg, db = MANAGER / "config.toml", MANAGER / "history.db"
    if proj is None or not cfg.exists() or not db.exists():
        return None
    import tomllib
    try:
        projects = tomllib.loads(cfg.read_text(encoding="utf-8")).get("project", [])
    except (OSError, ValueError):
        return None
    name = next((p.get("name") for p in projects
                 if str(Path(p.get("path", "")).resolve()).lower() == str(proj).lower()), None)
    if not name:
        return None
    con = sqlite3.connect(f"file:{db.as_posix()}?mode=ro", uri=True)
    try:
        rows = con.execute("select session, substr(started_at, 1, 10), tasks_done, status "
                           "from session_runs where project = ?", (name,)).fetchall()
    finally:
        con.close()
    rows = [r for r in rows if (not since or r[1] >= since) and (not until or r[1] < until)]
    by_session = collections.defaultdict(lambda: {"runs": 0, "tasks_done": 0})
    for sess, _, done, _ in rows:
        by_session[sess]["runs"] += 1
        by_session[sess]["tasks_done"] += done or 0
    return {"project": name, "runs": len(rows), "tasks_done": sum(r[2] or 0 for r in rows),
            "statuses": dict(collections.Counter(r[3] for r in rows)), "by_session": dict(by_session)}


def build_metrics(a, label, tdir, proj, sessions, cost, by_len):
    calls = sorted(s["calls"] for s in sessions)
    n, total_calls = len(sessions), sum(calls)
    usd = sum(s["usd"] for s in sessions)
    long = [s for s in sessions if s["calls"] > LONG]
    cats = cost.get("tool_result_lifetime_usd_by_category", {})
    runs, bad = collections.Counter(), collections.Counter()
    for s in sessions:
        runs.update(s["runs"])
        bad.update(s["garbled"])
    garbled = {k: {"runs": runs[k], "garbled": bad[k], "pct": pct(bad[k], runs[k])} for k, _ in runs.most_common()}
    garbled["все команды"] = {"runs": sum(runs.values()), "garbled": sum(bad.values()),
                              "pct": pct(sum(bad.values()), sum(runs.values()))}
    roles = {}
    for s in sessions:
        r = roles.setdefault(s["title"] or "(без имени)",
                             {"sessions": 0, "calls": 0, "usd": 0.0, "long_usd": 0.0, "ctx": 0})
        r["sessions"] += 1
        r["calls"] += s["calls"]
        r["usd"] += s["usd"]
        r["ctx"] += s["ctx"]
        r["long_usd"] += s["usd"] if s["calls"] > LONG else 0
    roles = {k: {"sessions": r["sessions"], "calls": r["calls"], "usd": round(r["usd"], 2),
                 "long_spend_pct": pct(r["long_usd"], r["usd"]),
                 "avg_ctx_per_call": int(r["ctx"] / max(r["calls"], 1))}
             for k, r in sorted(roles.items(), key=lambda kv: -kv[1]["usd"])}
    tasks = manager_tasks(proj, a.since, a.until)
    usd_manager = sum(s["usd"] for s in sessions if (s["entry"] or "").startswith("sdk"))
    days = sorted(s["day"] for s in sessions)
    return {
        "label": label, "project": str(proj) if proj else None, "transcripts": str(tdir),
        "window": {"since": a.since, "until": a.until, "first": days[0], "last": days[-1]},
        "generated": dt.datetime.now().isoformat(timespec="seconds"),
        "sessions": n, "sessions_manager": sum(1 for s in sessions if (s["entry"] or "").startswith("sdk")),
        "api_calls": total_calls, "usd_total": round(usd, 2), "kit_total_usd": cost.get("total_usd"),
        "usd_per_session_median": round(statistics.median(s["usd"] for s in sessions), 2),
        "usd_per_session_mean": round(usd / n, 2), "usd_per_call": round(usd / total_calls, 4),
        "avg_ctx_per_call": int(sum(s["ctx"] for s in sessions) / total_calls),
        "calls_per_session": {"median": calls[n // 2], "p90": calls[min(n - 1, int(n * 0.9))], "max": calls[-1]},
        "long_sessions": len(long), "long_sessions_pct": pct(len(long), n),
        "long_spend_pct": pct(sum(s["usd"] for s in long), usd),
        "pct_by_component": cost.get("pct_by_component", {}),
        "watch": {c: cats.get(c) for c in WATCH},
        "top_categories": dict(list(cats.items())[:10]),
        "by_length": by_len, "garbled": garbled, "roles": roles, "manager": tasks,
        "usd_per_task": round(usd_manager / tasks["tasks_done"], 2) if tasks and tasks["tasks_done"] else None,
    }


def num(x, digits=0):
    if x is None:
        return "—"
    return f"{x:,.{digits}f}".replace(",", " ")


def summary_md(m, sessions):
    w = m["window"]
    lines = [
        f"# Срез `{m['label']}`", "",
        f"Проект: `{m['project'] or m['transcripts']}` · сессии с {w['first']} по {w['last']}"
        f" (окно: {w['since'] or 'начало'} … {w['until'] or 'сейчас'}, до — не включая) · снято {m['generated']}", "",
        "| Метрика | Значение |", "|---|---|",
        f"| Сессий (из них запущено менеджером) / вызовов модели | {m['sessions']} ({m['sessions_manager']})"
        f" / {num(m['api_calls'])} |",
        f"| Затраты, эквивалент цен API | ${num(m['usd_total'], 2)} |",
        f"| $ на сессию: медиана / среднее | ${num(m['usd_per_session_median'], 2)} / ${num(m['usd_per_session_mean'], 2)} |",
        f"| $ на вызов модели | ${m['usd_per_call']:.4f} |",
        "| Доли затрат: чтение кэша / запись в кэш / ответы | " + " / ".join(
            f"{m['pct_by_component'].get(k, 0)}%" for k in ("cache_read", "cache_write", "output")) + " |",
        f"| Средний контекст на вызов, токенов | {num(m['avg_ctx_per_call'])} |",
        f"| Вызовов на сессию: медиана / p90 / макс | {m['calls_per_session']['median']} / "
        f"{m['calls_per_session']['p90']} / {m['calls_per_session']['max']} |",
        f"| Сессии длиннее {LONG} вызовов | {m['long_sessions']} ({m['long_sessions_pct']}% сессий) → "
        f"**{m['long_spend_pct']}% затрат** |",
    ]
    t = m["manager"]
    if t:
        lines.append(f"| Задач выполнено по history.db менеджера ({t['runs']} запусков) | {t['tasks_done']}"
                     f" → ≈ ${num(m['usd_per_task'], 2)} на задачу |")
    lines += ["", "## По длине сессии", "", "| Вызовов | Сессий | Доля затрат | $ на сессию | Контекст на вызов |",
              "|---|---|---|---|---|"]
    lines += [f"| {r['calls']} | {r['sessions']} | {r['pct_of_spend']}% | ${r['usd_per_session']} | "
              f"{num(r['avg_context_tokens_per_call'])} |" for r in m["by_length"]]
    lines += ["", "## Самые дорогие результаты инструментов", "",
              "Цена результата с учётом того, сколько раз его перечитали (`cost_attribution.py`).", "",
              "| Категория | $ | Доля затрат | Результатов | Ср. токенов |", "|---|---|---|---|---|"]
    lines += [f"| {k} | {v['usd']} | {v['pct_of_total']}% | {v['results']} | {num(v['avg_tokens'])} |"
              for k, v in m["top_categories"].items()]
    lines += ["", "## Отслеживаемые категории", "", "| Категория | Результатов | Ср. токенов | Доля затрат |",
              "|---|---|---|---|"]
    lines += [f"| {k} | {v['results']} | {num(v['avg_tokens'])} | {v['pct_of_total']}% |" if v else
              f"| {k} | — | — | — |" for k, v in m["watch"].items()]
    lines += ["", "## Нечитаемый вывод команд", "", "Эвристика: кракозябры cp1251, серии U+FFFD, UnicodeEncodeError.", "",
              "| Команды | Запусков | Нечитаемо | Доля |", "|---|---|---|---|"]
    lines += [f"| {k} | {v['runs']} | {v['garbled']} | {v['pct']}% |" for k, v in m["garbled"].items()]
    lines += ["", "## По ролям (имя сессии в менеджере)", "",
              f"| Роль | Сессий | Вызовов | $ | Затраты в сессиях >{LONG} | Контекст на вызов |", "|---|---|---|---|---|---|"]
    lines += [f"| {k} | {r['sessions']} | {num(r['calls'])} | {num(r['usd'], 2)} | {r['long_spend_pct']}% | "
              f"{num(r['avg_ctx_per_call'])} |" for k, r in m["roles"].items()]
    lines += ["", "## 10 самых дорогих сессий", "", "| День | Роль | Вызовов | $ | Контекст на вызов | Начало промпта |",
              "|---|---|---|---|---|---|"]
    for s in sorted(sessions, key=lambda s: -s["usd"])[:10]:
        prompt = s["prompt"].replace("|", "/")
        lines.append(f"| {s['day']} | {s['title'] or '—'} | {s['calls']} | {num(s['usd'], 2)} | "
                     f"{num(s['ctx'] / max(s['calls'], 1))} | {prompt} |")
    return "\n".join(lines) + "\n"


def transcript_files(tdir):
    """Transcripts of the project plus its worktrees: a session started with
    `claude --worktree` (or in a pool slot under .claude/worktrees) is stored in
    a sibling folder `<project>--claude-worktrees-<name>`."""
    files = list(tdir.glob("*.jsonl"))
    prefix = tdir.name.lower() + "--claude-worktrees-"
    for d in tdir.parent.iterdir():
        if d.is_dir() and d.name.lower().startswith(prefix):
            files += d.glob("*.jsonl")
    return sorted(files, key=lambda f: f.name)


def snapshot(a):
    for d in (a.since, a.until):
        if d and not DATE.match(d):
            sys.exit(f"дата должна быть в виде ГГГГ-ММ-ДД: {d}")
    tdir, proj = transcripts_dir(a.project)
    label = a.label or f"{dt.date.today().isoformat()}-{re.sub(r'[^A-Za-z0-9]+', '-', (proj or tdir).name).lower()}"
    out = (win_path(a.out) if a.out else default_out()) / label
    sessions, files = [], []
    for f in transcript_files(tdir):
        s = scan(f)
        s["day"] = local_day(s["start"]) if s["start"] else None
        if not s["calls"] or s["day"] is None:
            continue
        if (a.since and s["day"] < a.since) or (a.until and s["day"] >= a.until):
            continue
        sessions.append(s)
        files.append(f)
    if not sessions:
        sys.exit("в окне нет сессий с вызовами модели")
    out.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix="measure-", ignore_cleanup_errors=True) as tmp:
        for f in files:
            try:
                os.link(f, Path(tmp) / f.name)
            except OSError:
                shutil.copy2(f, Path(tmp) / f.name)
        for script, name in (("cost_attribution.py", "cost.json"), ("session_length_cost.py", "by_length.json")):
            subprocess.run([sys.executable, "-I", str(HERE / script), tmp, str(out / name)],
                           check=True, stdout=subprocess.DEVNULL)
    cost = json.loads((out / "cost.json").read_text(encoding="utf-8"))
    by_len = json.loads((out / "by_length.json").read_text(encoding="utf-8"))
    m = build_metrics(a, label, tdir, proj, sessions, cost, by_len)
    (out / "metrics.json").write_text(json.dumps(m, ensure_ascii=False, indent=1), encoding="utf-8")
    (out / "summary.md").write_text(summary_md(m, sessions), encoding="utf-8")
    with (out / "sessions.csv").open("w", newline="", encoding="utf-8-sig") as fh:
        wr = csv.writer(fh)
        wr.writerow(["day", "role", "entrypoint", "calls", "usd", "avg_ctx", "garbled", "prompt", "file"])
        for s in sorted(sessions, key=lambda s: s["start"]):
            wr.writerow([s["day"], s["title"] or "", s["entry"] or "", s["calls"], round(s["usd"], 2),
                         int(s["ctx"] / s["calls"]), sum(s["garbled"].values()), s["prompt"], s["file"]])
    if m["kit_total_usd"] and abs(m["kit_total_usd"] - m["usd_total"]) > 0.01 * m["usd_total"]:
        print(f"ВНИМАНИЕ: сумма cost_attribution.py (${m['kit_total_usd']}) расходится с подсчётом по сессиям")
    print("\n".join(summary_md(m, sessions).splitlines()[:16]))
    print(f"\nПолный отчёт: {out / 'summary.md'}")


ROWS = [  # label, metric getter, delta kind ("rel" = relative %, "pp" = percentage points), what should move it
    ("Сессий", lambda m: m["sessions"], "rel", "передача задачи: ↑ (задача делится на сессии)"),
    ("Вызовов модели", lambda m: m["api_calls"], "rel", ""),
    ("Затраты, $", lambda m: m["usd_total"], "rel", "зависит от объёма работы — смотреть строки ниже"),
    ("$ на задачу ≈ (history.db)", lambda m: m.get("usd_per_task"), "rel", "главная цель: ↓"),
    ("$ на вызов модели", lambda m: m["usd_per_call"], "rel", "передача задачи: ↓"),
    ("$ на сессию, медиана", lambda m: m["usd_per_session_median"], "rel", "передача задачи: ↓"),
    ("Средний контекст на вызов, токенов", lambda m: m["avg_ctx_per_call"], "rel", "передача задачи: ↓"),
    ("Вызовов на сессию, p90", lambda m: m["calls_per_session"]["p90"], "rel", "передача задачи: ≤ ~100"),
    (f"Сессии >{LONG} вызовов, % сессий", lambda m: m["long_sessions_pct"], "pp", "передача задачи: ↓"),
    (f"Затраты в сессиях >{LONG} вызовов, %", lambda m: m["long_spend_pct"], "pp", "передача задачи: ↓"),
    ("Доля чтения кэша, %", lambda m: m["pct_by_component"].get("cache_read"), "pp", "короче сессии: ↓"),
    ("git pull, ср. токенов на результат", lambda m: (m["watch"].get("Bash: git pull") or {}).get("avg_tokens"),
     "rel", "`git pull -q`: ↓ до <100"),
    ("grep, ср. токенов", lambda m: (m["watch"].get("Bash: grep") or {}).get("avg_tokens"), "rel",
     "правила kit об ограничении вывода: ↓"),
    ("sed, ср. токенов", lambda m: (m["watch"].get("Bash: sed") or {}).get("avg_tokens"), "rel", "то же"),
    ("cat, ср. токенов", lambda m: (m["watch"].get("Bash: cat") or {}).get("avg_tokens"), "rel", "то же"),
    ("Read *.md, ср. токенов", lambda m: (m["watch"].get("Read: *.md") or {}).get("avg_tokens"), "rel",
     "чтение диапазоном: ↓"),
    ("Нечитаемый вывод python, %", lambda m: (m["garbled"].get("python") or {}).get("pct"), "pp",
     "UTF-8 в окружении сессий: ↓ до ~1%"),
    ("Нечитаемый вывод, все команды, %", lambda m: (m["garbled"].get("все команды") or {}).get("pct"), "pp",
     "UTF-8 и правила kit: ↓"),
]


def compare(a):
    ma, mb = (json.loads((win_path(p) / "metrics.json").read_text(encoding="utf-8")) for p in (a.before, a.after))
    lines = [f"# Сравнение `{ma['label']}` → `{mb['label']}`", "",
             f"- До: {ma['window']['first']} … {ma['window']['last']}, {ma['sessions']} сессий",
             f"- После: {mb['window']['first']} … {mb['window']['last']}, {mb['sessions']} сессий"]
    if mb["sessions"] < 20 or ma["sessions"] < 20:
        lines.append("- **Мало сессий (меньше 20 в одном из окон): вывод предварительный.**")
    if ma["window"]["last"] >= mb["window"]["first"]:
        lines.append("- **Окна пересекаются: «после» содержит сессии из периода «до».**")
    lines += ["", "| Метрика | До | После | Изменение | Что должно её сдвинуть |", "|---|---|---|---|---|"]
    for label, get, kind, expect in ROWS:
        try:
            x, y = get(ma), get(mb)
        except (KeyError, TypeError):
            x = y = None
        if x is None or y is None:
            delta = "—"
        elif kind == "pp":
            delta = f"{y - x:+.1f} п.п."
        else:
            delta = f"{100 * (y - x) / x:+.0f}%" if x else "—"
        fmt = (lambda v: num(v, 4) if isinstance(v, float) and v < 1 else num(v, 2 if isinstance(v, float) else 0))
        lines.append(f"| {label} | {fmt(x)} | {fmt(y)} | {delta} | {expect} |")
    text = "\n".join(lines) + "\n"
    (win_path(a.after) / f"compare-{ma['label']}.md").write_text(text, encoding="utf-8")
    print(text)


def timed(cmd, runs):
    ms = []
    for _ in range(runs):
        t = time.perf_counter()
        subprocess.run(cmd, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        ms.append((time.perf_counter() - t) * 1000)
    return statistics.median(ms)


GO_HELLO = 'package main\n\nimport "fmt"\n\nfunc main() { fmt.Println("hi") }\n'
GO_LOOP = ('package main\n\nimport (\n\t"fmt"\n\t"os"\n\t"strconv"\n\t"time"\n)\n\n'
           'func main() {\n\tn, _ := strconv.Atoi(os.Args[1])\n\tt := time.Now()\n\ts := 0\n'
           '\tfor i := 0; i < n; i++ {\n\t\ts += (i * i) % 7\n\t}\n'
           '\tfmt.Println(float64(time.Since(t).Microseconds())/1000, s)\n}\n')
RS_HELLO = 'fn main() { println!("hi"); }\n'
# n comes from argv so the compiler cannot fold the loop away; the sum is printed so it is not dead code
RS_LOOP = ('use std::time::Instant;\nfn main() {\n    let n: u64 = std::env::args().nth(1).unwrap().parse().unwrap();\n'
           '    let t = Instant::now();\n    let mut s: u64 = 0;\n    for i in 0..n { s += (i * i) % 7; }\n'
           '    println!("{} {}", t.elapsed().as_secs_f64() * 1000.0, s);\n}\n')


def once(cmd, cwd=None, env=None, timeout=900):
    t = time.perf_counter()
    r = subprocess.run(cmd, cwd=cwd, env=env, capture_output=True, text=True, timeout=timeout)
    return (time.perf_counter() - t) * 1000, r


def compiled(runs, tmp):
    """Compile-and-run costs of the compiled toolchains found here; returns (name, argv) of their loop
    binaries — speed() appends the iteration count."""
    go, rustc, cargo, node = shutil.which("go"), shutil.which("rustc"), shutil.which("cargo"), shutil.which("node")
    rows, loops = [], []
    exe = ".exe" if os.name == "nt" else ""
    if go:
        d = tmp / "go"
        d.mkdir()
        (d / "hello.go").write_text(GO_HELLO, encoding="utf-8")
        ms, _ = once([go, "run", "hello.go"], cwd=d)
        rows.append(("Go: `go run hello.go`, первый раз (тёплый кэш стандартной библиотеки)", ms))
        rows.append(("Go: `go run hello.go`, повторно", statistics.median(once([go, "run", "hello.go"], cwd=d)[0]
                                                                     for _ in range(3))))
        cold = dict(os.environ, GOCACHE=str(tmp / "gocache-cold"))
        rows.append(("Go: `go run hello.go` с пустым кэшем сборки (новая машина, CI)",
                     once([go, "run", "hello.go"], cwd=d, env=cold)[0]))
        ms, _ = once([go, "build", "-o", "hello" + exe, "hello.go"], cwd=d)
        rows.append(("Go: `go build`", ms))
        rows.append(("Go: запуск готового бинарника", timed([str(d / ("hello" + exe))], runs)))
        (d / "loop.go").write_text(GO_LOOP, encoding="utf-8")
        once([go, "build", "-o", "loop" + exe, "loop.go"], cwd=d)
        loops.append(("Go (скомпилирован)", [str(d / ("loop" + exe))]))
    if rustc:
        d = tmp / "rs"
        d.mkdir()
        (d / "hello.rs").write_text(RS_HELLO, encoding="utf-8")
        once([rustc, "hello.rs"], cwd=d)  # warm-up: the first rustc call of a session pays a one-off start
        rows.append(("Rust: `rustc hello.rs` (отладочная сборка)", once([rustc, "hello.rs"], cwd=d)[0]))
        rows.append(("Rust: `rustc -O hello.rs`", once([rustc, "-O", "hello.rs"], cwd=d)[0]))
        rows.append(("Rust: запуск готового бинарника", timed([str(d / ("hello" + exe))], runs)))
        (d / "loop.rs").write_text(RS_LOOP, encoding="utf-8")
        once([rustc, "-O", "loop.rs"], cwd=d)
        loops.append(("Rust (rustc -O)", [str(d / ("loop" + exe))]))
    if cargo:
        ms, r = once([cargo, "new", "--vcs", "none", "--quiet", "proj"], cwd=tmp)
        if r.returncode == 0:
            rows.append(("Rust: `cargo build` нового проекта без зависимостей",
                         once([cargo, "build", "--quiet"], cwd=tmp / "proj")[0]))
            rows.append(("Rust: `cargo run` без изменений", once([cargo, "run", "--quiet"], cwd=tmp / "proj")[0]))
    if node:
        (tmp / "hello.js").write_text('console.log("hi")\n', encoding="utf-8")
        (tmp / "hello.ts").write_text('const msg: string = "hi";\nconsole.log(msg)\n', encoding="utf-8")
        rows.append(("Node: `node hello.js`", timed([node, str(tmp / "hello.js")], runs)))
        rows.append(("Node: `node hello.ts` (TypeScript без компиляции, стирание типов)",
                     timed([node, str(tmp / "hello.ts")], runs)))
    if rows:
        print("\n## Компиляция и запуск: сколько ждать до первого результата\n\n| Что | мс |\n|---|---|")
        for name, ms in rows:
            print(f"| {name} | {num(ms)} |")
    return loops


def ps_cmd(exe, script):
    return [exe, "-NoProfile", "-NonInteractive", "-EncodedCommand",
            base64.b64encode(script.encode("utf-16-le")).decode()]


def find_tool(name):
    """On PATH, or in D:\\bench\\bin where the study kept its Lua/LuaJIT/QuickJS builds."""
    found = shutil.which(name)
    if found:
        return found
    bench = Path("D:/bench/bin") / f"{name}.exe"
    return str(bench) if bench.exists() else None


def speed(a):
    py = sys.executable
    alias = Path(os.environ.get("LOCALAPPDATA", "")) / "Microsoft" / "WindowsApps" / "python.exe"
    node, pwsh, ps = shutil.which("node"), shutil.which("pwsh"), shutil.which("powershell")
    lua, luajit, qjs = find_tool("lua"), find_tool("luajit"), find_tool("qjs")
    jobrun = HERE.parent / "jobrun.exe"
    numpy = subprocess.run([py, "-c", "import numpy"], capture_output=True).returncode == 0
    start = [("Python, полный путь", [py, "-c", "pass"])]
    if alias.exists():
        start.append(("Python через алиас WindowsApps", [str(alias), "-c", "pass"]))
    if numpy:
        start.append(("Python + import numpy", [py, "-c", "import numpy"]))
    if jobrun.exists():
        start.append(("Python под jobrun.exe (Job Object)", [str(jobrun), "256", "10000", py, "-c", "pass"]))
    if node:
        start.append(("Node", [node, "-e", "0"]))
    for name, exe in (("Lua 5.4", lua), ("LuaJIT", luajit)):
        if exe:
            start.append((name, [exe, "-e", ""]))
    if qjs:
        start.append(("QuickJS-ng", [qjs, "-e", "0"]))
    if pwsh:
        start.append(("PowerShell 7", ps_cmd(pwsh, "0")))
    if ps:
        start.append(("PowerShell 5.1", ps_cmd(ps, "0")))
    print(f"## Старт процесса, медиана из {a.runs} запусков\n\n| Что | мс |\n|---|---|")
    for name, cmd in start:
        print(f"| {name} | {timed(cmd, a.runs):.0f} |")

    n, n_ps, n_c = 3_000_000, 100_000, 300_000_000
    loops = [("Python", [py, "-c", f"import time\nt=time.perf_counter();s=0\nfor i in range({n}): s+=(i*i)%7\n"
                                     "print((time.perf_counter()-t)*1000, s)"], n)]
    if numpy:
        loops.append(("Python + NumPy", [py, "-c", f"import time,numpy as np\nt=time.perf_counter()\n"
                                                    f"a=np.arange({n},dtype=np.int64)\ns=int(((a*a)%7).sum())\n"
                                                    "print((time.perf_counter()-t)*1000, s)"], n))
    if node:
        loops.append(("Node (V8)", [node, "-e", f"const t=performance.now();let s=0;for(let i=0;i<{n};i++)"
                                                f"{{s+=(i*i)%7}};console.log(performance.now()-t, s)"], n))
    lua_loop = f"local t=os.clock() local s=0 for i=0,{n}-1 do s=s+(i*i)%7 end print((os.clock()-t)*1000, s)"
    for name, exe in (("Lua 5.4", lua), ("LuaJIT", luajit)):
        if exe:
            loops.append((name, [exe, "-e", lua_loop], n))
    if qjs:
        loops.append(("QuickJS-ng", [qjs, "-e", f"const t=Date.now();let s=0;for(let i=0;i<{n};i++)"
                                               f"{{s+=(i*i)%7}};console.log(Date.now()-t, s)"], n))
    ps_loop = (f"$t=[Diagnostics.Stopwatch]::StartNew();$s=0;for($i=0;$i -lt {n_ps};$i++){{$s+=($i*$i)%7}};"
               "\"$($t.Elapsed.TotalMilliseconds) $s\"")
    if pwsh:
        loops.append(("PowerShell 7", ps_cmd(pwsh, ps_loop), n_ps))
    if ps:
        loops.append(("PowerShell 5.1", ps_cmd(ps, ps_loop), n_ps))
    with tempfile.TemporaryDirectory(prefix="measure-speed-", ignore_cleanup_errors=True) as tmpdir:
        for name, cmd in compiled(a.runs, Path(tmpdir)):
            loops.append((name, cmd + [str(n_c)], n_c))
        loop_table(loops)


def loop_table(loops):
    print(f"\n## Цикл `s += (i*i) % 7`, только время счёта (без старта процесса)\n\n"
          "| Что | Итераций | мс | нс на итерацию | Относительно Python |\n|---|---|---|---|---|")
    base = None
    for name, cmd, iters in loops:
        best = None
        for _ in range(3):
            p = subprocess.run(cmd, capture_output=True, text=True)
            try:
                ms = float(p.stdout.split()[0].replace(",", "."))
            except (IndexError, ValueError):
                ms = None
                break
            best = ms if best is None else min(best, ms)
        if best is None:
            print(f"| {name} | {iters:,} | ошибка | | |".replace(",", " "))
            continue
        ns = best * 1e6 / iters
        base = base or ns
        ratio = base / ns
        rel = f"в {ratio:.0f} раз быстрее" if ratio >= 1.5 else (f"в {1 / ratio:.0f} раз медленнее" if ratio <= 0.67
                                                                else "примерно так же")
        print(f"| {name} | {iters:,} | {best:.1f} | {ns:.1f} | {rel if name != 'Python' else '—'} |".replace(",", " "))


def ab_script(a):
    claude = shutil.which("claude") or "claude"
    src = win_path(a.file).resolve()
    if not src.is_file():
        sys.exit(f"нет файла {src}")
    variants = {
        "один скрипт": (
            f"{a.question}\n\nДанные — файл {src.name} в текущей папке. Реши задачу одним Python-скриптом: "
            "напиши его, запусти и выведи только итог.",
            # python by name, by quoted full path ("...python.exe" x.py), with the UTF-8 prefix, or under jobrun
            ["Read", "Write", "Edit", "Glob", "Bash(python *)", "Bash(py *)", "Bash(*python.exe* *)",
             "Bash(PYTHONIOENCODING=utf-8 *)", "Bash(*jobrun* *)"]),
        "только Read/Grep": (
            f"{a.question}\n\nДанные — файл {src.name} в текущей папке. Не пиши и не запускай скрипты: "
            "используй только инструменты Read, Grep и Glob.",
            ["Read", "Grep", "Glob"]),
    }
    env = {k: v for k, v in os.environ.items() if k not in ("CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT")}
    rows = []
    for run in range(a.runs):
        for name, (prompt, tools) in variants.items():
            with tempfile.TemporaryDirectory(prefix="ab-script-", ignore_cleanup_errors=True) as tmp:
                shutil.copy2(src, Path(tmp) / src.name)
                cmd = [claude, "-p", prompt, "--output-format", "json", "--model", a.model,
                       "--max-budget-usd", str(a.budget), "--allowedTools", *tools]
                t = time.perf_counter()
                p = subprocess.run(cmd, cwd=tmp, env=env, capture_output=True, text=True, encoding="utf-8",
                                   errors="replace", timeout=1800)
                wall = time.perf_counter() - t
            res = {}
            for line in reversed(p.stdout.strip().splitlines()):
                try:
                    res = json.loads(line)
                    break
                except ValueError:
                    continue
            rows.append({"variant": name, "run": run + 1, "turns": res.get("num_turns"),
                         "seconds": round((res.get("duration_ms") or wall * 1000) / 1000, 1),
                         "usd": res.get("total_cost_usd"), "error": res.get("is_error", p.returncode != 0),
                         "denied": len(res.get("permission_denials") or []),
                         "answer": res.get("result") or p.stderr or ""})
            r = rows[-1]
            print(f"{name} #{run + 1}: ходов {r['turns']}, {r['seconds']} с, ${r['usd']}, "
                  f"ошибка {r['error']}, отказов в доступе {r['denied']}", flush=True)
    print("\n| Вариант | Ходов (медиана) | Секунд | $ |\n|---|---|---|---|")
    for name in variants:
        rs = [r for r in rows if r["variant"] == name and r["usd"] is not None]
        if rs:
            print(f"| {name} | {statistics.median(r['turns'] or 0 for r in rs):.0f} | "
                  f"{statistics.median(r['seconds'] for r in rs):.0f} | {statistics.median(r['usd'] for r in rs):.3f} |")
    for r in rows:  # the full answers go to --out
        print(f"\n--- {r['variant']} #{r['run']}:\n{r['answer'][:1500]}")
    if a.out:
        win_path(a.out).write_text(json.dumps(rows, ensure_ascii=False, indent=1), encoding="utf-8")


def main():
    ap = argparse.ArgumentParser(description="Before/after measurements for claude-code-setup (see MEASURE.md).")
    sub = ap.add_subparsers(dest="cmd", required=True)
    s = sub.add_parser("snapshot")
    s.add_argument("project")
    s.add_argument("--since")
    s.add_argument("--until")
    s.add_argument("--label")
    s.add_argument("--out")
    c = sub.add_parser("compare")
    c.add_argument("before")
    c.add_argument("after")
    sp = sub.add_parser("speed")
    sp.add_argument("--runs", type=int, default=20)
    ab = sub.add_parser("ab-script")
    ab.add_argument("file")
    ab.add_argument("question")
    ab.add_argument("--runs", type=int, default=1)
    ab.add_argument("--budget", type=float, default=1.0)
    ab.add_argument("--model", default="sonnet")
    ab.add_argument("--out")
    a = ap.parse_args()
    {"snapshot": snapshot, "compare": compare, "speed": speed, "ab-script": ab_script}[a.cmd](a)


if __name__ == "__main__":
    main()
