"""Slowest packages and tests of the Go suite, from `go test -count=1 -json ./... > gotest.json`.

Usage: python -I slow.py gotest.json
"""
import collections
import datetime as dt
import json
import sys

sys.stdout.reconfigure(encoding="utf-8")
path = sys.argv[1] if len(sys.argv) > 1 else "gotest.json"
pkg_elapsed, tests = {}, []
first = last = None
for line in open(path, encoding="utf-8", errors="replace"):
    try:
        o = json.loads(line)
    except Exception:
        continue
    t = o.get("Time")
    if t:
        ts = dt.datetime.fromisoformat(t.replace("Z", "+00:00"))
        first = ts if first is None else min(first, ts)
        last = ts if last is None else max(last, ts)
    if o.get("Action") in ("pass", "fail") and "Elapsed" in o:
        if o.get("Test"):
            tests.append((o["Elapsed"], o["Package"].replace("claude-manager/", ""), o["Test"], o["Action"]))
        else:
            pkg_elapsed[o["Package"].replace("claude-manager/", "")] = o["Elapsed"]

print(f"suite wall: {(last - first).total_seconds()/60:.1f} min; packages: {len(pkg_elapsed)}; tests with timing: {len(tests)}")
print("\nslowest packages (s):")
for p, e in sorted(pkg_elapsed.items(), key=lambda x: -x[1])[:8]:
    print(f"  {e:7.1f}  {p}")

# top-level tests only (subtests contain '/')
top = [x for x in tests if "/" not in x[2]]
print("\nslowest tests (s):")
for e, p, n, a in sorted(top, key=lambda x: -x[0])[:14]:
    print(f"  {e:7.1f}  {p}: {n}")

by_pkg = collections.defaultdict(list)
for e, p, n, a in top:
    by_pkg[p].append(e)
print("\nper package: sum of test seconds / tests over 10 s (sum of those) / tests under 1 s (count)")
for p in ("internal/session", "internal/experience", "internal/control", "cmd/cm-mcp-eval", "internal/logship", "internal/suggest", "cmd/cm-mcp"):
    v = by_pkg.get(p, [])
    big = [x for x in v if x > 10]
    print(f"  {p:22s} sum {sum(v):7.1f}  >10s: {len(big):3d} ({sum(big):6.1f} s)  <1s: {sum(1 for x in v if x < 1)} of {len(v)}  pkg {pkg_elapsed.get(p, 0):6.1f}")
