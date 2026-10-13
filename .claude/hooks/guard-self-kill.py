#!/usr/bin/env python
"""PreToolUse guard: refuse shell commands that kill the manager hosting us.

A session started by Claude Session Manager runs as a child of
claude-manager.exe, and every session process is assigned to a Windows Job
Object created with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
(internal/proc/proc_windows.go). When the manager dies, the OS closes the job
handle and terminates *every* session and its whole process tree at once.

So a session that rebuilds the app and then kills the running instance to
restart it -- a perfectly reasonable-looking sequence -- silently destroys
itself and every other session running in parallel, with no panic, no
shutdown log, and no crash report. That happened on 2026-10-04 11:36:01
(`taskkill //PID 6588 //F`) and is the reason this guard exists.

The manager puts its own identity in the session environment
(CLAUDE_MANAGER_PID / CLAUDE_MANAGER_EXE, see internal/session/env.go), which
is what makes the check exact. When those are absent -- an ad-hoc session
started outside the manager -- the guard still refuses commands that name
claude-manager, and resolves numeric PIDs through the OS as a last resort.

Blocking is deliberately narrow, and anything unresolvable is allowed
through: a false block on every Bash call is far worse than a missed kill.
"""

import json
import os
import re
import subprocess
import sys

# Keys under which the various shell tools carry their command string.
_COMMAND_KEYS = ("command", "cmd", "script")

# A kill verb must be present before we spend anything on PID resolution;
# this keeps the guard at one cheap regex pass for the overwhelming majority
# of commands.
#
# The verb has to sit in command position: the lookbehind rejects anything
# glued to a preceding path or word character, so `guard-self-kill.py` and
# `src/killswitch.go` do not read as a kill, while `taskkill ...`,
# `cmd //c taskkill ...` and `Get-Process x | Stop-Process` all do. A verb
# quoted inside an echo is matched too — cheap, and it only costs a PID
# lookup that then finds nothing to block.
_KILL_VERB = re.compile(
    r"(?<![\w\-./\\])(taskkill|stop-process|pkill|killall|kill)\b"
    r"|(?<![\w\-./\\])wmic\b[^|;&]*\bdelete\b",
    re.IGNORECASE,
)

_TARGET_NAME = re.compile(r"claude[-_]manager", re.IGNORECASE)

# PID candidates: anything numeric the kill verb could plausibly be aimed at.
# `taskkill //PID 6588 //F`, `Stop-Process -Id 6588` and `kill -9 6588` all
# land here. Short numbers (signal numbers, /F-style flags) fall outside the
# {2,7} bound, and whatever is not actually the manager is rejected below.
_PID = re.compile(r"\b(\d{2,7})\b")
_MAX_PIDS = 8

_REFUSAL = """BLOCKED: this command would kill Claude Session Manager, which is hosting this session.

Every session runs inside the manager's Windows Job Object
(JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE). Killing the manager terminates this
session AND every other session running in parallel across all projects,
instantly and without a clean shutdown -- session logs are not written, and
every task in flight is cut mid-step.

Rebuilding the app is fine; restarting it from inside itself is not. Build,
then tell the user the new binary is ready and let them restart: tray "Quit",
then run-claude-manager.bat. Do not try to work around this check."""


def _manager_pids():
    """PIDs the manager claims as its own, from the session environment."""
    pids = set()
    raw = os.environ.get("CLAUDE_MANAGER_PID", "").strip()
    if raw.isdigit():
        pids.add(raw)
    return pids


def _targets_manager_name(command):
    if _TARGET_NAME.search(command):
        return True
    exe = os.environ.get("CLAUDE_MANAGER_EXE", "").strip()
    if exe:
        stem = os.path.splitext(os.path.basename(exe))[0]
        if stem and re.search(r"\b%s\b" % re.escape(stem), command, re.IGNORECASE):
            return True
    return False


def _resolve(pid):
    """Return the image name for pid, or "" when it cannot be determined."""
    try:
        if sys.platform == "win32":
            out = subprocess.run(
                ["tasklist", "/FI", "PID eq %s" % pid, "/NH", "/FO", "CSV"],
                capture_output=True,
                text=True,
                timeout=5,
            ).stdout
            # tasklist prints a localised "no tasks" notice rather than
            # failing when the PID is gone, so match on content, not status.
            return out if "," in out else ""
        with open("/proc/%s/comm" % pid) as fh:
            return fh.read()
    except Exception:
        return ""


def _refuse():
    sys.stderr.write(_REFUSAL)
    return 2


def main():
    try:
        payload = json.load(sys.stdin)
    except Exception:
        return 0

    tool_input = payload.get("tool_input") or {}
    command = ""
    for key in _COMMAND_KEYS:
        value = tool_input.get(key)
        if isinstance(value, str) and value.strip():
            command = value
            break
    if not command or not _KILL_VERB.search(command):
        return 0

    if _targets_manager_name(command):
        return _refuse()

    owned = _manager_pids()
    candidates = _PID.findall(command)[:_MAX_PIDS]
    for pid in candidates:
        if pid in owned:
            return _refuse()

    # No environment hint matched. Fall back to asking the OS what those PIDs
    # actually are -- this is what covers ad-hoc sessions the manager did not
    # start, which inherit none of its variables.
    for pid in candidates:
        if _TARGET_NAME.search(_resolve(pid)):
            return _refuse()

    return 0


if __name__ == "__main__":
    sys.exit(main())
