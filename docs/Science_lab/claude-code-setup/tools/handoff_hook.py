"""PostToolUse hook: count tool calls per Claude Code session and, past a threshold, ask the model to hand off.

Long sessions are expensive: every API call re-reads the whole conversation, so a session past ~100 calls
costs several times more per call than a fresh one. This hook does not stop anything; it injects a note into
the model's context asking it to finish the current step, write a handoff note and end the session.

Configure in a project's .claude/settings.json (see SETUP.md, "Session handoff"):
  env CLAUDE_HANDOFF_AT      first reminder at this many tool calls (default 90)
  env CLAUDE_HANDOFF_EVERY   repeat the reminder every N calls after that (default 20)
  env CLAUDE_HANDOFF_TARGET  where the note goes, e.g. "the 'In progress' section of STATUS-P1.md"
                             (default: "the project's task-state file")
"""
import json
import os
import sys
import tempfile
from pathlib import Path


def main() -> int:
    try:
        data = json.loads(sys.stdin.buffer.read().decode("utf-8") or "{}")
    except ValueError:
        return 0
    sid = str(data.get("session_id") or "unknown")
    safe = "".join(ch for ch in sid if ch.isalnum() or ch in "-_")[:80] or "unknown"
    d = Path(tempfile.gettempdir()) / "claude-handoff"
    d.mkdir(exist_ok=True)
    f = d / f"{safe}.count"
    try:
        n = int(f.read_text()) + 1
    except (OSError, ValueError):
        n = 1
    f.write_text(str(n))
    first = int(os.environ.get("CLAUDE_HANDOFF_AT", "90"))
    every = max(1, int(os.environ.get("CLAUDE_HANDOFF_EVERY", "20")))
    if n < first or (n - first) % every:
        return 0
    target = os.environ.get("CLAUDE_HANDOFF_TARGET", "the project's task-state file")
    note = (f"This session has made {n} tool calls; every further call re-reads the whole conversation and "
            f"costs more. If the current task is not finished: complete the step you are on, then write a "
            f"handoff note in {target} (what is done, what is next, branch/worktree, how to verify), commit "
            f"work in progress, and end the session so a fresh session can continue from the note. "
            f"If the task is about to be finished, finish it instead.")
    sys.stdout.buffer.write(json.dumps({"hookSpecificOutput": {"hookEventName": "PostToolUse",
                                                               "additionalContext": note}}).encode("utf-8"))
    return 0


if __name__ == "__main__":
    sys.exit(main())
