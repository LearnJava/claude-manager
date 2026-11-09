"""t2 verify: hidden tests (generated with an independent Python oracle) + go test."""
import shutil
import subprocess
import sys
from datetime import date, timedelta
from pathlib import Path

sys.stdout.reconfigure(encoding="utf-8")
HERE = Path(__file__).resolve().parent
SANDBOX = Path(sys.argv[1]).resolve() if len(sys.argv) > 1 else Path.cwd()

# Independent oracle: weekday counts computed in Python.
def weekdays(a: date, b: date) -> int:
    n, d = 0, a
    while d <= b:
        if d.weekday() < 5:
            n += 1
        d += timedelta(days=1)
    return n

CASES = [
    ("2026-01-01", "2026-01-07"),   # Thu..Wed, crosses a weekend
    ("2026-01-03", "2026-01-04"),   # Sat..Sun only
    ("2026-01-05", "2026-01-05"),   # single Monday
    ("2026-01-04", "2026-01-04"),   # single Sunday
    ("2026-02-27", "2026-03-02"),   # month boundary
    ("2026-12-30", "2027-01-02"),   # year boundary
    ("2026-01-01", "2026-03-31"),   # long range
]

def main() -> int:
    lines = ["package datespan", "",
             'import (', '\t"testing"', '\t"time"', ')', ""]
    a, b = CASES[0][0], CASES[0][1]
    lines += [
        "func TestParseISORangeErrors(t *testing.T) {",
        '\tif _, _, err := ParseISORange("2026-01-05..2026-01-01"); err == nil {',
        '\t\tt.Fatal("start after end must error")',
        "\t}",
        '\tif _, _, err := ParseISORange("garbage"); err == nil {',
        '\t\tt.Fatal("garbage must error")',
        "\t}",
        '\tif _, _, err := ParseISORange("2026-02-30..2026-03-01"); err == nil {',
        '\t\tt.Fatal("invalid calendar date must error")',
        "\t}",
        '\tif _, _, err := ParseISORange("2026-1-1..2026-01-05"); err == nil {',
        '\t\tt.Fatal("non-ISO shape must error")',
        "\t}",
        '\tif _, _, err := ParseISORange("2026-01-01..2026-01-01"); err != nil {',
        '\t\tt.Fatalf("equal dates must be allowed: %v", err)',
        "\t}",
        "}",
        "",
        "func TestWeekdaysBetween(t *testing.T) {",
        '\tutc := func(s string) time.Time { t0, _ := time.ParseInLocation("2006-01-02", s, time.UTC); return t0 }',
    ]
    for s, e in CASES:
        lines.append(f'\tif n := WeekdaysBetween(utc("{s}"), utc("{e}")); n != {weekdays(date.fromisoformat(s), date.fromisoformat(e))} {{')
        lines.append(f'\t\tt.Fatalf("{s}..{e}: got %d", n)')
        lines.append("\t}")
    lines += [
        '\tif n := WeekdaysBetween(utc("2026-01-05"), utc("2026-01-01")); n != 0 {',
        '\t\tt.Fatalf("start after end: got %d, want 0", n)',
        "\t}",
        "}",
    ]
    (SANDBOX / "datespan_hidden_test.go").write_text("\n".join(lines) + "\n", encoding="utf-8")

    r = subprocess.run(["go", "test", "./..."], cwd=SANDBOX, capture_output=True, timeout=300)
    out = (r.stdout + r.stderr).decode("utf-8", "replace")
    print(out[-1500:])
    if r.returncode != 0:
        print("FAIL: go test exited", r.returncode)
        return 1
    print("PASS")
    return 0

if __name__ == "__main__":
    sys.exit(main())
