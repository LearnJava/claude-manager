// Package docs embeds the documents the binaries serve at runtime.
package docs

import _ "embed"

// MCPGuide is docs/mcp.md: the agent-facing guide cm-mcp serves as cm://guide.
// Embedding the file keeps one copy of the guide.
//
//go:embed mcp.md
var MCPGuide string
