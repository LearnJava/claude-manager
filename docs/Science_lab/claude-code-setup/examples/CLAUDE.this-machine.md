# User-level instructions

## Scripting on this Windows machine

Measured on this machine (Windows 10, see the runtime benchmark in
`D:\GoProjects\MetalSwing\.claude\worktrees\ai-agent-runtime-benchmarks-d96428\research\runtime-bench\REPORT.md`).

- Call Python by its full path:
  `C:\Users\konstantin\AppData\Local\Python\pythoncore-3.14-64\python.exe`
  The `WindowsApps\python.exe` alias adds ~100 ms to every start (140 ms vs 36 ms).
- In Python scripts, force UTF-8 I/O: `sys.stdout.reconfigure(encoding="utf-8")`, and read stdin as
  `sys.stdin.buffer.read().decode("utf-8")`. The default console code page is not UTF-8.
- Never call `threading.stack_size(...)` with large values — it raises `ValueError` on Windows.
  Prefer iterative algorithms over deep recursion.
- Do not name script files after stdlib modules (`json.py`, `base64.py`, `csv.py`, ...): the script's
  directory shadows the standard library.
- PowerShell is for Windows administration only (services, registry, ACLs, scheduled tasks), not for data
  processing — it is 100-1000x slower than Python and starts in 160-270 ms.
  - Variable and function names are case-insensitive: `$W` and `$w` are the same variable.
  - Do not name functions after built-in aliases (`move`, `copy`, `sort`, `diff`, `cat`, `ls`, ...):
    aliases take precedence over functions.
  - Windows PowerShell 5.1 (`powershell.exe`) is the system default; PowerShell 7.6 is at
    `D:\bench\pwsh7\pwsh.exe`. Scripts written for 5.1 ran unchanged on 7.6.
- QuickJS-ng (`D:\bench\bin\qjs.exe`): import modules as `"qjs:std"` / `"qjs:os"`, not `"std"` / `"os"`.

### Garbled (non-UTF-8) output — the most common real failure here

Measured over 6,431 Claude Code sessions of the Lumen project: 23% of Python runs without a UTF-8 fix
returned unreadable output (Russian text mangled), versus 0.9% with the fix; `tasklist` / `taskkill` output
was unreadable in 58-67% of calls. The OS is Russian-localized, so native tools print CP866 / UTF-16.

- Python: always `sys.stdout.reconfigure(encoding="utf-8")` in scripts, or prefix one-liners with
  `PYTHONIOENCODING=utf-8`.
- Native console tools (`taskkill`, `tasklist`, `netstat`, `schtasks`, `cmd /c ...`): pipe through
  `iconv -f cp866 -t utf-8`. Prefer `tasklist //FO CSV //NH` for parsable output.
- Do not use `wmic` (deprecated, prints UTF-16): use PowerShell `Get-CimInstance Win32_Process`.
- PowerShell from Git Bash: wrap `-Command` in single quotes so bash does not expand `$_`, `$null`,
  `$env:...`, and start with `[Console]::OutputEncoding=[Text.Encoding]::UTF8;`. Prefer
  `... | ConvertTo-Json -Compress` over formatted tables.

## Cost: every tool output is paid again on every later turn

Measured over 2,474 Claude Code sessions of the Lumen project (~$9,560 at API prices): 65% of spend is
cache reads — the whole conversation re-read on each API call — 21% cache writes, 13% output.
A tool result of N tokens costs roughly `N × (2 + 0.1 × turns_remaining)` input-price units, so after
~20 more turns re-reading already costs more than writing it. Sessions over 100 calls are 21% of sessions
but 55% of spend (context grows to 150-280k tokens per call). Therefore:

- **Keep tool output small.** Cap every command: `| head -n 50`, `| tail -n 30`, `grep -c`, `grep -m 20`,
  `git pull -q`, `git log --oneline -n 20`, `git diff --stat`. Send long build/test output to a log file
  and print only the summary (`cargo test ... > .tmp/t.log 2>&1; grep -E "^test result|FAILED|error\[" .tmp/t.log | head -n 40`).
- **Do not read large files whole.** For files over ~400 lines (and always for ROADMAP.md, BUGS.md,
  CAPABILITIES.md and similar multi-MB docs): `grep -n` first, then Read with `offset`/`limit`.
- **Do not re-read** a file or re-run a command whose output is already in the conversation.
- **Batch steps:** a chain of more than ~3 commands over the same data is one script (see below).
- **Delegate reading-heavy exploration to a subagent** — its context is discarded and only the summary
  returns. Keep the main session short: when a task is done, finish rather than continuing into an
  unrelated one in the same session.

## Multi-step data work: write one script

When a task needs more than ~3 steps over data (parsing logs, CSV/JSON, many files, aggregation,
comparisons, cross-checking), do not pull the data into the conversation piece by piece with many
Read/Grep/shell calls. Instead use the `script-first` skill: write one Python script, run it, and print
only the result (numbers, a small table, the first N matching lines). Never print raw bulk data.

Run long-running or risky scripts under the Job Object limiter:

```
D:\bench\bin\jobrun.exe <mem_MB> <timeout_ms> <program> <args...>
```

e.g. `D:\bench\bin\jobrun.exe 1024 120000 C:\Users\konstantin\AppData\Local\Python\pythoncore-3.14-64\python.exe script.py`
(1 GB memory cap, 2-minute hard timeout, child processes blocked). Exit code 124 means timeout.
