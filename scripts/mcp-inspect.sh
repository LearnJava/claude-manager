#!/usr/bin/env bash
# Opt-in check of cm-mcp with the official MCP Inspector (CLI mode).
# Manual run only: needs Node/npx and network access for the first download.
#
#   scripts/mcp-inspect.sh            # list tools, resources, prompts
#   scripts/mcp-inspect.sh <method> [extra inspector args]
#                                     # e.g. tools/call --tool-name get_sessions
#
# The running Claude Manager app is picked up through ~/.claude-manager/control.json
# (or CM_CONTROL_ADDR / CM_CONTROL_TOKEN); without it, tools/list and prompts/list
# still work, tools/call reports "control-plane unreachable".
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
exe="$(mktemp -d)/cm-mcp"
(cd "$root" && go build -o "$exe" ./cmd/cm-mcp)
inspect() { npx -y @modelcontextprotocol/inspector --cli "$exe" --method "$@"; }
if [ $# -gt 0 ]; then
  inspect "$@"
else
  for m in tools/list resources/list resources/templates/list prompts/list; do
    echo "== $m"
    inspect "$m"
  done
fi
