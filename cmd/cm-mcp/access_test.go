package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claude-manager/internal/control"
)

func listedTools(t *testing.T, srv *mcpServer) map[string]bool {
	t.Helper()
	p := startPeer(t, srv)
	p.handshake("2025-11-25")
	p.send(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	out := map[string]bool{}
	tools, _ := p.result(2)["tools"].([]any)
	for _, x := range tools {
		out[x.(map[string]any)["name"].(string)] = true
	}
	return out
}

func TestLevelFiltersToolsList(t *testing.T) {
	for _, level := range []control.AccessLevel{control.LevelRead, control.LevelOperate, control.LevelAdmin, ""} {
		srv := newTestMCPServer(t, &stubCP{})
		srv.level = level
		got := listedTools(t, srv)
		eff := srv.effectiveLevel()
		for _, def := range control.MCPTools {
			if want := eff.Allows(def.Level); got[def.Name] != want {
				t.Errorf("level %q: tool %s (%s) listed=%v want %v", level, def.Name, def.Level, got[def.Name], want)
			}
		}
		if level == "" && got["set_global_settings"] {
			t.Error("the default level must not expose admin tools")
		}
	}
}

func TestHiddenToolCallExplainsHowToEnable(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	srv.level = control.LevelRead
	p := startPeer(t, srv)
	p.handshake("2025-11-25")
	p.send(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"approve_permission","arguments":{"session_id":"a/b","request_id":"r"}}}`)
	r := p.result(2)
	c, _ := r["content"].([]any)
	txt, _ := c[0].(map[string]any)["text"].(string)
	if r["isError"] != true || !strings.Contains(txt, "CM_MCP_LEVEL=admin") {
		t.Fatalf("want an isError result with the enable hint, got %v", r)
	}
	if stub.lastPath != "" {
		t.Errorf("a refused tool must not reach the control-plane (hit %s)", stub.lastPath)
	}
}

func TestCallToolRefusesAboveLevel(t *testing.T) {
	stub := &stubCP{}
	srv := newTestMCPServer(t, stub)
	srv.level = control.LevelOperate
	if _, err := srv.callTool("set_global_settings", json.RawMessage(`{}`)); err == nil || !strings.Contains(err.Error(), "admin") {
		t.Fatalf("operate must refuse set_global_settings, got %v", err)
	}
	if stub.lastPath != "" {
		t.Error("refused call reached the control-plane")
	}
}

func TestRequestCarriesLevelAndSelfHeaders(t *testing.T) {
	var got http.Header
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":[]}`))
	}))
	defer ts.Close()
	srv := &mcpServer{baseURL: ts.URL, token: "tok", client: ts.Client(), level: control.LevelRead, callerSession: "lumen/P1", allowSelf: true}
	if _, err := srv.callTool("get_sessions", json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{control.HeaderLevel: "read", control.HeaderCaller: "lumen/P1", control.HeaderAllowSelf: "1"} {
		if got.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, got.Get(k), v)
		}
	}
	srv.callerSession, srv.allowSelf, srv.level = "", false, ""
	_, _ = srv.callTool("get_sessions", json.RawMessage(`{}`))
	if got.Get(control.HeaderLevel) != "operate" || got.Get(control.HeaderAllowSelf) != "" {
		t.Errorf("defaults wrong: level=%q allowSelf=%q", got.Get(control.HeaderLevel), got.Get(control.HeaderAllowSelf))
	}
}
