#!/usr/bin/env bash
# Linux counterpart of run-claude-manager.bat.
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

if [ ! -x "$EXE" ]; then
    echo "claude-manager not found, building..."
    (cd "$SCRIPT_DIR" && CGO_ENABLED=1 wails build -tags webkit2_41)
    if [ ! -x "$EXE" ]; then
        echo "Build failed: claude-manager still missing." >&2
        exit 1
    fi
fi

echo "---- $(date) ----" >> "$LOGDIR/crash.log"
exec "$EXE" 2>> "$LOGDIR/crash.log"
