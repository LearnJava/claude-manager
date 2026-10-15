package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claude-manager/internal/control"
)

// routedCP answers each control-plane RPC method with a canned result.
type routedCP struct {
	results map[string]string
	calls   []map[string]any
}

func (c *routedCP) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	c.calls = append(c.calls, body)
	method, _ := body["method"].(string)
	res, ok := c.results[method]
	if !ok {
		fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"no stub for %s"}}`, method)
		return
	}
	fmt.Fprintf(w, `{"jsonrpc":"2.0","id":1,"result":%s}`, res)
}

func newResourcePeer(t *testing.T, cp *routedCP) *rpcPeer {
	t.Helper()
	ts := httptest.NewServer(cp)
	t.Cleanup(ts.Close)
	srv := &mcpServer{baseURL: ts.URL, token: "secret-token-xyz", level: control.LevelRead, client: ts.Client()}
	p := startPeer(t, srv)
	p.handshake("2025-11-25")
	return p
}

func readLine(id int, uri string) string {
	b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "resources/read", "params": map[string]any{"uri": uri}})
	return string(b)
}

func (p *rpcPeer) readText(id int, uri string) string {
	p.t.Helper()
	p.send(readLine(id, uri))
	cs, _ := p.result(float64(id))["contents"].([]any)
	if len(cs) != 1 {
		p.t.Fatalf("%s: want one content, got %v", uri, cs)
	}
	c, _ := cs[0].(map[string]any)
	if c["uri"] != uri || c["mimeType"] == nil || c["mimeType"] == "" {
		p.t.Fatalf("%s: bad content header %v", uri, c)
	}
	s, _ := c["text"].(string)
	return s
}

// errorFor waits for the JSON-RPC response with id and returns its error.
func (p *rpcPeer) errorFor(id float64) map[string]any {
	p.t.Helper()
	for m := range p.lines {
		if m["id"] == id {
			e, _ := m["error"].(map[string]any)
			return e
		}
	}
	p.t.Fatal("server closed stdout")
	return nil
}

func TestResources_ListAndTemplates(t *testing.T) {
	p := newResourcePeer(t, &routedCP{})
	p.send(`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`)
	got := map[string]bool{}
	list, _ := p.result(2)["resources"].([]any)
	for _, r := range list {
		m := r.(map[string]any)
		got[m["uri"].(string)] = true
		for _, f := range []string{"name", "title", "description", "mimeType"} {
			if m[f] == nil || m[f] == "" {
				t.Errorf("resource %v lacks %s", m["uri"], f)
			}
		}
	}
	for _, u := range []string{"cm://guide", "cm://projects", "cm://sessions"} {
		if !got[u] {
			t.Errorf("resources/list lacks %s: %v", u, got)
		}
	}
	p.send(`{"jsonrpc":"2.0","id":3,"method":"resources/templates/list"}`)
	tpls, _ := p.result(3)["resourceTemplates"].([]any)
	want := map[string]bool{
		"cm://projects/{project}/config":                 false,
		"cm://sessions/{project}/{session}":              false,
		"cm://sessions/{project}/{session}/log{?cursor}": false,
	}
	for _, r := range tpls {
		m := r.(map[string]any)
		if _, ok := want[m["uriTemplate"].(string)]; ok {
			want[m["uriTemplate"].(string)] = true
		}
		for _, f := range []string{"name", "title", "description", "mimeType"} {
			if m[f] == nil || m[f] == "" {
				t.Errorf("template %v lacks %s", m["uriTemplate"], f)
			}
		}
	}
	for u, ok := range want {
		if !ok {
			t.Errorf("templates lack %s", u)
		}
	}
}

func TestResources_ReadGuide(t *testing.T) {
	p := newResourcePeer(t, &routedCP{})
	if s := p.readText(2, "cm://guide"); !strings.Contains(s, "Claude Manager как MCP-сервер") {
		t.Fatalf("guide is not docs/mcp.md: %.80q", s)
	}
}

func TestResources_ReadEachTemplate(t *testing.T) {
	cp := &routedCP{results: map[string]string{
		"GetProjects":        `[{"name":"lumen","path":"D:/lumen"}]`,
		"GetAllSessions":     `[{"id":"lumen/P1","status":"waiting"}]`,
		"GetSessionOverview": `{"session":{"id":"lumen/P1","status":"waiting"},"pending_permissions":[]}`,
		"GetSessionLog":      `[{"type":"text","text":"hi"}]`,
		"GetConfigView":      `{"version":"v1","config":{"project":[{"name":"lumen","path":"D:/lumen","token":"[redacted]"},{"name":"other"}]}}`,
	}}
	p := newResourcePeer(t, cp)

	if s := p.readText(2, "cm://projects"); !strings.Contains(s, `"lumen"`) {
		t.Errorf("projects: %s", s)
	}
	if s := p.readText(3, "cm://sessions"); !strings.Contains(s, `"lumen/P1"`) {
		t.Errorf("sessions: %s", s)
	}
	if s := p.readText(4, "cm://sessions/lumen/P1"); !strings.Contains(s, `"waiting"`) {
		t.Errorf("session: %s", s)
	}
	if params := cp.calls[len(cp.calls)-1]["params"].(map[string]any); params["id"] != "lumen/P1" {
		t.Errorf("session id param: %v", params)
	}
	if s := p.readText(5, "cm://sessions/lumen/P1/log"); !strings.Contains(s, `"hi"`) {
		t.Errorf("log: %s", s)
	}
	p.readText(6, "cm://sessions/lumen/P1/log?cursor=5")
	if params := cp.calls[len(cp.calls)-1]["params"].(map[string]any); params["offset"] != float64(5) {
		t.Errorf("cursor not applied: %v", params)
	}
	s := p.readText(7, "cm://projects/lumen/config")
	if !strings.Contains(s, "D:/lumen") || strings.Contains(s, `"other"`) {
		t.Errorf("project config must hold only lumen: %s", s)
	}
}

func TestResources_UnknownIsNotFound(t *testing.T) {
	p := newResourcePeer(t, &routedCP{results: map[string]string{
		"GetConfigView": `{"version":"v1","config":{"project":[{"name":"lumen"}]}}`,
	}})
	for i, uri := range []string{"cm://nope", "cm://projects/ghost/config", "file:///etc/passwd"} {
		p.send(readLine(10+i, uri))
		if p.errorFor(float64(10+i)) == nil {
			t.Errorf("%s: want a JSON-RPC error", uri)
		}
	}
}

func TestResourcesAndTools_NeverLeakTokenOrSecrets(t *testing.T) {
	cp := &routedCP{results: map[string]string{
		"GetProjects":    `[{"name":"lumen"}]`,
		"GetAllSessions": `[{"id":"lumen/P1","status":"waiting"}]`,
		"GetConfigView":  `{"version":"v1","config":{"settings":{"api_key":"[redacted]"},"project":[{"name":"lumen"}]}}`,
	}}
	p := newResourcePeer(t, cp)
	var all strings.Builder
	for i, uri := range []string{"cm://guide", "cm://projects", "cm://sessions", "cm://projects/lumen/config"} {
		all.WriteString(p.readText(2+i, uri))
	}
	p.send(`{"jsonrpc":"2.0","id":20,"method":"tools/list"}`)
	b, _ := json.Marshal(p.result(20))
	all.Write(b)
	p.send(`{"jsonrpc":"2.0","id":21,"method":"prompts/list"}`)
	b, _ = json.Marshal(p.result(21))
	all.Write(b)
	if strings.Contains(all.String(), "secret-token-xyz") {
		t.Fatal("the control-plane token leaked into a resource, tool or prompt")
	}
}

func TestPrompts_ListAndGet(t *testing.T) {
	p := newResourcePeer(t, &routedCP{})
	p.send(`{"jsonrpc":"2.0","id":2,"method":"prompts/list"}`)
	list, _ := p.result(2)["prompts"].([]any)
	if len(list) != 5 {
		t.Fatalf("want 5 prompts, got %d", len(list))
	}
	for _, r := range list {
		m := r.(map[string]any)
		if m["title"] == nil || m["description"] == nil {
			t.Errorf("prompt %v lacks title/description", m["name"])
		}
	}
	p.send(`{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"run_task","arguments":{"session_id":"lumen/P1","task":"fix the bug"}}}`)
	msgs, _ := p.result(3)["messages"].([]any)
	if len(msgs) != 1 {
		t.Fatalf("messages: %v", msgs)
	}
	text, _ := msgs[0].(map[string]any)["content"].(map[string]any)["text"].(string)
	if !strings.Contains(text, "lumen/P1") || !strings.Contains(text, "fix the bug") || !strings.Contains(text, "wait_for_any") {
		t.Errorf("prompt text: %s", text)
	}
}

// Every `backticked`-free tool name a prompt mentions must exist in the registry.
func TestPrompts_NameRealTools(t *testing.T) {
	a := map[string]string{"path": "D:/x", "session_id": "p/s", "task": "t", "days": "3", "project": "p", "name": "n"}
	named := 0
	for _, d := range promptDefs {
		for _, w := range strings.FieldsFunc(d.text(a), func(r rune) bool { return !(r == '_' || r >= 'a' && r <= 'z') }) {
			if !strings.Contains(w, "_") || w == "session_id" || w == "cost_usd" {
				continue
			}
			named++
			if control.FindTool(w) == nil {
				t.Errorf("prompt %s names %q, which is not a tool", d.name, w)
			}
		}
	}
	if named == 0 {
		t.Fatal("no tool names found in prompts")
	}
}

func TestPrompts_RequiredArgumentAndUnknown(t *testing.T) {
	p := newResourcePeer(t, &routedCP{})
	p.send(`{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"run_task","arguments":{"task":"x"}}}`)
	if p.errorFor(2) == nil {
		t.Error("missing required argument must be an error")
	}
	p.send(`{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"nope"}}`)
	if p.errorFor(3) == nil {
		t.Error("unknown prompt must be an error")
	}
}

func TestPrompts_NoArgPromptsGet(t *testing.T) {
	p := newResourcePeer(t, &routedCP{})
	for i, name := range []string{"triage_waiting", "cost_report"} {
		p.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"prompts/get","params":{"name":%q}}`, 2+i, name))
		if msgs, _ := p.result(float64(2 + i))["messages"].([]any); len(msgs) != 1 {
			t.Errorf("%s: messages %v", name, msgs)
		}
	}
}
