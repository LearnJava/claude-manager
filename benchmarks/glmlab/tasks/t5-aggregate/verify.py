"""t5 verify: run agent's aggregate.py, compare summary.json with reference."""
import csv
import json
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
SANDBOX = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path.cwd()
PY = r"C:\Users\konstantin\AppData\Local\Python\pythoncore-3.14-64\python.exe"


def reference(path: Path):
    by_cat = defaultdict(float)
    by_month = defaultdict(float)
    by_prod = defaultdict(float)
    units = 0
    total = 0.0
    with path.open(encoding="utf-8") as f:
        for row in csv.DictReader(f):
            rev = int(row["qty"]) * float(row["unit_price"])
            total += rev
            by_cat[row["category"]] += rev
            by_month[row["date"][:7]] += rev
            by_prod[row["product"]] += rev
            units += int(row["qty"])
    return {
        "total_revenue": round(total, 2),
        "by_category": {k: round(v, 2) for k, v in sorted(by_cat.items())},
        "monthly": {k: round(v, 2) for k, v in sorted(by_month.items())},
        "top_product": max(by_prod.items(), key=lambda kv: kv[1])[0],
        "units_sold": units,
    }


def main() -> int:
    out = SANDBOX / "summary.json"
    if out.exists():
        out.unlink()
    r = subprocess.run([PY, "aggregate.py", "sales.csv", "summary.json"], cwd=SANDBOX,
                       capture_output=True, timeout=120)
    if r.returncode != 0 or not out.exists():
        print((r.stdout + r.stderr).decode("utf-8", "replace")[-1200:])
        print("FAIL: aggregate.py did not produce summary.json")
        return 1
    got = json.loads(out.read_text(encoding="utf-8"))
    want = reference(SANDBOX / "sales.csv")
    if got != want:
        print("FAIL: summary.json != reference")
        for k in want:
            if got.get(k) != want[k]:
                print(f"  {k}: got {got.get(k)!r}, want {want[k]!r}")
        return 1
    print("PASS")
    return 0

if __name__ == "__main__":
    sys.exit(main())
