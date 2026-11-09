"""t3 verify: run the agent's parser.py, compare out.json with a reference implementation."""
import json
import re
import subprocess
import sys
from collections import Counter
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
HERE = Path(__file__).resolve().parent
SANDBOX = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path.cwd()
PY = r"C:\Users\konstantin\AppData\Local\Python\pythoncore-3.14-64\python.exe"
LEVELS = {"INFO", "WARN", "ERROR", "DEBUG"}
LINE_RE = re.compile(r"^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z) +([A-Z]+) +module=(\S+)(.*)$")
KV_RE = re.compile(r'(\w+)=("(?:[^"]*)"|\S+)')


def reference(path: Path):
    entries = []
    for raw in path.read_text(encoding="utf-8").splitlines():
        m = LINE_RE.match(raw)
        if not m:
            continue
        ts, level, module, rest = m.groups()
        if level not in LEVELS:
            continue
        h, mi, s = ts[11:13], ts[14:16], ts[17:19]
        if not (int(h) < 24 and int(mi) < 60 and int(s) < 60):
            continue
        attrs = {}
        for k, v in KV_RE.findall(rest):
            attrs[k] = v[1:-1] if v.startswith('"') else v
        entries.append({"ts": ts, "level": level, "module": module, "attrs": attrs})
    levels = Counter(e["level"] for e in entries)
    modules = Counter(e["module"] for e in entries)
    codes = Counter(e["attrs"]["code"] for e in entries if e["level"] == "ERROR" and "code" in e["attrs"])
    return {"total": len(entries),
            "levels": dict(levels),
            "modules": dict(modules),
            "errors_by_code": dict(codes)}


def main() -> int:
    out = SANDBOX / "out.json"
    if out.exists():
        out.unlink()
    r = subprocess.run([PY, "parser.py", "app.log", "out.json"], cwd=SANDBOX,
                       capture_output=True, timeout=120)
    if r.returncode != 0 or not out.exists():
        print((r.stdout + r.stderr).decode("utf-8", "replace")[-1200:])
        print("FAIL: parser.py did not produce out.json")
        return 1
    got = json.loads(out.read_text(encoding="utf-8"))
    want = reference(HERE / "starter" / "app.log")
    if got != want:
        print("FAIL: out.json != reference")
        print("got :", json.dumps(got, ensure_ascii=False, sort_keys=True)[:600])
        print("want:", json.dumps(want, ensure_ascii=False, sort_keys=True)[:600])
        return 1
    print(f"PASS ({want['total']} entries)")
    return 0

if __name__ == "__main__":
    sys.exit(main())
