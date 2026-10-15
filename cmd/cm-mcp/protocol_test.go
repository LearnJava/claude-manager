package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const initLine = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"%s","capabilities":{},"clientInfo":{"name":"t","version":"0"}}}`

// rpcPeer drives the SDK server over a pipe with raw JSON-RPC lines, the way a
// stdio client does, so the tests cover both protocol eras without a client SDK.
type rpcPeer struct {
	t     *testing.T
	w     io.WriteCloser
	lines chan map[string]any
}

func startPeer(t *testing.T, srv *mcpServer) *rpcPeer {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	p := &rpcPeer{t: t, w: inW, lines: make(chan map[string]any, 16)}
	go func() { _ = srv.serve(ctx, inR, outW); outW.Close() }()
	go func() {
		sc := bufio.NewScanner(outR)
		sc.Buffer(make([]byte, 1<<20), 1<<20)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("stdout is not protocol JSON: %q", sc.Text())
				continue
			}
			p.lines <- m
		}
		close(p.lines)
	}()
	t.Cleanup(func() { cancel(); inW.Close() })
	return p
}

func (p *rpcPeer) send(line string) {
	p.t.Helper()
	if _, err := io.WriteString(p.w, line+"\n"); err != nil {
		p.t.Fatalf("write: %v", err)
	}
}

// result waits for the response with the given id and returns its result.
func (p *rpcPeer) result(id float64) map[string]any {
	p.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m, ok := <-p.lines:
			if !ok {
				p.t.Fatal("server closed stdout")
			}
			if m["id"] != id {
				continue
			}
			if m["error"] != nil {
				p.t.Fatalf("unexpected error for id %v: %v", id, m["error"])
			}
			r, _ := m["result"].(map[string]any)
			return r
		case <-deadline:
			p.t.Fatalf("timeout waiting for response %v", id)
		}
	}
}

func (p *rpcPeer) handshake(ver string) {
	p.t.Helper()
	p.send(strings.Replace(initLine, "%s", ver, 1))
	p.result(1)
	p.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
}

func callLine(id int, tool string) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": tool, "arguments": map[string]any{}}})
	return string(b)
}

func TestProtocol_LegacyHandshake(t *testing.T) {
	for _, ver := range []string{"2025-11-25", "2024-11-05"} {
		t.Run(ver, func(t *testing.T) {
			p := startPeer(t, newTestMCPServer(t, &stubCP{}))
			p.send(strings.Replace(initLine, "%s", ver, 1))
			r := p.result(1)
			if r["protocolVersion"] != ver {
				t.Fatalf("protocolVersion: want %s, got %v", ver, r["protocolVersion"])
			}
			info, _ := r["serverInfo"].(map[string]any)
			if info["name"] != mcpServerName || info["title"] != mcpServerTitle || info["version"] != version {
				t.Fatalf("serverInfo: %v", info)
			}
			ins, _ := r["instructions"].(string)
			if !strings.Contains(ins, "wait_for_status") || !strings.Contains(ins, "project/session") {
				t.Fatalf("instructions missing guidance: %q", ins)
			}
			p.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
			p.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
			p.result(2)
			p.send(`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
			if tools, _ := p.result(3)["tools"].([]any); len(tools) == 0 {
				t.Fatal("empty tools/list")
			}
		})
	}
}

func TestProtocol_UnknownVersionGetsSupportedOne(t *testing.T) {
	p := startPeer(t, newTestMCPServer(t, &stubCP{}))
	p.send(strings.Replace(initLine, "%s", "1999-01-01", 1))
	v, _ := p.result(1)["protocolVersion"].(string)
	if v == "" || v == "1999-01-01" {
		t.Fatalf("server must answer with a version it supports, got %q", v)
	}
}

func TestProtocol_ModernDiscoverWithoutHandshake(t *testing.T) {
	p := startPeer(t, newTestMCPServer(t, &stubCP{}))
	meta := `"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientCapabilities":{},"io.modelcontextprotocol/clientInfo":{"name":"t","version":"0"}}`
	p.send(`{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{` + meta + `}}`)
	r := p.result(1)
	m, _ := r["_meta"].(map[string]any)
	info, _ := m["io.modelcontextprotocol/serverInfo"].(map[string]any)
	if r["instructions"] == nil || info["name"] != mcpServerName {
		t.Fatalf("discover result lacks identity: %v", r)
	}
	caps, _ := r["capabilities"].(map[string]any)
	if caps["tools"] == nil || caps["logging"] != nil {
		t.Fatalf("capabilities: want tools only, got %v", caps)
	}
	p.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{` + meta + `}}`)
	if tools, _ := p.result(2)["tools"].([]any); len(tools) == 0 {
		t.Fatal("empty tools/list in modern mode")
	}
}

func TestProtocol_ToolCall(t *testing.T) {
	stub := &stubCP{}
	p := startPeer(t, newTestMCPServer(t, stub))
	p.handshake("2025-11-25")
	p.send(callLine(2, "get_sessions"))
	r := p.result(2)
	if stub.lastBody["method"] != "GetAllSessions" {
		t.Fatalf("method: %v", stub.lastBody["method"])
	}
	if c, _ := r["content"].([]any); len(c) == 0 || r["isError"] == true {
		t.Fatalf("bad result: %v", r)
	}
}

func TestProtocol_ToolErrorIsResultNotRPCError(t *testing.T) {
	stub := &stubCP{response: `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"session not found"}}`}
	p := startPeer(t, newTestMCPServer(t, stub))
	p.handshake("2025-11-25")
	p.send(callLine(2, "get_sessions"))
	if r := p.result(2); r["isError"] != true {
		t.Fatalf("isError: want true, got %v", r)
	}
}

func TestProtocol_ControlPlaneUnavailable(t *testing.T) {
	srv := newTestMCPServer(t, &stubCP{})
	srv.baseURL = "http://127.0.0.1:1" // nothing listens: the app is not running
	p := startPeer(t, srv)
	p.handshake("2025-11-25")
	p.send(callLine(2, "get_sessions"))
	r := p.result(2)
	if r["isError"] != true {
		t.Fatalf("want isError, got %v", r)
	}
	c, _ := r["content"].([]any)
	txt, _ := c[0].(map[string]any)["text"].(string)
	if !strings.Contains(txt, "start Claude Manager") {
		t.Fatalf("error lacks the hint: %q", txt)
	}
	p.send(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	p.result(3) // the server survived
}

func TestProtocol_CancelAbortsInFlightCall(t *testing.T) {
	hit := make(chan struct{}, 1)
	released := make(chan struct{})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // the server notices a dropped client only after the body is read
		hit <- struct{}{}
		<-r.Context().Done() // returns only when the client request is cancelled
		close(released)
	}))
	t.Cleanup(ts.Close)
	srv := newTestMCPServer(t, &stubCP{})
	srv.baseURL = ts.URL
	srv.client = ts.Client()
	p := startPeer(t, srv)
	p.handshake("2025-11-25")
	p.send(callLine(2, "get_sessions"))
	<-hit
	p.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":2,"reason":"test"}}`)
	select {
	case <-released:
	case <-time.After(10 * time.Second):
		t.Fatal("cancellation did not abort the control-plane call")
	}
	p.send(`{"jsonrpc":"2.0","id":3,"method":"ping"}`)
	p.result(3)
}
