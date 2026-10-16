package control

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claude-manager/internal/logger"
	"claude-manager/internal/sesstoken"
)

func TestParseLevel(t *testing.T) {
	for in, want := range map[string]AccessLevel{"": LevelOperate, "READ": LevelRead, " admin ": LevelAdmin, "operate": LevelOperate} {
		got, err := ParseLevel(in)
		if err != nil || got != want {
			t.Errorf("ParseLevel(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := ParseLevel("root"); err == nil {
		t.Error("ParseLevel(root) must fail")
	}
	if !LevelAdmin.Allows(LevelRead) || LevelRead.Allows(LevelOperate) || AccessLevel("x").Allows(LevelRead) {
		t.Error("Allows ordering is wrong")
	}
}

// Every RPC method must have an explicit level: a forgotten one silently
// becomes admin-only, which is safe but breaks the tool that needs it.
func TestRPCLevelsCoverRegistry(t *testing.T) {
	srv, _ := newTestServer(t, &mockManager{})
	for m := range srv.registry {
		if _, ok := rpcLevels[m]; !ok && m != "RespondPermission" {
			t.Errorf("RPC method %s has no entry in rpcLevels", m)
		}
	}
	for m := range rpcLevels {
		if _, ok := srv.registry[m]; !ok {
			t.Errorf("rpcLevels lists %s, which is not an RPC method", m)
		}
	}
}

// A tool must not need a higher RPC level than its own, or a cm-mcp running at
// the tool's level would be refused by the control-plane.
func TestToolLevelsCoverTheirRPCs(t *testing.T) {
	for _, tool := range MCPTools {
		for _, m := range tool.Covers {
			need := RPCLevel(m, "deny")
			if m == "RespondPermission" && tool.Name == "approve_permission" {
				need = RPCLevel(m, "allow")
			}
			if !tool.Level.Allows(need) {
				t.Errorf("tool %s (%s) covers %s which needs %s", tool.Name, tool.Level, m, need)
			}
		}
	}
}

func rpcAt(t *testing.T, ts *httptest.Server, token string, hdr map[string]string, method, params string) map[string]any {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/rpc",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"`+method+`","params":`+params+`}`))
	req.Header.Set("X-CM-Token", token)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func errMsg(r map[string]any) string {
	e, _ := r["error"].(map[string]any)
	s, _ := e["message"].(string)
	return s
}

func TestRPCLevelEnforced(t *testing.T) {
	srv, token := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()
	lvl := func(l string) map[string]string { return map[string]string{HeaderLevel: l} }

	cases := []struct {
		level, method, params string
		wantOK                bool
	}{
		{"read", "GetAllSessions", `{}`, true},
		{"read", "SendMessage", `{"id":"a/b","message":"x"}`, false},
		{"read", "StopAll", `{}`, false},
		{"operate", "SendMessage", `{"id":"a/b","message":"x"}`, true},
		{"operate", "UpdateConfig", `{}`, false},
		{"operate", "StopAll", `{}`, false},
		{"operate", "StopProject", `{"project":"a"}`, false},
		{"operate", "RespondPermission", `{"id":"a/b","request_id":"r","decision":"allow"}`, false},
		{"operate", "RespondPermission", `{"id":"a/b","request_id":"r","decision":"deny"}`, true},
		{"admin", "RespondPermission", `{"id":"a/b","request_id":"r","decision":"allow"}`, true},
		{"admin", "StopAll", `{}`, true},
		{"", "StopAll", `{}`, false}, // no header: operate, not admin (MCP-19)
		{"", "SendMessage", `{"id":"a/b","message":"x"}`, true},
	}
	for _, c := range cases {
		hdr := lvl(c.level)
		if c.level == "" {
			hdr = nil
		}
		r := rpcAt(t, ts, token, hdr, c.method, c.params)
		if gotOK := r["error"] == nil; gotOK != c.wantOK {
			t.Errorf("%s at %q: ok=%v want %v (%s)", c.method, c.level, gotOK, c.wantOK, errMsg(r))
		}
		if !c.wantOK && !strings.Contains(errMsg(r), "CM_MCP_LEVEL=") {
			t.Errorf("%s at %q: refusal lacks the enable hint: %q", c.method, c.level, errMsg(r))
		}
	}
	if r := rpcAt(t, ts, token, lvl("root"), "GetAllSessions", `{}`); r["error"] == nil {
		t.Error("an unknown level must be refused")
	}
}

func TestSelfProtection(t *testing.T) {
	srv, token := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()
	h := func(extra ...string) map[string]string {
		m := map[string]string{HeaderLevel: "admin", HeaderCaller: "lumen/P1"}
		for i := 0; i+1 < len(extra); i += 2 {
			m[extra[i]] = extra[i+1]
		}
		return m
	}
	refused := []struct{ method, params string }{
		{"StopSession", `{"id":"lumen/P1"}`},
		{"RestartSession", `{"id":"lumen/P1"}`},
		{"ClearSessionState", `{"project":"lumen","session":"P1"}`},
		{"StopProject", `{"project":"lumen"}`},
		{"StopAll", `{}`},
		{"InterruptSession", `{"id":"lumen/P1"}`},
		{"ResumeSession", `{"id":"lumen/P1"}`},
		{"SetSessionRuntime", `{"id":"lumen/P1","runtime":"claude"}`},
		{"RemoveSession", `{"project":"lumen","session":"P1"}`},
		{"UpdateSession", `{"project":"lumen","session":"P1","fields":{}}`},
		{"ClearProjectLogs", `{"project":"lumen"}`},
	}
	for _, c := range refused {
		if r := rpcAt(t, ts, token, h(), c.method, c.params); !strings.Contains(errMsg(r), "calling session") {
			t.Errorf("%s on self must be refused, got %v", c.method, r)
		}
		if r := rpcAt(t, ts, token, h(HeaderAllowSelf, "1"), c.method, c.params); strings.Contains(errMsg(r), "calling session") {
			t.Errorf("%s on self with the flag must pass, got %s", c.method, errMsg(r))
		}
	}
	// Another session or project is unaffected.
	for _, c := range []struct{ method, params string }{
		{"StopSession", `{"id":"lumen/P2"}`},
		{"ClearSessionState", `{"project":"lumen","session":"P2"}`},
		{"StopProject", `{"project":"other"}`},
	} {
		if r := rpcAt(t, ts, token, h(), c.method, c.params); r["error"] != nil {
			t.Errorf("%s on another session refused: %s", c.method, errMsg(r))
		}
	}
}

func TestSessionTokenIdentifiesCaller(t *testing.T) {
	srv, _ := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()
	tokA := sesstoken.Issue("lumen/A")
	defer sesstoken.Revoke(tokA)
	// The header claims B; the token says A, so StopSession(A) is a self-stop.
	hdr := map[string]string{HeaderLevel: "admin", HeaderCaller: "lumen/B"}
	if r := rpcAt(t, ts, tokA, hdr, "StopSession", `{"id":"lumen/A"}`); !strings.Contains(errMsg(r), "calling session lumen/A") {
		t.Errorf("session token must define the caller, got %v", r)
	}
	if r := rpcAt(t, ts, tokA, hdr, "StopSession", `{"id":"lumen/B"}`); r["error"] != nil {
		t.Errorf("stopping another session must pass: %s", errMsg(r))
	}
	// No header at all: still the token's session.
	if r := rpcAt(t, ts, tokA, map[string]string{HeaderLevel: "admin"}, "StopAll", `{}`); !strings.Contains(errMsg(r), "calling session") {
		t.Errorf("StopAll with a session token must be a self-target, got %v", r)
	}
	sesstoken.Revoke(tokA)
	if r := rpcAt(t, ts, tokA, hdr, "GetAllSessions", `{}`); r["result"] != nil {
		t.Errorf("a revoked token must be refused, got %v", r)
	}
}

func captureLog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	old := logger.L
	logger.L = slog.New(slog.NewTextHandler(&buf, nil))
	t.Cleanup(func() { logger.L = old })
	return &buf
}

func TestAuditLog(t *testing.T) {
	buf := captureLog(t)
	srv, token := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()
	hdr := map[string]string{HeaderLevel: "operate", HeaderClient: "claude-code 2.1"}

	rpcAt(t, ts, token, hdr, "GetAllSessions", `{}`)
	rpcAt(t, ts, token, hdr, "SendMessage", `{"id":"a/b","message":"hi","api_token":"s3cret-value"}`)
	rpcAt(t, ts, token, hdr, "UpdateConfig", `{}`)

	log := buf.String()
	if strings.Contains(log, "method=GetAllSessions") {
		t.Error("read calls must not be audited")
	}
	for _, want := range []string{"control.audit", "method=SendMessage", "result=ok", "claude-code 2.1", "method=UpdateConfig", "denied:level"} {
		if !strings.Contains(log, want) {
			t.Errorf("audit log lacks %q:\n%s", want, log)
		}
	}
	if strings.Contains(log, "s3cret-value") || !strings.Contains(log, "***") {
		t.Errorf("secret not redacted:\n%s", log)
	}
}

func TestTokenNeverLogged(t *testing.T) {
	buf := captureLog(t)
	srv, token := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()
	rpcAt(t, ts, token, map[string]string{HeaderLevel: "operate"}, "StopAll", `{}`)
	rpcAt(t, ts, "wrong-"+token, nil, "GetAllSessions", `{}`)
	resp, _ := http.Get(ts.URL + "/events?token=" + token)
	if resp != nil {
		resp.Body.Close()
	}
	if strings.Contains(buf.String(), token) {
		t.Errorf("token leaked into the log:\n%s", buf.String())
	}
}

func TestListensOnLoopbackOnly(t *testing.T) {
	srv, _ := newTestServer(t, &mockManager{})
	ln, err := srv.Listen("0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !strings.HasPrefix(ln.Addr().String(), "127.0.0.1:") {
		t.Errorf("control-plane bound to %s, want loopback", ln.Addr())
	}
}

func TestCleanClient(t *testing.T) {
	if got := cleanClient("ab\ncd\x01é"); got != "abcd" {
		t.Errorf("cleanClient = %q", got)
	}
	if got := cleanClient(strings.Repeat("x", 500)); len(got) != 80 {
		t.Errorf("cleanClient length = %d, want 80", len(got))
	}
}
