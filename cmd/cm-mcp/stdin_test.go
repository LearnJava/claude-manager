package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"
)

// nextErr reads lines until an error response and returns id and code.
func (p *rpcPeer) nextErr() (id any, code float64) {
	p.t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case m, ok := <-p.lines:
			if !ok {
				p.t.Fatal("server closed stdout")
			}
			if e, _ := m["error"].(map[string]any); e != nil {
				return m["id"], e["code"].(float64)
			}
		case <-deadline:
			p.t.Fatal("timeout waiting for an error response")
		}
	}
}

func TestStdin_BadLinesDoNotEndTheSession(t *testing.T) {
	p := startPeer(t, newTestMCPServer(t, &stubCP{}))
	p.handshake("2025-06-18")
	cases := []struct {
		line   string
		wantID any
		code   float64
	}{
		{`{not json`, nil, -32700},
		{`42`, nil, -32600},
		{`{"id":7,"method":"ping"}`, float64(7), -32600},
		{`[{"jsonrpc":"2.0","id":8,"method":"ping"}]`, nil, -32600},
		{`{"jsonrpc":"2.0","id":null,"method":"ping"}`, nil, -32600},
	}
	for _, c := range cases {
		p.send(c.line)
		id, code := p.nextErr()
		if id != c.wantID || code != c.code {
			t.Fatalf("%s: want id %v code %v, got id %v code %v", c.line, c.wantID, c.code, id, code)
		}
	}
	p.send(`{"jsonrpc":"2.0","id":99,"method":"ping"}`)
	p.result(99)
}

func TestStdin_EOFAfterInitializeStillAnswers(t *testing.T) {
	srv := newTestMCPServer(t, &stubCP{})
	inR, inW := io.Pipe()
	var out bytes.Buffer
	done := make(chan error, 1)
	go func() { done <- srv.serve(context.Background(), inR, nopWriteCloser{&out}) }()
	_, _ = io.WriteString(inW, strings.Replace(initLine, "%s", "2025-06-18", 1)+"\n")
	inW.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after EOF")
	}
	var m map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(out.Bytes()), &m); err != nil || m["id"] != float64(1) || m["result"] == nil {
		t.Fatalf("initialize was not answered before exit: %q (%v)", out.String(), err)
	}
}

type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

func TestStdin_CancelledRequestGetsNoResponse(t *testing.T) {
	g := newStdioGate(&bytes.Buffer{})
	var out bytes.Buffer
	g.out = &out
	if !g.admit([]byte(`{"jsonrpc":"2.0","id":5,"method":"tools/call"}`)) ||
		!g.admit([]byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":5}}`)) {
		t.Fatal("well-formed messages must be forwarded")
	}
	_, _ = g.Write([]byte(`{"jsonrpc":"2.0","id":5,"result":{"isError":true}}` + "\n"))
	if out.Len() != 0 {
		t.Fatalf("response to a cancelled request was written: %q", out.String())
	}
	g.waitDrained() // nothing pending any more: returns at once
}

func TestControlPlane401MentionsToken(t *testing.T) {
	stub := &stubCP{statusCode: 401, response: "unauthorized"}
	srv := newTestMCPServer(t, stub)
	_, err := srv.callTool("get_sessions", json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "token") || strings.Contains(err.Error(), "invalid character") {
		t.Fatalf("want a token error, got %v", err)
	}
	stub.statusCode, stub.response = 500, "boom"
	if _, err = srv.callTool("get_sessions", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "500") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("want status and body, got %v", err)
	}
}

func TestPromptMissingArgumentIsInvalidParams(t *testing.T) {
	p := startPeer(t, newTestMCPServer(t, &stubCP{}))
	p.handshake("2025-06-18")
	p.send(`{"jsonrpc":"2.0","id":4,"method":"prompts/get","params":{"name":"run_task","arguments":{"session_id":"lumen/P1"}}}`)
	if code := p.errorCode(4); code != -32602 {
		t.Fatalf("want -32602, got %v", code)
	}
}
