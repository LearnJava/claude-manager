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
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"

	"claude-manager/internal/control"
)

const (
	mcpProtocolVersion = "2024-11-05"
	mcpServerName      = "cm-mcp"
	mcpServerVersion   = "1.0.0"
)

// mcpRequest is a JSON-RPC 2.0 request or notification from the MCP client.
type mcpRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

// mcpResponse is a JSON-RPC 2.0 response written to the MCP client.
type mcpResponse struct {
	JSONRPC string    `json:"jsonrpc"`
	ID      any       `json:"id,omitempty"`
	Result  any       `json:"result,omitempty"`
	Error   *mcpError `json:"error,omitempty"`
}

type mcpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

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
	srv := newMCPServer()
	if err := srv.run(os.Stdin, os.Stdout); err != nil && err != io.EOF {
		fmt.Fprintln(os.Stderr, "cm-mcp:", err)
		os.Exit(1)
	}
}

// run reads newline-delimited JSON-RPC requests from in, processes each one,
// and writes JSON-RPC responses to out. It returns when in reaches EOF.
func (s *mcpServer) run(in io.Reader, out io.Writer) error {
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 1<<20), 1<<20) // 1 MB max per line
	enc := json.NewEncoder(out)

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var req mcpRequest
		if err := json.Unmarshal(line, &req); err != nil {
			_ = enc.Encode(mcpResponse{
				JSONRPC: "2.0",
				Error:   &mcpError{Code: -32700, Message: "parse error: " + err.Error()},
			})
			continue
		}
		// Notifications have no id — no response required.
		if req.ID == nil {
			continue
		}
		_ = enc.Encode(s.handle(req))
	}
	return scanner.Err()
}

// handle dispatches one JSON-RPC request and returns the response.
func (s *mcpServer) handle(req mcpRequest) mcpResponse {
	switch req.Method {
	case "initialize":
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": mcpProtocolVersion,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": mcpServerName, "version": mcpServerVersion},
			},
		}

	case "tools/list":
		tools := make([]map[string]any, len(control.MCPTools))
		for i, t := range control.MCPTools {
			tools[i] = map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"inputSchema": t.InputSchema,
			}
		}
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  map[string]any{"tools": tools},
		}

	case "tools/call":
		var params struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &params); err != nil {
			return errResp(req.ID, -32602, "invalid params: "+err.Error())
		}
		if params.Arguments == nil {
			params.Arguments = json.RawMessage(`{}`)
		}
		result, callErr := s.callTool(params.Name, params.Arguments)
		if callErr != nil {
			return mcpResponse{
				JSONRPC: "2.0",
				ID:      req.ID,
				Result: map[string]any{
					"content": []map[string]any{{"type": "text", "text": callErr.Error()}},
					"isError": true,
				},
			}
		}
		text, _ := json.MarshalIndent(result, "", "  ")
		return mcpResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"content": []map[string]any{{"type": "text", "text": string(text)}},
			},
		}

	default:
		return errResp(req.ID, -32601, "method not found: "+req.Method)
	}
}

// callTool translates a tool invocation into a control-plane HTTP call and
// returns the parsed result.
func (s *mcpServer) callTool(name string, args json.RawMessage) (any, error) {
	endpoint, body, err := control.TranslateTool(name, args)
	if err != nil {
		return nil, err
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, s.baseURL+endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CM-Token", s.token)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("control-plane: %w", err)
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

func errResp(id any, code int, msg string) mcpResponse {
	return mcpResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &mcpError{Code: code, Message: msg},
	}
}
