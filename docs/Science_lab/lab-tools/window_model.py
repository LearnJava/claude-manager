"""LAB-02 (H8, step 1): cost model "one long Opus 5.5 session, 1M window" against "Sonnet 5.5 with context handoff at ~150K".

Two measurements on already recorded data, then a closed-form cost model. No paid runs.

  1. Handoffs: runs whose transcript starts with "--- Session handoff (context restart) ---" (history.db session_runs
     + ~/.claude/projects transcripts). Per handoff: size of the handoff summary (first-call context of the new run
     minus first-call context of the previous run of the same session), re-opened files (Read/read_file/cat/sed/head/tail
     of a path already read by the previous run, action_signatures), and tool calls before the first write.
  2. Growth: main transcripts of the last N days without handoff and with >= 30 calls: context at call k, tokens added
     per call (cache write + uncached input), output tokens per call, share of 1h cache writes. Parameters per model.
  3. Model: cost of M productive calls. Opus: no cap. Sonnet: when the context would pass H, one summary call, then a new
     session of ctx0 + S tokens plus E re-open calls adding R tokens. Break-even ratio of call counts Opus/Sonnet.

Prices: PRICES, W5, W1H, READ from claude-code-setup/tools/measure/cost_attribution.py (read = READ * input price).

Usage (python -I):  window_model.py [--days 30] [--out DIR] [--handoff-tokens 150000]
Reads ~/.claude/projects and ~/.claude-manager/history.db (read-only). Prints only totals; writes params.json and tables.md.
"""
import collections
import json
import re
import sqlite3
import statistics
import sys
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
LAB = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(Path(__file__).resolve().parent))
from cache_write_split import DB, PROJECTS, scan_file, transcripts  # noqa: E402  (LAB-01 transcript parsing)
from cache_write_split import READ, W1H, W5, price  # noqa: E402

HANDOFF_RE = re.compile(r"\s*--- Session handoff \(context restart\)")
WRITE_TOOLS = {"Edit", "Write", "MultiEdit", "NotebookEdit", "patch", "write_file"}
READ_TOOLS = {"Read", "read_file"}
CAT_RE = re.compile(r"^\s*(cat|head|tail|sed|less|bat)\b")
CHARS_PER_TOKEN = 4.0  # assumption (not verified): converts tool result chars to tokens
MODEL_FAMILY = (("opus", "opus"), ("sonnet", "sonnet"))


def family(model):
    m = (model or "").lower()
    for k, v in MODEL_FAMILY:
        if k in m:
            return v
    return None


def norm_path(p):
    return p.strip().strip("'\"").replace("\\", "/").lower()


def read_path(tool, arg):
    """Path read by an action_signatures row, or None."""
    if tool in READ_TOOLS:
        return norm_path(arg) if arg else None
    if tool in ("Bash", "terminal") and arg and CAT_RE.match(arg):
        cmd = re.sub(r"^(cd\s+\S+\s*(&&|;)\s*)+", "", arg.strip()).split("|")[0]
        toks = [t for t in cmd.split() if not t.startswith("-") and ("/" in t or "." in t) and not re.fullmatch(r"[\d,]+p?", t)]
        return norm_path(toks[-1]) if toks else None
    return None


def connect():
    return sqlite3.connect(f"file:{DB.as_posix()}?mode=ro", uri=True)


def index_transcripts():
    idx = {}
    for d in PROJECTS.iterdir():
        if d.is_dir():
            idx.update(transcripts(d))
    return idx


def first_ctx(calls):
    return calls[0]["ctx"] if calls else None


def transcript_actions(path):
    """(first user message chars, [(is_write, read_path or None, result_chars)]) from a main transcript.
    Full tool inputs live in the transcript; action_signatures.arg is truncated to ~80 chars."""
    uses, results, order, first_chars = {}, {}, [], None
    with path.open(encoding="utf-8", errors="replace") as fh:
        for line in fh:
            try:
                d = json.loads(line)
            except ValueError:
                continue
            content = (d.get("message") or {}).get("content")
            if d.get("type") == "user" and first_chars is None:
                first_chars = len(content) if isinstance(content, str) else sum(
                    len(b.get("text", "")) for b in content or [] if isinstance(b, dict) and b.get("type") == "text")
            for b in content if isinstance(content, list) else []:
                if not isinstance(b, dict):
                    continue
                if b.get("type") == "tool_use" and b.get("id") not in uses:
                    inp = b.get("input") or {}
                    arg = inp.get("file_path") or inp.get("command") or ""
                    uses[b["id"]] = (b.get("name"), arg)
                    order.append(b["id"])
                elif b.get("type") == "tool_result":
                    c = b.get("content")
                    results[b.get("tool_use_id")] = len(c) if isinstance(c, str) else sum(
                        len(x.get("text", "")) for x in c or [] if isinstance(x, dict))
    acts = []
    for tid in order:
        name, arg = uses[tid]
        acts.append((name in WRITE_TOOLS, read_path(name, arg), results.get(tid, 0)))
    return first_chars or 0, acts


def measure_handoffs(con, idx):
    runs = con.execute("select id, project, session, cli_session_id, started_at from session_runs "
                       "where cli_session_id is not null and cli_session_id != '' order by project, session, started_at").fetchall()
    by_sess = collections.defaultdict(list)
    for r in runs:
        by_sess[(r[1], r[2])].append(r)
    out, skipped = [], 0
    for key, lst in by_sess.items():
        for i, (rid, proj, sess, sid, started) in enumerate(lst):
            if i == 0 or sid not in idx:
                continue
            calls, prompt = scan_file(idx[sid][0], False)
            if not HANDOFF_RE.match(prompt) or not calls:
                continue
            psid = lst[i - 1][3]
            if psid not in idx:
                skipped += 1
                continue
            pcalls, _ = scan_file(idx[psid][0], False)
            if not pcalls:
                skipped += 1
                continue
            chars, acts = transcript_actions(idx[sid][0])
            _, pacts = transcript_actions(idx[psid][0])
            before = {p for _, p, _ in pacts if p}
            reopen, first_write = [], None
            for n, (is_w, p, rc) in enumerate(acts):
                if is_w and first_write is None:
                    first_write = n
                if p and p in before and first_write is None:
                    reopen.append(rc)
            out.append({"run": rid, "session": f"{proj}/{sess}", "model": calls[0]["mdl"],
                        "summary_chars": chars, "summary_tokens": chars / CHARS_PER_TOKEN,
                        "ctx0_new": first_ctx(calls), "ctx0_prev": first_ctx(pcalls),
                        "prev_calls": len(pcalls), "prev_last_ctx": pcalls[-1]["ctx"],
                        "reopen_reads": len(reopen), "reopen_chars": sum(reopen),
                        "tool_calls_before_first_write": first_write, "tool_calls_total": len(acts)})
    return out, skipped


def measure_growth(con, idx, days):
    import datetime as dt
    cut = (dt.datetime.now() - dt.timedelta(days=days)).timestamp()
    sids = {r[0] for r in con.execute("select cli_session_id from session_runs where cli_session_id != ''")}
    per = collections.defaultdict(list)  # family -> list of call lists
    for sid, files in idx.items():
        if sid not in sids or files[0].stat().st_mtime < cut:
            continue
        calls, prompt = scan_file(files[0], False)
        if len(calls) < 30 or HANDOFF_RE.match(prompt):
            continue
        fam = collections.Counter(family(c["mdl"]) for c in calls).most_common(1)[0][0]
        if fam:
            per[fam].append(calls)
    res = {}
    for fam, sessions in per.items():
        allc = [c for s in sessions for c in s]
        ks = [1, 10, 25, 50, 100, 150, 200, 300, 500]
        ctx_at = {}
        for k in ks:
            v = [s[k - 1]["ctx"] for s in sessions if len(s) >= k]
            if len(v) >= 3:
                ctx_at[k] = {"n": len(v), "median": statistics.median(v)}
        new = [c["w5"] + c["w1h"] for c in allc[1:]]
        w_all = sum(c["w5"] + c["w1h"] for c in allc)
        res[fam] = {
            "sessions": len(sessions), "calls": len(allc),
            "ctx0_median": statistics.median(s[0]["ctx"] for s in sessions),
            "new_per_call_median": statistics.median(new), "new_per_call_mean": statistics.fmean(new),
            "out_per_call_median": statistics.median(c["ot"] for c in allc),
            "out_per_call_mean": statistics.fmean(c["ot"] for c in allc),
            "w1h_share": sum(c["w1h"] for c in allc) / (w_all or 1),
            "ctx_at_call": ctx_at,
            "calls_median": statistics.median(len(s) for s in sessions),
        }
    return res


def mix_multiplier(w1h_share):
    return W1H * w1h_share + W5 * (1 - w1h_share)


def session_cost(m_calls, p, pr, handoff=None):
    """Cost in $ of m_calls productive calls. p: parameters, pr: (in, out, read_per_M).
    handoff: None (no cap) or dict(H, S, R, E)."""
    pin, pout, pread = pr
    wm = mix_multiplier(p["w1h_share"])
    new, out = p["new"], p["out"]
    ctx = p["ctx0"]
    cost = ctx * pin * wm / 1e6  # first call writes the start of the session
    reading = ctx
    handoffs, peak, done = 0, ctx, 0
    extra_calls = 0
    while done < m_calls:
        if handoff and ctx + new > handoff["H"]:
            # summary call: reads the context, writes S output tokens; new session starts with ctx0 + S
            cost += ctx * pread / 1e6 + handoff["S"] * pout / 1e6
            ctx = p["ctx0"] + handoff["S"]
            cost += ctx * pin * wm / 1e6
            for _ in range(int(round(handoff["E"]))):
                r = handoff["R"] / max(handoff["E"], 1)
                cost += ctx * pread / 1e6 + r * pin * wm / 1e6 + out * pout / 1e6
                ctx += r
                extra_calls += 1
            handoffs += 1
        cost += ctx * pread / 1e6 + new * pin * wm / 1e6 + out * pout / 1e6
        ctx += new
        peak = max(peak, ctx)
        done += 1
    return {"cost": cost, "handoffs": handoffs, "peak_ctx": peak, "extra_calls": extra_calls}


def break_even_ratio(n, p_o, pr_o, p_s, pr_s, hand):
    """Opus calls / Sonnet calls at which both cost the same, for n Sonnet productive calls."""
    target = session_cost(n, p_s, pr_s, hand)["cost"]
    lo, hi = 0.05, 3.0
    if session_cost(max(int(round(hi * n)), 1), p_o, pr_o)["cost"] < target:
        return None
    for _ in range(40):
        mid = (lo + hi) / 2
        if session_cost(max(int(round(mid * n)), 1), p_o, pr_o)["cost"] < target:
            lo = mid
        else:
            hi = mid
    return (lo + hi) / 2


def med(vals):
    vals = [v for v in vals if v is not None]
    return statistics.median(vals) if vals else None


def main():
    a = sys.argv[1:]

    def opt(name, default):
        return a[a.index(name) + 1] if name in a else default

    days = int(opt("--days", 30))
    H = int(opt("--handoff-tokens", 150000))
    out_dir = Path(opt("--out", str(LAB / "measurements" / "2026-10-28-window-model")))
    out_dir.mkdir(parents=True, exist_ok=True)
    con = connect()
    idx = index_transcripts()
    hs, skipped = measure_handoffs(con, idx)
    growth = measure_growth(con, idx, days)
    s_tok = med([h["summary_tokens"] for h in hs])
    re_reads = med([h["reopen_reads"] for h in hs])
    re_tok = med([h["reopen_chars"] / CHARS_PER_TOKEN for h in hs])
    calls_to_write = med([h["tool_calls_before_first_write"] for h in hs])
    params = {"handoffs": hs, "handoffs_skipped_no_prev_transcript": skipped, "growth": growth,
              "summary_tokens_median": s_tok, "reopen_reads_median": re_reads, "reopen_tokens_median": re_tok,
              "calls_before_first_write_median": calls_to_write, "chars_per_token_assumed": CHARS_PER_TOKEN, "H": H}
    (out_dir / "params.json").write_text(json.dumps(params, ensure_ascii=False, indent=1), encoding="utf-8")

    L = []
    L.append(f"handoffs with both transcripts: {len(hs)} (skipped, previous transcript gone: {skipped})")
    L.append("| run | session | summary tok | reopen reads | reopen tok (chars/4) | tool calls before 1st write | prev calls | prev last ctx |\n|---|---|---|---|---|---|---|---|")
    for h in hs:
        L.append(f"| {h['run']} | {h['session']} | {h['summary_tokens']:.0f} | {h['reopen_reads']} | "
                 f"{h['reopen_chars'] / CHARS_PER_TOKEN:.0f} | {h['tool_calls_before_first_write']} | {h['prev_calls']} | {h['prev_last_ctx']} |")
    L.append(f"| **median** | | {s_tok} | {re_reads} | {re_tok} | {calls_to_write} | | |")
    L.append("")
    L.append("growth (main transcripts, no handoff, >= 30 calls, last %d days):" % days)
    L.append("| model | sessions | calls | ctx0 median | new tok/call median (mean) | out tok/call median (mean) | 1h share of writes | median calls/session |\n|---|---|---|---|---|---|---|---|")
    for fam, g in growth.items():
        L.append(f"| {fam} | {g['sessions']} | {g['calls']} | {g['ctx0_median']:.0f} | {g['new_per_call_median']:.0f} ({g['new_per_call_mean']:.0f}) | "
                 f"{g['out_per_call_median']:.0f} ({g['out_per_call_mean']:.0f}) | {100 * g['w1h_share']:.0f}% | {g['calls_median']:.0f} |")
    L.append("")
    L.append("context at call k (median over sessions with >= k calls; n in brackets):")
    for fam, g in growth.items():
        L.append(f"- {fam}: " + ", ".join(f"k={k}: {v['median']:.0f} (n={v['n']})" for k, v in g["ctx_at_call"].items()))

    # --- model ---
    base = growth.get("sonnet") or next(iter(growth.values()))
    pr_o = (price("claude-opus-5-5")[0], price("claude-opus-5-5")[1], price("claude-opus-5-5")[0] * READ)
    pr_s = (price("claude-sonnet-5-5")[0], price("claude-sonnet-5-5")[1], price("claude-sonnet-5-5")[0] * READ)
    pr_o_alt = (pr_o[0], pr_o[1], 0.20)  # read price quoted in the task text: $0.20/M for both models

    def prm(stat):
        return {"ctx0": base["ctx0_median"], "new": base["new_per_call_" + stat], "out": base["out_per_call_" + stat],
                "w1h_share": base["w1h_share"]}

    hand0 = {"H": H, "S": s_tok or 0, "R": 0, "E": 0}  # exact-path reopen measured 0 reads in all handoffs
    e_up = calls_to_write or 0                              # upper bound: every call before the first write is overhead
    hand_up = {"H": H, "S": s_tok or 0, "R": e_up * base["new_per_call_median"], "E": e_up}
    L.append("")
    L.append(f"Prices ($/M): Opus 5.5 in {pr_o[0]}, out {pr_o[1]}, read {pr_o[2]:.2f}; Sonnet 5.5 in {pr_s[0]}, out {pr_s[1]}, read {pr_s[2]:.2f} (PRICES x READ={READ}). "
             f"H={H}, S={hand_up['S']:.0f}; overhead per handoff: lower bound E=0, upper bound E={hand_up['E']:.0f} calls adding {hand_up['R']:.0f} tokens. Token profile: Sonnet main sessions.")
    for label, stat, pro, hand in (("base: median tokens/call, read = READ x input, handoff overhead = upper bound", "median", pr_o, hand_up),
                                   ("sensitivity: handoff overhead = 0 (measured exact reopen)", "median", pr_o, hand0),
                                   ("sensitivity: mean tokens/call, overhead upper bound", "mean", pr_o, hand_up),
                                   ("sensitivity: Opus read $0.20/M (as in the task text), overhead upper bound", "median", pr_o_alt, hand_up)):
        p = prm(stat)
        L.append("")
        L.append(f"### {label}")
        L.append("| N productive calls | Sonnet, no cap $ | Sonnet + handoff $ (handoffs) | Opus 1M $ (peak ctx) | Opus/Sonnet+handoff at equal calls | break-even Opus/Sonnet call ratio |\n|---|---|---|---|---|---|")
        for n in (25, 50, 75, 100, 150, 200, 300, 500, 800):
            sn = session_cost(n, p, pr_s)
            sh = session_cost(n, p, pr_s, hand)
            op = session_cost(n, p, pro)
            be = break_even_ratio(n, p, pro, p, pr_s, hand)
            L.append(f"| {n} | {sn['cost']:.2f} | {sh['cost']:.2f} ({sh['handoffs']}) | {op['cost']:.2f} ({op['peak_ctx'] / 1000:.0f}K) | "
                     f"{op['cost'] / sh['cost']:.2f} | {('%.2f' % be) if be else '>3'} |")
    txt = "\n".join(L)
    (out_dir / "tables.md").write_text(txt, encoding="utf-8")
    print(txt)


if __name__ == "__main__":
    main()
