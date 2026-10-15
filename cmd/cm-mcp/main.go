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
package main

import (
	"bytes"
	"context"
	"encoding/json"
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
}

func newMCPServer() *mcpServer {
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
		baseURL: addr,
		token:   token,
		// No timeout: wait_for_* calls can block for extended durations; the
		// control-plane /wait endpoint enforces its own timeout_ms.
		client: &http.Client{},
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	// stdout carries the protocol only; everything else goes to stderr.
	if err := newMCPServer().serve(ctx, os.Stdin, os.Stdout); err != nil && err != io.EOF {
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
