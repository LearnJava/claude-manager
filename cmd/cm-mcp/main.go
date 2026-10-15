// cm-mcp is a stdio MCP server that proxies into the claude-manager control-plane.
//
// Registration in Claude Code:
//
//	claude mcp add cm -- cm-mcp
//
// Address and token are discovered from ~/.claude-manager/control.json, which
// the running app writes on startup; a GUI build has no terminal to print the
// generated token to, so requiring the user to export it would mean the tools
// only work in the `wails dev` flow. Environment variables override the file:
//
//	CM_CONTROL_ADDR  – control-plane base URL (default: the endpoint file, else
//	                   http://127.0.0.1:7333)
//	CM_CONTROL_TOKEN – authentication token (default: the endpoint file)
//	CM_MCP_LEVEL     – access level: read, operate (default) or admin; --level
//	                   overrides it. Tools above the level are not listed.
//	CM_MCP_ALLOW_SELF – "1": the agent may stop/restart/clear its own session
//	                   (off by default; --allow-self does the same)
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"

	"claude-manager/internal/control"
)

const (
	mcpServerName  = "cm-mcp"
	mcpServerTitle = "Claude Manager"
)

// version is stamped at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

// mcpServer is the stdio MCP proxy server.
type mcpServer struct {
	baseURL string
	token   string
	client  *http.Client

	// level is the access tier this process runs at (MCP-05). Empty means
	// control.DefaultLevel, never admin.
	level control.AccessLevel
	// callerSession is the "{project}/{session}" id of the session hosting this
	// process (CM_SESSION_ID, set by the manager); allowSelf lifts the
	// self-protection for it.
	callerSession string
	allowSelf     bool
}

// effectiveLevel is level with the safe default applied.
func (s *mcpServer) effectiveLevel() control.AccessLevel {
	if s.level == "" {
		return control.DefaultLevel
	}
	return s.level
}

// checkLevel refuses a tool above the configured level, with the way to enable it.
func (s *mcpServer) checkLevel(name string) error {
	t := control.FindTool(name)
	if t == nil || s.effectiveLevel().Allows(t.Level) {
		return nil
	}
	return fmt.Errorf("tool %s %s", name, control.LevelHint(t.Level))
}

type clientKey struct{}

func newMCPServer() (*mcpServer, error) {
	levelFlag := flag.String("level", "", "access level: read, operate (default) or admin (env CM_MCP_LEVEL)")
	allowSelf := flag.Bool("allow-self", false, "let the agent stop/restart/clear its own session (env CM_MCP_ALLOW_SELF=1)")
	flag.Parse()
	levelStr := *levelFlag
	if levelStr == "" {
		levelStr = os.Getenv("CM_MCP_LEVEL")
	}
	level, err := control.ParseLevel(levelStr)
	if err != nil {
		return nil, err
	}
	addr := os.Getenv("CM_CONTROL_ADDR")
	token := os.Getenv("CM_CONTROL_TOKEN")
	if addr == "" || token == "" {
		// A missing or unreadable endpoint file is not fatal: fall through to
		// the defaults and let the first RPC report the real failure.
		if ep, err := control.LoadEndpoint(); err == nil && ep != nil {
			if addr == "" {
				addr = ep.Addr
			}
			if token == "" {
				token = ep.Token
			}
		}
	}
	if addr == "" {
		addr = "http://127.0.0.1:7333"
	}
	return &mcpServer{
		level:         level,
		callerSession: os.Getenv("CM_SESSION_ID"),
		allowSelf:     *allowSelf || os.Getenv("CM_MCP_ALLOW_SELF") == "1",
		baseURL:       addr,
		token:         token,
		// No timeout: wait_for_* calls can block for extended durations; the
		// control-plane /wait endpoint enforces its own timeout_ms.
		client: &http.Client{},
	}, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// stdout carries the protocol only; everything else goes to stderr.
	srv, err := newMCPServer()
	if err != nil {
		fmt.Fprintln(os.Stderr, "cm-mcp:", err)
		os.Exit(2)
	}
	if err := srv.serve(ctx, os.Stdin, os.Stdout); err != nil && err != io.EOF {
		fmt.Fprintln(os.Stderr, "cm-mcp:", err)
		os.Exit(1)
	}
}

// callTool translates a tool invocation into a control-plane HTTP call and
// returns the parsed result.
func (s *mcpServer) callTool(name string, args json.RawMessage) (any, error) {
	return s.callToolCtx(context.Background(), name, args)
}

// callToolCtx is callTool bound to ctx: cancelling the MCP request aborts the
// in-flight control-plane call (notably a long wait_for_*).
func (s *mcpServer) callToolCtx(ctx context.Context, name string, args json.RawMessage) (any, error) {
	if err := s.checkLevel(name); err != nil {
		return nil, err
	}
	endpoint, body, err := control.TranslateTool(name, args)
	if err != nil {
		return nil, err
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CM-Token", s.token)
	req.Header.Set(control.HeaderLevel, string(s.effectiveLevel()))
	if c, _ := ctx.Value(clientKey{}).(string); c != "" {
		req.Header.Set(control.HeaderClient, c)
	}
	if s.callerSession != "" {
		req.Header.Set(control.HeaderCaller, s.callerSession)
	}
	if s.allowSelf {
		req.Header.Set(control.HeaderAllowSelf, "1")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("control-plane unreachable at %s (%v): start Claude Manager and retry; "+
			"the address and token come from ~/.claude-manager/control.json, which the running app writes", s.baseURL, err)
	}
	defer resp.Body.Close()

	if endpoint == "/rpc" {
		return s.decodeRPCResponse(resp)
	}
	return s.decodeWaitResponse(resp)
}

func (s *mcpServer) decodeRPCResponse(resp *http.Response) (any, error) {
	var rpcResp struct {
		Result any `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode rpc response: %w", err)
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	return rpcResp.Result, nil
}

func (s *mcpServer) decodeWaitResponse(resp *http.Response) (any, error) {
	if resp.StatusCode == http.StatusRequestTimeout {
		return nil, fmt.Errorf("wait timeout")
	}
	var result any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode wait response: %w", err)
	}
	return result, nil
}
