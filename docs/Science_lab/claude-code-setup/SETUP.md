# claude-code-setup — faster, cheaper, more reliable Claude Code on Windows

This folder is a self-contained kit. It turns the findings of a runtime and cost study (see "Why these rules"
below and `../REPORT.md` when present) into Claude Code configuration for any Windows machine.

**For the person:** copy this folder to the new machine, open Claude Code there and say:

> Follow `<path to this folder>\SETUP.md` and set up this machine.

**For Claude Code:** the rest of this file is written for you. Follow "Install" step by step.

---

## What the kit installs

| Piece | Installed to | Effect |
|---|---|---|
| Rules section (Windows traps, encoding, cost, script-first) | `%USERPROFILE%\.claude\CLAUDE.md`, between `<!-- BEGIN claude-code-setup -->` and `<!-- END claude-code-setup -->` | Loaded into every Claude Code session on this machine; the rest of the file is untouched |
| `script-first` skill | `%USERPROFILE%\.claude\skills\script-first\SKILL.md` | Multi-step data work is done by one script instead of many tool calls |
| Job Object limiter: `jobrun.exe` (or `jobrun.py` fallback) | `%USERPROFILE%\.claude\tools\` | Runs scripts with a memory cap, a hard timeout (exit 124) and no child processes |
| Permission rules for the limiter | `permissions.allow` in `%USERPROFILE%\.claude\settings.json` (merged) | Limiter runs without a prompt; rules match only the limiter command |
| Optional fix of slow hooks (`--fix-hooks`) | `hooks` in the same `settings.json` | Hooks that start `python` through the WindowsApps alias get the full interpreter path |
| `handoff_hook.py` (not enabled by default) | `%USERPROFILE%\.claude\tools\` | Per-project: asks the model to hand off before a session grows past ~90 tool calls |
| Measurement scripts | `%USERPROFILE%\.claude\tools\measure\` | Cost per session, by component and by session length, from Claude Code transcripts |

Machine-specific values are detected, not hard-coded: the real `python.exe` path, the console OEM code page
(the CP866 section is added only on Russian-localized Windows), and which limiter works.

---

## Install (instructions for Claude Code)

1. **Check prerequisites.**
   - Windows, and the Bash tool available (Git Bash).
   - A real Python 3.10+ (`py -0p` lists installed interpreters). The `...\WindowsApps\python.exe` alias does
     not count. If there is no real Python, stop and ask the person to install it (python.org installer or
     `winget install Python.Python.3.13`); do not download anything without their approval.
2. **Dry run and show the plan.** Run, with the full path of a real `python.exe`:

   ```
   "<python.exe>" "<kit>\install.py" --dry-run
   ```

   Show the person the output: detected Python, code page, limiter, files that will be written, and any hooks
   reported as calling bare `python`. Ask whether to proceed, and whether to apply `--fix-hooks` if slow hooks
   were reported. These are changes to their Claude Code configuration: get a clear yes.
3. **Install.**

   ```
   "<python.exe>" "<kit>\install.py"            (add --fix-hooks if they agreed)
   ```

   The installer backs up every file it changes as `<file>.bak-<timestamp>`, writes only its own marked section
   of `CLAUDE.md`, merges `settings.json` (an invalid `settings.json` is skipped and reported, never overwritten),
   and is idempotent: re-running changes nothing that is already current.
4. **Check the verification lines** at the end of the output: `limiter stops an infinite loop: OK` and
   `python prints UTF-8: OK`. A `FAILED` line means stop and report it; see "Troubleshooting".
5. **Tell the person** what changed (files, limiter mode, Python path) and that the changes load in a **new**
   Claude Code session. Your current session keeps the old `CLAUDE.md`.
6. **Optional, ask first:** enable the session handoff hook for projects that run long autonomous sessions
   (section "Session handoff").

If your sandbox refuses writes under `%USERPROFILE%\.claude\`, do not work around it: tell the person and give
them the exact command from step 3 to run in their own terminal.

### Manual install (if install.py cannot run)

1. Copy `tools\` to `%USERPROFILE%\.claude\tools\`. Test the limiter:
   `"%USERPROFILE%\.claude\tools\jobrun.exe" 64 1500 "<python.exe>" -c "while True: pass"` must exit with code
   124 after ~1.5 s. If `jobrun.exe` does not start, use `"<python.exe>" "%USERPROFILE%\.claude\tools\jobrun.py"`
   as the limiter command instead (same arguments).
2. Take `templates\CLAUDE.section.md`, replace `{{PYTHON}}` with the `python.exe` path, `{{JOBRUN}}` with the
   limiter command, `{{OEM_CP}}` with the code page (`chcp` shows it), and `{{OEM_SECTION}}` with the contents of
   `templates\CLAUDE.oem-cp866.md` if the code page is 866, else with nothing. Append the result to
   `%USERPROFILE%\.claude\CLAUDE.md` between the BEGIN/END markers shown above.
3. Do the same replacements in `templates\SKILL.script-first.md` and save it as
   `%USERPROFILE%\.claude\skills\script-first\SKILL.md`.
4. Add to `permissions.allow` in `%USERPROFILE%\.claude\settings.json` (merge, do not replace the file):
   `Bash(<limiter command> *)`, plus the same with forward slashes and with `/c/...` drive style, so the rule
   matches however the path is spelled. Never allow bare `python.exe *`: that would allow every Python command.

### Uninstall

```
"<python.exe>" "<kit>\install.py" --uninstall
```

Removes the marked `CLAUDE.md` section, the skill and the limiter permission rules. Leaves `tools\` and the
`*.bak-*` backups in place.

---

## Session handoff (optional, per project)

Use for projects where Claude Code runs long, mostly autonomous sessions (an orchestrator that starts a session
per task). In the study, sessions over 100 tool calls were 21% of sessions but 55% of spend.

How it works: `handoff_hook.py` counts tool calls per session. At the 90th call (and every 20 calls after) it
adds a note to the model's context: finish the current step, write a handoff note where the project keeps task
state, commit work in progress, and end the session. Nothing is killed mid-step. The next session reads the
note and continues with a small context.

Prerequisite: the project must have a place for the note that every new session reads first — for example an
"In progress" section in a status file that the session prompt already tells the model to check. Without that,
do not enable the hook.

Add to the **project's** `.claude/settings.json` (merge with existing hooks), using the real paths:

```json
{
  "env": {
    "CLAUDE_HANDOFF_AT": "90",
    "CLAUDE_HANDOFF_TARGET": "the 'In progress' section of STATUS-P1.md"
  },
  "hooks": {
    "PostToolUse": [
      { "hooks": [ { "type": "command",
        "command": "\"C:\\path\\to\\python.exe\" \"C:\\Users\\<user>\\.claude\\tools\\handoff_hook.py\"" } ] }
    ]
  }
}
```

Test before relying on it: pipe `{"session_id":"t"}` into the command 90 times; the 90th call must print a JSON
object with `hookSpecificOutput.additionalContext`. Delete `%TEMP%\claude-handoff\t.count` afterwards.
If the orchestrator counts tasks (for example a `--max-tasks` limit), make sure a handed-off task is continued,
not counted as a new one. Do not use the CLI's `--max-turns` for this: it stops a session without a note.

---

## Measure before and after

Claude Code stores transcripts in `%USERPROFILE%\.claude\projects\<project-dir>\*.jsonl` with per-call token
usage. Measure a project now and again after a week or two:

```
"<python.exe>" "%USERPROFILE%\.claude\tools\measure\cost_attribution.py" "%USERPROFILE%\.claude\projects\<project-dir>" cost.json
"<python.exe>" "%USERPROFILE%\.claude\tools\measure\session_length_cost.py" "%USERPROFILE%\.claude\projects\<project-dir>" by_length.json
```

Run them under the limiter for large projects. They print API-equivalent dollars (prices per model are in
`PRICES` at the top of `cost_attribution.py`; update them when prices change). Watch: share of cache reads,
share of spend in sessions over 100 calls, average context per call. On a subscription the dollars stand for
usage-limit consumption, not a bill.

---

## Why these rules (summary of the measurements)

Measured on a Windows 10 laptop (i7-1065G7) with Lua 5.4, LuaJIT, QuickJS-ng, Node 24, Deno 2.6, Python 3.14,
PowerShell 5.1 and 7.6, plus 390 programs written blind by Claude Haiku and Sonnet, plus 2,474 real Claude Code
sessions of one project.

**Language.** Python is the working language for scripts: richest standard library and the shortest code.
Claude wrote correct code in every language tested (100% of simple tasks, 95–100% of hard ones, checked on
~2,100 random inputs); a "rare" language costs 2–3x more code, not more errors. JavaScript is the second choice
(and the natural one when the host already embeds V8). PowerShell is for Windows administration only: a function
call costs ~45 µs (Python: ~0.04 µs) and it starts in 160–270 ms. Avoid LuaJIT for untrusted code (a count hook
cannot interrupt a JIT-compiled loop) and QuickJS for speed (slower than CPython).

**Environment knowledge matters more than language.** Every model failure was an environment trap: the module
name `qjs:std` (0/15 without it, 15/15 with it), Linux-only `threading.stack_size`, PowerShell's
case-insensitive variables and aliases shadowing functions. One line in the instructions removes each.

**Encoding is the most common real failure.** In 6,431 sessions, 23% of Python runs without a UTF-8 setting
returned unreadable output (0.9% with it); `tasklist`/`taskkill` output was unreadable in 58–67% of calls on
Russian-localized Windows.

**Startup costs.** `CreateProcess` floor ~5 ms; Python 36 ms by full path, 140 ms through the WindowsApps alias,
242 ms with `import numpy`; Node 55 ms; PowerShell 160–270 ms. Hooks that start `python` through the alias add
~0.2–0.3 s to every tool call.

**Where Claude Code money goes.** 65% cache reads (the conversation re-read on every call), 21% cache writes,
13% output. Average context per call grows from 57k tokens (sessions of 1–20 calls) to 280k (251+ calls);
cost per session from $0.50 to $25. Expensive inputs: whole-file reads of source files (12% of spend), `grep`
and `sed` dumps (13.5%), `git pull` without `-q` (5,400 tokens per call on average).

**Script-first.** The same log analysis: 118 turns / 317 s / $0.74 with tool calls versus 3 turns / 47 s / $0.077
with one script, identical answers. On 2 x 25 MB of test logs the tool-call variant could not finish the task;
the script variant did it in 20 s for $0.087.

**Isolation.** A Windows Job Object caps memory, enforces a hard timeout and blocks child processes at ~0
compute cost (~9 ms extra start for the wrapper). It does not restrict files or network.

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `No real python.exe found` | Only the WindowsApps alias, or no Python | Install Python, or pass `--python C:\path\python.exe` |
| `limiter stops an infinite loop: FAILED` | Limiter blocked by security software, or wrong path | Try the `jobrun.py` fallback (manual step 1); check antivirus logs |
| `jobrun.exe cannot start (WinError 216)` | Different CPU architecture (e.g. ARM64) | The installer switches to `jobrun.py` automatically |
| `SKIP settings.json: invalid JSON` | The file was already broken | Fix the JSON by hand, re-run the installer |
| Limiter still asks for permission | Claude Code spelled the path differently | Add a `Bash(<that spelling> *)` rule with the exact prefix it used |
| Rules seem ignored | Old session | Start a new Claude Code session; check `%USERPROFILE%\.claude\CLAUDE.md` has the BEGIN/END section |
| Non-Russian Windows | Code page is not 866 | Expected: the CP866 section is not installed; the UTF-8 rules still apply |

## Kit contents

```
SETUP.md                        this file
install.py                      installer (dry run, install, uninstall, verify)
templates/CLAUDE.section.md     rules section, with {{PYTHON}} {{JOBRUN}} {{OEM_CP}} {{OEM_SECTION}}
templates/CLAUDE.oem-cp866.md   extra rules for Russian-localized Windows
templates/SKILL.script-first.md the script-first skill
tools/jobrun.exe, jobrun.c      Job Object limiter (x64) and its source
tools/jobrun.py                 the same limiter in pure Python (ctypes), used when the exe cannot run
tools/handoff_hook.py           optional session handoff hook
tools/measure/*.py              cost measurement over Claude Code transcripts
examples/                       the configuration as installed on the machine where the kit was made
```
