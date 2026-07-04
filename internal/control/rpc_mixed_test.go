package control

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRPC_MixedMethodsDispatch(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCall string
	}{
		{
			"register_mixed_brief",
			`{"jsonrpc":"2.0","id":1,"method":"RegisterMixedBrief","params":{"id":"b1","task":"Implement Greet"}}`,
			"RegisterMixedBrief b1 Implement Greet",
		},
		{
			"dispatch_mixed_task",
			`{"jsonrpc":"2.0","id":2,"method":"DispatchMixedTask","params":{"project":"proj","brief_id":"b1","worker":"step37"}}`,
			"DispatchMixedTask proj b1 step37",
		},
		{
			"get_mixed_rounds",
			`{"jsonrpc":"2.0","id":3,"method":"GetMixedRounds","params":{"project":"proj"}}`,
			"GetMixedRounds proj",
		},
		{
			"get_mixed_quality",
			`{"jsonrpc":"2.0","id":5,"method":"GetMixedQuality","params":{"project":"proj"}}`,
			"GetMixedQuality proj",
		},
		{
			"cancel_mixed_task",
			`{"jsonrpc":"2.0","id":4,"method":"CancelMixedTask","params":{"id":"proj/b1/step37"}}`,
			"CancelMixedTask proj/b1/step37",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := &mockManager{}
			srv, token := newTestServer(t, mgr)
			ts := httptest.NewServer(srv.srv.Handler)
			defer ts.Close()

			out := planRPC(t, ts, token, tc.body)
			if out.Error != nil {
				t.Fatalf("unexpected RPC error: %s", out.Error.Message)
			}
			found := false
			for _, c := range mgr.calls {
				if c == tc.wantCall {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected manager call %q, got %v", tc.wantCall, mgr.calls)
			}
		})
	}
}

func TestRPC_DispatchMixedTask_ReturnsTask(t *testing.T) {
	mgr := &mockManager{}
	srv, token := newTestServer(t, mgr)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	out := planRPC(t, ts, token,
		`{"jsonrpc":"2.0","id":1,"method":"DispatchMixedTask","params":{"project":"p","brief_id":"b","worker":"w"}}`)
	if out.Error != nil {
		t.Fatalf("unexpected RPC error: %s", out.Error.Message)
	}
	raw, _ := json.Marshal(out.Result)
	for _, want := range []string{`"p/b/w"`, `"done"`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("result missing %s: %s", want, raw)
		}
	}
}

func TestRPC_RegisterMixedBrief_RequiresIDAndTask(t *testing.T) {
	mgr := &mockManager{}
	srv, token := newTestServer(t, mgr)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	out := planRPC(t, ts, token,
		`{"jsonrpc":"2.0","id":1,"method":"RegisterMixedBrief","params":{"id":"b1"}}`)
	if out.Error == nil {
		t.Fatal("expected error for brief without task")
	}
	if len(mgr.calls) != 0 {
		t.Fatalf("manager must not be called on validation failure, got %v", mgr.calls)
	}
}

func TestTranslateTool_Mixed(t *testing.T) {
	cases := []struct {
		tool         string
		args         string
		wantEndpoint string
		wantContains []string
	}{
		{
			"register_mixed_brief",
			`{"id":"b1","task":"Implement Greet"}`,
			"/rpc",
			[]string{`"RegisterMixedBrief"`, `"b1"`, `"Implement Greet"`},
		},
		{
			"dispatch_mixed_task",
			`{"project":"proj","brief_id":"b1","worker":"step37"}`,
			"/rpc",
			[]string{`"DispatchMixedTask"`, `"brief_id":"b1"`, `"worker":"step37"`},
		},
		{
			"get_mixed_rounds",
			`{"project":"proj"}`,
			"/rpc",
			[]string{`"GetMixedRounds"`, `"proj"`},
		},
		{
			"wait_for_worker_status",
			`{"task_id":"proj/b1/step37","status":"done"}`,
			"/wait",
			[]string{`"worker:done"`, `"task_id":"proj/b1/step37"`, `"status":"done"`, `"timeout_ms":60000`},
		},
		{
			// status omitted: match by task_id only, either terminal status.
			"wait_for_worker_status",
			`{"task_id":"proj/b1/step37"}`,
			"/wait",
			[]string{`"worker:done"`, `"task_id":"proj/b1/step37"`},
		},
	}

	for _, tc := range cases {
		endpoint, body, err := TranslateTool(tc.tool, json.RawMessage(tc.args))
		if err != nil {
			t.Errorf("%s: %v", tc.tool, err)
			continue
		}
		if endpoint != tc.wantEndpoint {
			t.Errorf("%s: endpoint = %q, want %q", tc.tool, endpoint, tc.wantEndpoint)
		}
		raw, _ := json.Marshal(body)
		for _, want := range tc.wantContains {
			if !strings.Contains(string(raw), want) {
				t.Errorf("%s: body missing %s: %s", tc.tool, want, raw)
			}
		}
	}
}

func TestMCPTools_IncludeMixedTools(t *testing.T) {
	want := map[string]bool{
		"register_mixed_brief":   false,
		"dispatch_mixed_task":    false,
		"get_mixed_rounds":       false,
		"wait_for_worker_status": false,
	}
	for _, tool := range MCPTools {
		if _, ok := want[tool.Name]; ok {
			want[tool.Name] = true
			if !json.Valid(tool.InputSchema) {
				t.Errorf("%s: invalid input schema", tool.Name)
			}
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("tool %s not in MCPTools", name)
		}
	}
}
