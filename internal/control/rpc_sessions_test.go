package control

import (
	"encoding/base64"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sessionsApp is a mockApp that also implements SessionApp.
type sessionsApp struct {
	*mockApp
	calls *[]string
}

func (a sessionsApp) rec(s string) error { *a.calls = append(*a.calls, s); return nil }
func (a sessionsApp) StartSessionWithModel(p, n, m, e string) error {
	return a.rec("StartSessionWithModel " + p + "/" + n + " " + m + " " + e)
}
func (a sessionsApp) StartAdHocChatSession(p string) error { return a.rec("StartAdHocChatSession " + p) }
func (a sessionsApp) StartProject(p string) error          { return a.rec("StartProject " + p) }
func (a sessionsApp) SetSessionModel(id, m string) error {
	return a.rec("SetSessionModel " + id + " " + m)
}
func (a sessionsApp) SetSessionRuntime(id, r string) error {
	return a.rec("SetSessionRuntime " + id + " " + r)
}

func rpcBody(method, params string) string {
	return `{"jsonrpc":"2.0","id":1,"method":"` + method + `","params":` + params + `}`
}

func TestRPC_SessionMethodsPreferApp(t *testing.T) {
	var calls []string
	srv, token := newTestServer(t, &mockManager{})
	srv.app = sessionsApp{&mockApp{}, &calls}
	srv.registry = srv.buildRegistry()
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	for _, tc := range []struct{ method, params, want string }{
		{"StartSessionWithModel", `{"project":"p","session":"s","model":"opus","effort":"high","runtime":"hermes"}`, "SetSessionRuntime p/s hermes|StartSessionWithModel p/s opus high"},
		{"StartAdHocChatSession", `{"project":"p"}`, "StartAdHocChatSession p"},
		{"StartProject", `{"project":"p"}`, "StartProject p"},
		{"SetSessionModel", `{"id":"p/s","model":"sonnet"}`, "SetSessionModel p/s sonnet"},
		{"SetSessionRuntime", `{"id":"p/s","runtime":"claude"}`, "SetSessionRuntime p/s claude"},
	} {
		calls = nil
		if out := planRPC(t, ts, token, rpcBody(tc.method, tc.params)); out.Error != nil {
			t.Fatalf("%s: %s", tc.method, out.Error.Message)
		}
		if got := strings.Join(calls, "|"); got != tc.want {
			t.Errorf("%s: calls %q, want %q", tc.method, got, tc.want)
		}
	}
}

func TestRPC_SessionMethodsFallBackWithoutApp(t *testing.T) {
	mgr := &mockManager{}
	srv, token := newTestServer(t, mgr)
	srv.app = nil
	srv.registry = srv.buildRegistry()
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	if out := planRPC(t, ts, token, rpcBody("SetSessionModel", `{"id":"p/s","model":"opus"}`)); out.Error != nil {
		t.Fatal(out.Error.Message)
	}
	if len(mgr.calls) != 1 || mgr.calls[0] != "SetSessionModel p/s opus" {
		t.Errorf("manager calls = %v", mgr.calls)
	}
	for _, m := range []string{"StartAdHocChatSession", "SetSessionRuntime"} {
		out := planRPC(t, ts, token, rpcBody(m, `{"project":"p","id":"p/s","runtime":"claude"}`))
		if out.Error == nil || !strings.Contains(out.Error.Message, "needs the Claude Manager app") {
			t.Errorf("%s without app: %+v", m, out.Error)
		}
	}
}

func TestRPC_SessionOverviewUnknown(t *testing.T) {
	srv, token := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()
	out := planRPC(t, ts, token, rpcBody("GetSessionOverview", `{"id":"p/s"}`))
	if out.Error == nil || !strings.Contains(out.Error.Message, "get_sessions") {
		t.Fatalf("want actionable not-found error, got %+v", out.Error)
	}
}

func TestResolveImages(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "a.PNG")
	os.WriteFile(png, []byte("png-bytes"), 0o644)
	txt := filepath.Join(dir, "a.txt")
	os.WriteFile(txt, []byte("x"), 0o644)
	big := filepath.Join(dir, "big.png")
	os.WriteFile(big, make([]byte, MaxImageBytes+1), 0o644)
	b64 := base64.StdEncoding.EncodeToString([]byte("x"))

	ok, err := resolveImages([]imageArg{{Path: png}, {DataBase64: b64, MediaType: "image/gif"}})
	if err != nil || len(ok) != 2 || ok[0].MediaType != "image/png" || ok[1].MediaType != "image/gif" {
		t.Fatalf("valid images: %v %+v", err, ok)
	}
	bad := map[string][]imageArg{
		"exactly one":   {{Path: png, DataBase64: b64}},
		"not a png":     {{Path: txt}},
		"cannot read":   {{Path: filepath.Join(dir, "missing.png")}},
		"over the":      {{Path: big}},
		"valid base64":  {{DataBase64: "!!!", MediaType: "image/png"}},
		"media_type is": {{DataBase64: b64}},
		"must be image": {{DataBase64: b64, MediaType: "text/plain"}},
		"at most":       make([]imageArg, MaxImages+1),
	}
	for want, in := range bad {
		if _, err := resolveImages(in); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want error containing %q, got %v", want, err)
		}
	}
}

func TestSessionTools_Translate(t *testing.T) {
	cases := []struct{ tool, args, method string }{
		{"start_session", `{"session_id":"p/s"}`, "StartSession"},
		{"start_session", `{"session_id":"p/s","model":"opus"}`, "StartSessionWithModel"},
		{"send_message", `{"session_id":"p/s","text":"x","images":[{"path":"a.png"}]}`, "SendMessageWithImages"},
		{"set_session_model", `{"session_id":"p/s","model":"opus"}`, "SetSessionModel"},
		{"set_session_runtime", `{"session_id":"p/s","runtime":"hermes"}`, "SetSessionRuntime"},
		{"clear_session_state", `{"session_id":"p/s"}`, "ClearSessionState"},
		{"start_project", `{"project":"p"}`, "StartProject"},
		{"stop_project", `{"project":"p"}`, "StopProject"},
		{"stop_all_sessions", `{"confirm":true}`, "StopAll"},
		{"start_ad_hoc_chat", `{"project":"p"}`, "StartAdHocChatSession"},
		{"get_session", `{"session_id":"p/s"}`, "GetSessionOverview"},
		{"list_slash_commands", `{"session_id":"p/s"}`, "ListSlashCommands"},
	}
	for _, c := range cases {
		_, body, err := TranslateTool(c.tool, []byte(c.args))
		if err != nil {
			t.Errorf("%s: %v", c.tool, err)
			continue
		}
		if got := body.(map[string]any)["method"]; got != c.method {
			t.Errorf("%s: method %v, want %s", c.tool, got, c.method)
		}
	}
	for _, tool := range []string{"set_session_model", "set_session_runtime", "start_project"} {
		if _, _, err := TranslateTool(tool, []byte(`{"session_id":"p/s"}`)); err == nil {
			t.Errorf("%s without its required argument must fail", tool)
		}
	}
}

func TestStopAllNeedsConfirm(t *testing.T) {
	_, _, err := TranslateTool("stop_all_sessions", []byte(`{}`))
	if err == nil || !strings.Contains(err.Error(), "confirm=true") {
		t.Fatalf("want an actionable confirm error, got %v", err)
	}
}
