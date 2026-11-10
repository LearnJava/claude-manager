"""t6 verify: hidden tests for all 14 packages + FIXES.md completeness."""
import shutil
import subprocess
import sys
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
HERE = Path(__file__).resolve().parent
SANDBOX = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path.cwd()

PKGS = ["revstr", "clamp", "uniq", "sumeven", "fmtdate", "titlecase", "maxval",
        "countwords", "absdiff", "ispalin", "flatten", "within", "mergesort", "sign"]


def run(cmd):
    return subprocess.run(cmd, cwd=SANDBOX, capture_output=True, timeout=600)


def main() -> int:
    for p in PKGS:
        src = HERE / "hidden" / f"{p}_hidden_test.go"
        shutil.copy2(src, SANDBOX / "pkg" / p / f"{p}_hidden_test.go")

    for cmd in (["go", "build", "./..."], ["go", "vet", "./..."], ["go", "test", "./..."]):
        r = run(cmd)
        if r.returncode != 0:
            print(" ".join(cmd), "->", r.returncode)
            print((r.stdout + r.stderr).decode("utf-8", "replace")[-1800:])
            print("FAIL")
            return 1

    fixes = SANDBOX / "FIXES.md"
    if not fixes.exists():
        print("FAIL: FIXES.md не создан")
        return 1
    text = fixes.read_text(encoding="utf-8", errors="replace")
    missing = [p for p in PKGS if p not in text]
    if missing:
        print(f"FAIL: FIXES.md без упоминаний: {missing}")
        return 1
    print("PASS (14 пакетов + FIXES.md)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
