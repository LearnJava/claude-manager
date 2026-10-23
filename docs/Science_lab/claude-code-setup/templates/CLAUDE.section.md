## Scripting on this Windows machine

Installed by claude-code-setup (see its SETUP.md for the measurements behind each rule).

- Call Python by its full path: `{{PYTHON}}`
  The `WindowsApps\python.exe` alias adds ~100 ms to every start (measured: 140 ms vs 36 ms).
- In Python scripts force UTF-8 I/O: `sys.stdout.reconfigure(encoding="utf-8")`, and read stdin as
  `sys.stdin.buffer.read().decode("utf-8")`. For one-liners prefix the command with `PYTHONIOENCODING=utf-8`.
  The console code page here is {{OEM_CP}}, not UTF-8.
- Never call `threading.stack_size(...)` with large values: it raises `ValueError` on Windows.
  Prefer iterative algorithms over deep recursion.
- Do not name script files after stdlib modules (`json.py`, `base64.py`, `csv.py`, ...): the script's
  directory shadows the standard library.
- PowerShell is for Windows administration only (services, registry, ACLs, scheduled tasks), not for data
  processing: it is 100-1000x slower than Python and starts in 160-270 ms.
  - Variable and function names are case-insensitive: `$W` and `$w` are the same variable.
  - Do not name functions after built-in aliases (`move`, `copy`, `sort`, `diff`, `cat`, `ls`, ...):
    aliases take precedence over functions.
  - From Git Bash wrap `-Command` in single quotes so bash does not expand `$_`, `$null`, `$env:...`,
    start it with `[Console]::OutputEncoding=[Text.Encoding]::UTF8;`, and prefer `ConvertTo-Json -Compress`
    over formatted tables.
- Do not use `wmic` (deprecated, prints UTF-16): use PowerShell `Get-CimInstance Win32_Process`.
{{OEM_SECTION}}
## Cost: every tool output is paid again on every later turn

Measured over 2,474 Claude Code sessions (~$9,560 at API prices): 65% of spend is cache reads (the whole
conversation re-read on each API call), 21% cache writes, 13% output. A tool result of N tokens costs roughly
`N x (2 + 0.1 x turns_remaining)` input-price units, so after ~20 more turns re-reading costs more than writing it.
Sessions over 100 calls were 21% of sessions but 55% of spend. Therefore:

- **Keep tool output small.** Cap every command: `| head -n 50`, `| tail -n 30`, `grep -c`, `grep -m 20`,
  `git pull -q`, `git log --oneline -n 20`, `git diff --stat`. Send long build/test output to a log file and
  print only the summary, e.g. `cargo test > .tmp/t.log 2>&1; grep -E "test result|FAILED|error\[" .tmp/t.log | head -n 40`.
  Cap line width too: 16 lines of a markdown table measured 6,600 tokens — add `| cut -c1-300` to `grep`/`sed`/`cat`
  over `.md` files and logs.
- **Do not read large files whole.** For files over ~400 lines: `grep -n` first, then Read with `offset`/`limit`.
- **Do not re-read** a file or re-run a command whose output is already in the conversation.
- **Batch independent commands** into one Bash call (`;` / `&&`) instead of one call each.
- **Batch steps over data into one script** (next section).
- **Delegate reading-heavy exploration to a subagent**: its context is discarded and only the summary returns.
- **Keep sessions short**: when a task is done, finish instead of continuing into an unrelated task. For long
  tasks, write a handoff note (done / next / branch) where the project keeps task state and end the session
  rather than running past ~100 tool calls.
- **Wait for a long command in one blocking call, never by polling.** Every `sleep 60; tail log` call re-reads
  the whole conversation (Lumen, 14-23.10.2026: 540 such polls, ~12% of spend), and a subagent's prompt cache
  expires after 5 idle minutes and is rebuilt. Run the command in the foreground with a long `timeout`
  (up to 600000 ms), or block on its log in one call:
  `timeout 590 bash -c 'until grep -qE "Finished|error" .tmp/b.log; do sleep 10; done'; tail -n 20 .tmp/b.log`.
  In an interactive session `run_in_background` plus the completion notice costs nothing while waiting.
  Do not give a subagent work that has to wait for a build or a test run.

## Multi-step data work: write one script

When a task needs more than ~3 steps over data (parsing logs, CSV/JSON, many files, aggregation, comparisons,
cross-checking), do not pull the data into the conversation piece by piece. Use the `script-first` skill:
write one Python script, run it, print only the result (numbers, a small table, the first N matching lines).
Measured: the same log analysis took 118 turns / 317 s / $0.74 with tool calls and 3 turns / 47 s / $0.077
with one script; on 50 MB of logs the tool-call variant could not finish at all.

Run long-running or risky scripts under the Job Object limiter (memory cap, hard timeout, no child processes;
exit code 124 = timeout):

```
{{JOBRUN}} <mem_MB> <timeout_ms> <program> <args...>
```

e.g. `{{JOBRUN}} 1024 120000 "{{PYTHON}}" script.py`
