---
name: script-first
description: Use when a task needs many steps over data — parsing logs, CSV/JSON, many files, aggregation, statistics, comparisons, cross-checks. Solve it with one Python script run under a Job Object limiter instead of many separate tool calls, and return only the result.
---

# Script-first

Doing data work as a chain of Read/Grep/shell calls pushes every intermediate result through the
conversation: slow, expensive, and error-prone for counting. Write one program instead, keep the data
inside it, and print only the answer. This is the "code mode" pattern (Anthropic programmatic tool
calling, Cloudflare Code Mode) applied to a local Windows machine.

## When to use

- More than ~3 steps over the same data, or a loop over many files/items.
- Inputs are large (logs, tables, JSON dumps) but the needed answer is small.
- Exact arithmetic or counting is required.

Do not use it for a single lookup, for reading code to understand it, or when you must judge the meaning
of each intermediate result yourself.

## Steps

1. State the result you need: which numbers, table, or list.
2. Write ONE script to `%TEMP%\claude-scripts\<short-task-name>.py`
   (create the directory if missing; never name the file after a stdlib module).
   - First lines: `import sys` and `sys.stdout.reconfigure(encoding="utf-8")`.
   - Read files as UTF-8 explicitly; handle CRLF.
   - Do all filtering, joining and aggregation inside the script.
   - Print only the result: at most ~50 lines, plus counts of what was processed and skipped.
3. Run it under the limiter, with Python called by full path:

   ```
   {{JOBRUN}} 1024 120000 "{{PYTHON}}" <script>.py [args]
   ```

   Exit code 124 = timeout, the process was killed. A `MemoryError` means the 1 GB cap was hit: stream the
   input instead of loading it all, or raise the cap deliberately.
4. On an error, fix the script and rerun it. Do not fall back to reading the data by hand.
5. Answer with the result and the script path, so it can be rerun later without the model.

## Notes

- Use the standard library first (`json`, `csv`, `re`, `collections`, `datetime`, `decimal`, `hashlib`,
  `pathlib`, `sqlite3`). For money and rounding use `decimal`, never `float`.
- Importing `numpy` adds ~200 ms to startup; use it only for real numeric work.
- The limiter blocks child processes: a script cannot start `git`, `cmd` or another Python from inside.
  Run such commands as separate shell calls.
