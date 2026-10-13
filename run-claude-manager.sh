#!/usr/bin/env bash
# Linux counterpart of run-claude-manager.bat: captures raw stderr and
# restarts the app if it dies without meaning to (see the loop below).
#
# Wails builds the app as a GUI binary with no console, so anything the Go
# runtime writes directly to the OS-level stderr handle -- an unrecovered
# panic, or a fatal runtime error such as a concurrent map access or stack
# overflow, none of which go through app.log's slog logger -- normally
# vanishes with zero trace. Redirecting stderr here is what would catch it
# next time.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXE="$SCRIPT_DIR/build/bin/claude-manager"
LOGDIR="$HOME/.claude-manager/logs"
mkdir -p "$LOGDIR"

if ! command -v wails >/dev/null 2>&1; then
    export PATH="$PATH:$(go env GOPATH)/bin"
fi

# Auto-update on every start: fetch origin, fast-forward if it is ahead, and
# rebuild when the checked-out commit differs from the one the binary was built
# from (recorded in STAMP). Skipped outside a git checkout or when offline.
# Delete STAMP to force a rebuild (uncommitted local edits are not detected).
STAMP="$SCRIPT_DIR/build/bin/claude-manager.commit"
if git -C "$SCRIPT_DIR" rev-parse --git-dir >/dev/null 2>&1; then
    git -C "$SCRIPT_DIR" fetch --quiet origin >/dev/null 2>&1 || true
    behind=$(git -C "$SCRIPT_DIR" rev-list --count 'HEAD..@{u}' 2>/dev/null || echo 0)
    if [ "$behind" -gt 0 ]; then
        echo "Updating: $behind new commit(s) on origin..."
        git -C "$SCRIPT_DIR" pull --ff-only --autostash             || echo "WARNING: git pull failed, keeping the local copy." >&2
    fi
    head_rev=$(git -C "$SCRIPT_DIR" rev-parse HEAD)
    built=$(cat "$STAMP" 2>/dev/null || true)
    if [ ! -x "$EXE" ] || [ "$head_rev" != "$built" ]; then
        echo "Building claude-manager at $head_rev..."
        if (cd "$SCRIPT_DIR" && CGO_ENABLED=1 wails build -tags webkit2_41); then
            echo "$head_rev" > "$STAMP"
        else
            echo "WARNING: build failed." >&2
        fi
    fi
elif [ ! -x "$EXE" ]; then
    echo "claude-manager not found, building..."
    (cd "$SCRIPT_DIR" && CGO_ENABLED=1 wails build -tags webkit2_41)
fi
if [ ! -x "$EXE" ]; then
    echo "Build failed: claude-manager still missing." >&2
    exit 1
fi

# Relaunch loop. Sessions are children of the manager and die with it, so an
# unplanned exit should bring the app straight back and let crash recovery
# resume them. A deliberate quit is recognised by the marker app.go's
# shutdown writes on its way out (App.markCleanExit) — present means stop.
MARKER="$HOME/.claude-manager/state/clean-exit"
MAX_RESTARTS=10
HEALTHY_SECONDS=60
restarts=0

while true; do
    rm -f "$MARKER"
    started=$SECONDS

    echo "---- $(date) ----" >> "$LOGDIR/crash.log"
    # Not `exec`: the loop has to outlive the app to be able to restart it.
    # A non-zero exit is expected here (crash, kill), so suspend errexit.
    set +e
    "$EXE" 2>> "$LOGDIR/crash.log"
    set -e

    if [ -f "$MARKER" ]; then
        echo "---- $(date) clean exit ----" >> "$LOGDIR/crash.log"
        exit 0
    fi

    uptime=$((SECONDS - started))
    # A run long enough to be a real session means this is an isolated
    # failure rather than a loop, so forgive the earlier attempts.
    if [ "$uptime" -ge "$HEALTHY_SECONDS" ]; then
        restarts=0
    fi

    restarts=$((restarts + 1))
    if [ "$restarts" -gt "$MAX_RESTARTS" ]; then
        echo "---- $(date) gave up after $MAX_RESTARTS failed restarts ----" >> "$LOGDIR/crash.log"
        echo "claude-manager keeps exiting unexpectedly ($MAX_RESTARTS times). Not restarting again." >&2
        echo "See $LOGDIR/crash.log" >&2
        exit 1
    fi

    echo "---- $(date) unexpected exit after ${uptime}s, restart $restarts of $MAX_RESTARTS ----" >> "$LOGDIR/crash.log"
    # Breathing room so a failure that is instant does not spin the CPU.
    sleep 5
done
