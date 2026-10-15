package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

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
- Full guide: read the cm://guide resource (also docs/mcp.md in the claude-manager repository). Ready-made scenarios are in prompts/list.`

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
			// Tools, resources, prompts: fixed sets, so no listChanged. The
			// logging capability is deprecated (diagnostics go to stderr).
			Capabilities: &mcp.ServerCapabilities{Tools: &mcp.ToolCapabilities{}, Resources: &mcp.ResourceCapabilities{}, Prompts: &mcp.PromptCapabilities{}},
		},
	)
	srv.AddReceivingMiddleware(s.levelMiddleware)
	s.addResources(srv)
	s.addPrompts(srv)
	for _, def := range control.MCPTools {
		if !s.effectiveLevel().Allows(def.Level) {
			continue // not listed; a call by name is answered by levelMiddleware
		}
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
				ctx = withClient(ctx, req.Session)
				args := req.Params.Arguments
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				if control.IsLongRunning(name) {
					defer s.reportProgress(ctx, req, name)()
					if !control.IsWaitTool(name) {
						var cancel context.CancelFunc
						ctx, cancel = context.WithTimeout(ctx, control.LongToolTimeout)
						defer cancel()
					}
				}
				result, err := s.callToolCtx(ctx, name, args)
				if errors.Is(err, context.DeadlineExceeded) {
					err = fmt.Errorf("%s still running after %s; it keeps running in the app, so check the outcome there (or in the matching get_* tool) instead of repeating the call", name, control.LongToolTimeout)
				}
				var out any
				var summary string
				if err == nil {
					out, summary, err = control.ShapeResult(name, args, result)
				}
				if err != nil {
					// Tool failures are results, not JSON-RPC errors, so the model sees them.
					return &mcp.CallToolResult{
						IsError: true,
						Content: []mcp.Content{&mcp.TextContent{Text: control.ExplainError(name, err, func(tool string) []any {
							res, lerr := s.callToolCtx(ctx, tool, json.RawMessage(`{"limit":500}`))
							items, _ := res.([]any)
							_ = lerr // context is best effort: the plain error still goes out
							return items
						})}},
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

// defaultProgressInterval is how often a long call tells the client it is alive.
const defaultProgressInterval = 10 * time.Second

// reportProgress sends notifications/progress for a tools/call that carries a
// progressToken, every progress interval until the returned stop func runs.
// progress is the elapsed seconds: the total is unknown, the value only has
// to grow. Without a token it does nothing.
func (s *mcpServer) reportProgress(ctx context.Context, req *mcp.CallToolRequest, name string) (stop func()) {
	token := req.Params.GetProgressToken()
	if token == nil || req.Session == nil {
		return func() {}
	}
	interval := s.progressInterval
	if interval <= 0 {
		interval = defaultProgressInterval
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		start := time.Now()
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-tick.C:
				elapsed := time.Since(start).Round(time.Second)
				_ = req.Session.NotifyProgress(ctx, &mcp.ProgressNotificationParams{
					ProgressToken: token,
					Progress:      elapsed.Seconds(),
					Message:       fmt.Sprintf("%s: still running, %s elapsed", name, elapsed),
				})
			}
		}
	}()
	return func() { close(done); <-finished }
}

// withClient records the MCP clientInfo for the control-plane audit log.
func withClient(ctx context.Context, ss *mcp.ServerSession) context.Context {
	if ss == nil {
		return ctx
	}
	if p := ss.InitializeParams(); p != nil && p.ClientInfo != nil {
		return context.WithValue(ctx, clientKey{}, p.ClientInfo.Name+" "+p.ClientInfo.Version)
	}
	return ctx
}

// levelMiddleware answers a tools/call for a tool above the configured level
// with an actionable tool error instead of the SDK's bare "unknown tool".
func (s *mcpServer) levelMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method == "tools/call" {
			if call, ok := req.(*mcp.CallToolRequest); ok && call.Params != nil {
				if err := s.checkLevel(call.Params.Name); err != nil {
					return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
				}
			}
		}
		return next(ctx, method, req)
	}
}

// serve runs the MCP server over in/out until the client closes in or ctx ends.
func (s *mcpServer) serve(ctx context.Context, in io.ReadCloser, out io.WriteCloser) error {
	return s.newSDKServer().Run(ctx, &mcp.IOTransport{Reader: in, Writer: out})
}
