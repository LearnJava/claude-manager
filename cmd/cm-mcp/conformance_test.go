package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// startProcPeer runs the real cm-mcp binary over stdio, the way an MCP client
// does, and speaks raw JSON-RPC to it.
func startProcPeer(t *testing.T, addr string) *rpcPeer {
	t.Helper()
	cmd := exec.Command(buildBinary(t, "cm-mcp"), "-level", "admin")
	cmd.Env = append(os.Environ(), "CM_CONTROL_ADDR="+addr, "CM_CONTROL_TOKEN=t")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &rpcPeer{t: t, w: in, lines: make(chan map[string]any, 16)}
	go func() {
		sc := bufio.NewScanner(out)
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
	t.Cleanup(func() {
		in.Close()
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
		}
	})
	return p
}

// errorCode waits for the response with id and returns its JSON-RPC error code.
func (p *rpcPeer) errorCode(id float64) float64 {
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
			e, _ := m["error"].(map[string]any)
			if e == nil {
				p.t.Fatalf("id %v: want an error, got %v", id, m)
			}
			return e["code"].(float64)
		case <-deadline:
			p.t.Fatalf("timeout waiting for response %v", id)
		}
	}
}

func (p *rpcPeer) call(id int, method, params string) map[string]any {
	p.t.Helper()
	p.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":%s}`, id, method, params))
	return p.result(float64(id))
}

// TestConformance_StdioSubprocess walks every capability the server declares
// and checks that everything it does not declare answers -32601.
func TestConformance_StdioSubprocess(t *testing.T) {
	cp := httptest.NewServer(&routedCP{results: map[string]string{
		"GetConfigView": `{"version":"v1","config":{"project":[]}}`,
	}})
	t.Cleanup(cp.Close)
	p := startProcPeer(t, cp.URL)

	p.send(strings.Replace(initLine, "%s", "2025-11-25", 1))
	init := p.result(1)
	caps, _ := init["capabilities"].(map[string]any)
	for _, c := range []string{"tools", "resources", "prompts"} {
		if caps[c] == nil {
			t.Fatalf("capability %s is not declared: %v", c, caps)
		}
	}
	for _, c := range []string{"logging", "completions", "tasks"} {
		if caps[c] != nil {
			t.Fatalf("capability %s is declared but not implemented", c)
		}
	}
	p.send(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)

	p.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	p.result(2)

	tools, _ := p.call(3, "tools/list", `{}`)["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("tools/list is empty")
	}
	for _, x := range tools {
		tl := x.(map[string]any)
		if tl["name"] == "" || tl["description"] == "" || tl["inputSchema"] == nil {
			t.Fatalf("incomplete tool definition: %v", tl)
		}
	}
	if r := p.call(4, "tools/call", `{"name":"get_projects","arguments":{}}`); r["content"] == nil {
		t.Fatalf("tools/call: %v", r)
	}

	res := p.call(5, "resources/list", `{}`)
	if rs, _ := res["resources"].([]any); len(rs) == 0 {
		t.Fatal("resources/list is empty")
	}
	if ts, _ := p.call(6, "resources/templates/list", `{}`)["resourceTemplates"].([]any); len(ts) == 0 {
		t.Fatal("resources/templates/list is empty")
	}
	if cs, _ := p.call(7, "resources/read", `{"uri":"cm://guide"}`)["contents"].([]any); len(cs) != 1 {
		t.Fatalf("resources/read cm://guide: %v", cs)
	}

	pr, _ := p.call(8, "prompts/list", `{}`)["prompts"].([]any)
	if len(pr) == 0 {
		t.Fatal("prompts/list is empty")
	}
	first, _ := pr[0].(map[string]any)["name"].(string)
	args := map[string]string{}
	for _, a := range asList(pr[0].(map[string]any)["arguments"]) {
		if am := a.(map[string]any); am["required"] == true {
			args[am["name"].(string)] = "demo/S1"
		}
	}
	ab, _ := json.Marshal(map[string]any{"name": first, "arguments": args})
	if msgs, _ := p.call(9, "prompts/get", string(ab))["messages"].([]any); len(msgs) == 0 {
		t.Fatalf("prompts/get %s: no messages", first)
	}

	// Not declared, so not implemented: the standard "method not found".
	for i, m := range []string{"completion/complete", "logging/setLevel", "tasks/list", "no/such/method"} {
		id := 100 + i
		p.send(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":%q,"params":{}}`, id, m))
		if code := p.errorCode(float64(id)); code != -32601 {
			t.Errorf("%s: want -32601, got %v", m, code)
		}
	}
	// Subscriptions are off too (listChanged/subscribe are not declared).
	p.send(`{"jsonrpc":"2.0","id":110,"method":"resources/subscribe","params":{"uri":"cm://sessions"}}`)
	if code := p.errorCode(110); code != -32601 {
		t.Errorf("resources/subscribe: want -32601, got %v", code)
	}

	p.send(`{"jsonrpc":"2.0","id":120,"method":"ping"}`)
	p.result(120) // still alive after the errors
}
