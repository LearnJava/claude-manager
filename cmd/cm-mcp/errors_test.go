package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestProtocol_NotFoundErrorIsActionable: a "session not found" from the app
// comes back naming the sessions that do exist in that project.
func TestProtocol_NotFoundErrorIsActionable(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &req)
		switch req["method"] {
		case "GetAllSessions":
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"result":[{"id":"lumen/P1"},{"id":"lumen/P2"}]}`)
		default:
			_, _ = io.WriteString(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"session \"lumen/P9\" not found"}}`)
		}
	}))
	t.Cleanup(ts.Close)
	srv := &mcpServer{baseURL: ts.URL, token: "t", client: ts.Client()}

	p := startPeer(t, srv)
	p.handshake("2025-11-25")
	p.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"stop_session","arguments":{"session_id":"lumen/P9"}}}`)
	r := p.result(2)
	if r["isError"] != true {
		t.Fatalf("want isError, got %v", r)
	}
	c, _ := r["content"].([]any)
	txt, _ := c[0].(map[string]any)["text"].(string)
	for _, want := range []string{"lumen/P1, lumen/P2", "get_sessions"} {
		if !strings.Contains(txt, want) {
			t.Errorf("error text %q lacks %q", txt, want)
		}
	}
}
