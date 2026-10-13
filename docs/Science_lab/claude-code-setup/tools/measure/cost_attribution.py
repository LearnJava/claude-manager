"""Where does Claude Code spend money? Cost attribution over Claude Code transcripts (*.jsonl).

Per API call we read usage (input, cache write 5m/1h, cache read, output) and price it.
New context added by a tool result is paid once as a cache write and then re-read (cache read)
on every later API call of the session. So each tool result has a "lifetime cost":
    tokens * (write_price + read_price * calls_remaining_in_session)
We attribute each call's cache-write tokens to the tool results that arrived just before it
(proportionally by character size) and aggregate lifetime cost by tool / command category.
"""
import collections
import json
import re
import sys
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")

# USD per 1M tokens: input, output (claude-api skill pricing table, cached 2026-10-06).
PRICES = {
    "claude-sonnet-5": (2.0, 10.0), "claude-sonnet-5-5": (2.0, 10.0),
    "claude-opus-5": (5.0, 25.0), "claude-opus-5-5": (4.0, 20.0),
    "claude-haiku-5-5": (0.10, 0.50), "claude-sonnet-4-6": (3.0, 15.0),
    "claude-opus-4-8": (5.0, 25.0), "claude-opus-4-7": (5.0, 25.0), "claude-opus-4-6": (5.0, 25.0),
    "claude-haiku-4-5": (1.0, 5.0), "claude-fable-5": (10.0, 50.0), "claude-fable-5-1": (10.0, 50.0),
}
W5, W1H, READ = 1.25, 2.0, 0.10


def price(model):
    m = re.sub(r"\[.*?\]|-\d{8}$", "", model or "")
    return PRICES.get(m)


def category(name, inp):
    if name == "Bash":
        cmd = (inp or {}).get("command", "").strip()
        cmd = re.sub(r"^(cd\s+\S+\s*(&&|;)\s*)+", "", cmd)
        cmd = re.sub(r"^(export\s+\S+\s*(&&|;)\s*)+", "", cmd)
        first = cmd.split()[0].rsplit("/", 1)[-1].lower() if cmd.split() else "?"
        if first in ("cargo",):
            sub = cmd.split()[1] if len(cmd.split()) > 1 else ""
            return f"Bash: cargo {sub}"
        if first in ("python", "python3", "py") or first.endswith("python.exe"):
            return "Bash: python"
        if first == "git":
            sub = cmd.split()[1] if len(cmd.split()) > 1 else ""
            return f"Bash: git {sub}"
        if first in ("grep", "rg", "sed", "awk", "cat", "head", "tail", "wc", "find", "ls"):
            return f"Bash: {first}"
        return "Bash: other"
    if name == "Read":
        p = (inp or {}).get("file_path", "")
        base = re.split(r"[\\/]", p)[-1]
        if base.upper().startswith(("ROADMAP", "BUGS", "CAPABILITIES", "STATUS", "CLAUDE", "SUBSYSTEMS", "DECISIONS")):
            return f"Read: {base}"
        ext = base.rsplit(".", 1)[-1].lower() if "." in base else ""
        return f"Read: *.{ext}" if ext else "Read: other"
    if name in ("Grep", "Glob", "Edit", "Write", "Agent", "Task", "WebFetch", "WebSearch", "TodoWrite"):
        return name
    if name.startswith("mcp__"):
        return "MCP: " + name.split("__")[1]
    return name


def result_chars(content):
    if isinstance(content, str):
        return len(content)
    if isinstance(content, list):
        n = 0
        for b in content:
            if isinstance(b, dict):
                n += len(b.get("text", "") or "") + (2000 if b.get("type") == "image" else 0)
        return n
    return 0


def main():
    root = Path(sys.argv[1])
    out_path = Path(sys.argv[2])
    files = sorted(root.glob("*.jsonl"))
    comp = collections.Counter()           # USD by component
    comp_tok = collections.Counter()
    by_model = collections.Counter()
    cat_cost = collections.Counter()       # lifetime USD by category
    cat_tok = collections.Counter()
    cat_n = collections.Counter()
    top_items = []
    sessions = []
    for f in files:
        calls = []          # (model_prices, usage) per unique API call
        chunks = []         # (call_index, tokens, category, desc, prices)
        seen = set()
        pending_tools = {}  # tool_use_id -> (category, desc)
        arrived = []        # tool results waiting for next call: (category, chars, desc)
        try:
            fh = f.open(encoding="utf-8", errors="replace")
        except OSError:
            continue
        with fh:
            for line in fh:
                try:
                    d = json.loads(line)
                except ValueError:
                    continue
                t = d.get("type")
                msg = d.get("message") or {}
                if t == "assistant":
                    mid = msg.get("id") or d.get("requestId")
                    content = msg.get("content") or []
                    for b in content if isinstance(content, list) else []:
                        if isinstance(b, dict) and b.get("type") == "tool_use":
                            inp = b.get("input") or {}
                            desc = (inp.get("command") or inp.get("file_path") or inp.get("pattern") or "")[:120]
                            pending_tools[b.get("id")] = (category(b.get("name", ""), inp), desc)
                    if mid in seen:
                        continue
                    seen.add(mid)
                    u = msg.get("usage") or {}
                    pr = price(msg.get("model"))
                    if not pr or not u:
                        continue
                    pin, pout = pr
                    cc = u.get("cache_creation") or {}
                    w1h = cc.get("ephemeral_1h_input_tokens")
                    w5 = cc.get("ephemeral_5m_input_tokens")
                    cw = u.get("cache_creation_input_tokens", 0) or 0
                    if w1h is None and w5 is None:
                        w1h, w5 = cw, 0
                    cr = u.get("cache_read_input_tokens", 0) or 0
                    it = u.get("input_tokens", 0) or 0
                    ot = u.get("output_tokens", 0) or 0
                    costs = {"input": it * pin, "cache_write": (w5 * W5 + w1h * W1H) * pin,
                             "cache_read": cr * READ * pin, "output": ot * pout}
                    for k, v in costs.items():
                        comp[k] += v / 1e6
                    comp_tok["input"] += it; comp_tok["cache_write"] += cw
                    comp_tok["cache_read"] += cr; comp_tok["output"] += ot
                    by_model[msg.get("model")] += sum(costs.values()) / 1e6
                    idx = len(calls)
                    calls.append(pr)
                    new_tok = cw + it
                    total_chars = sum(c for _, c, _ in arrived)
                    wprice = (W1H if (w1h or 0) >= (w5 or 0) else W5) * pin
                    if arrived and total_chars > 0:
                        # share of the new tokens that belongs to tool results (cap: the rest is model text)
                        for cat, chars, desc in arrived:
                            tok = new_tok * chars / max(total_chars, 1)
                            chunks.append((idx, tok, cat, desc, pin, wprice))
                    arrived = []
                elif t == "user":
                    content = msg.get("content")
                    for b in content if isinstance(content, list) else []:
                        if isinstance(b, dict) and b.get("type") == "tool_result":
                            cat, desc = pending_tools.pop(b.get("tool_use_id"), ("unknown", ""))
                            arrived.append((cat, result_chars(b.get("content")), desc))
        n = len(calls)
        if not n:
            continue
        s_cost = 0.0
        for idx, tok, cat, desc, pin, wprice in chunks:
            remaining = n - idx - 1
            life = tok * (wprice + READ * pin * remaining) / 1e6
            cat_cost[cat] += life
            cat_tok[cat] += tok
            cat_n[cat] += 1
            s_cost += life
            if life > 0.5:
                top_items.append((round(life, 2), int(tok), remaining, cat, desc, f.name))
        sessions.append((n, f.name))
    total = sum(comp.values())
    top_items.sort(reverse=True)
    sess_calls = sorted(n for n, _ in sessions)
    res = {
        "transcripts": len(files), "sessions_with_calls": len(sessions),
        "api_calls": sum(sess_calls),
        "calls_per_session": {"median": sess_calls[len(sess_calls) // 2] if sess_calls else 0,
                              "p90": sess_calls[int(len(sess_calls) * .9)] if sess_calls else 0,
                              "max": sess_calls[-1] if sess_calls else 0},
        "total_usd": round(total, 2),
        "usd_by_component": {k: round(v, 2) for k, v in comp.most_common()},
        "pct_by_component": {k: round(100 * v / total, 1) for k, v in comp.most_common()} if total else {},
        "tokens_by_component_M": {k: round(v / 1e6, 1) for k, v in comp_tok.items()},
        "usd_by_model": {k: round(v, 2) for k, v in by_model.most_common()},
        "tool_result_lifetime_usd_by_category": {
            k: {"usd": round(v, 2), "pct_of_total": round(100 * v / total, 1), "results": cat_n[k],
                "avg_tokens": int(cat_tok[k] / max(cat_n[k], 1))}
            for k, v in cat_cost.most_common(25)},
        "most_expensive_single_results": [
            {"usd": a, "tokens": b, "reread_by_calls": c, "category": d, "what": e, "file": f}
            for a, b, c, d, e, f in top_items[:25]],
    }
    out_path.write_text(json.dumps(res, ensure_ascii=False, indent=1), encoding="utf-8")
    print(json.dumps(res, ensure_ascii=False, indent=1))


if __name__ == "__main__":
    main()
