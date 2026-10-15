package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"claude-manager/internal/control"
)

// serverInstructions is returned in the initialize / server/discover result.
// It is the agent's first read, so it states the model, not the tool list.
const serverInstructions = `Claude Manager runs several Claude Code sessions per project in parallel; these tools drive that desktop app.
- A session is addressed by project and session name; as one string it is "project/session" (e.g. "lumen/P1").
- Lifecycle: start_session -> running -> waiting (idle, ready for send_message) or blocked on a permission -> stopped / crashed / error.
- Permission requests block a session until approve_permission or deny_permission answers them; get_pending_permissions lists them.
- Do not poll in a loop. Block with wait_for_status or wait_for_event (they take timeout_ms), then act on the result.
- Keep output small: get_session_logs takes a tail, get_sessions returns summaries.
- Tool errors come back with isError=true and a hint; a "control-plane unreachable" error means the Claude Manager app is not running - ask the user to start it.
- Full guide: docs/mcp.md in the claude-manager repository (the cm://guide resource will carry it once resources are added).`

// newSDKServer builds the protocol layer. The SDK negotiates the protocol
// revision (legacy initialize and 2026-07-28 _meta/server/discover), handles
// ping and cancellation; only the tool set and its dispatch are ours.
func (s *mcpServer) newSDKServer() *mcp.Server {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	srv := mcp.NewServer(
		&mcp.Implementation{Name: mcpServerName, Title: mcpServerTitle, Version: version},
		&mcp.ServerOptions{
			Instructions: serverInstructions,
			Logger:       logger,
			// Tools only: the set is fixed for the process lifetime, and the
			// logging capability is deprecated (diagnostics go to stderr).
			Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}},
		},
	)
	for _, def := range control.MCPTools {
		name := def.Name
		ann := def.Annotations
		srv.AddTool(
			&mcp.Tool{
				Name:         name,
				Title:        def.Title,
				Description:  def.Description,
				InputSchema:  def.InputSchema,
				OutputSchema: def.OutputSchema,
				Annotations: &mcp.ToolAnnotations{
					Title:           def.Title,
					ReadOnlyHint:    ann.ReadOnly,
					DestructiveHint: &ann.Destructive,
					IdempotentHint:  ann.Idempotent,
					OpenWorldHint:   &ann.OpenWorld,
				},
				Meta: mcp.Meta{"cm/accessLevel": string(def.Level)},
			},
			func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				args := req.Params.Arguments
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				result, err := s.callToolCtx(ctx, name, args)
				var out any
				var summary string
				if err == nil {
					out, summary, err = control.ShapeResult(name, args, result)
				}
				if err != nil {
					// Tool failures are results, not JSON-RPC errors, so the model sees them.
					return &mcp.CallToolResult{
						IsError: true,
						Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
					}, nil
				}
				return &mcp.CallToolResult{
					StructuredContent: out,
					Content:           []mcp.Content{&mcp.TextContent{Text: summary}},
				}, nil
			},
		)
	}
	return srv
}

// serve runs the MCP server over in/out until the client closes in or ctx ends.
func (s *mcpServer) serve(ctx context.Context, in io.ReadCloser, out io.WriteCloser) error {
	return s.newSDKServer().Run(ctx, &mcp.IOTransport{Reader: in, Writer: out})
}
