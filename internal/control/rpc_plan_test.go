package control

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// planRPC posts one JSON-RPC body and decodes the response.
func planRPC(t *testing.T, ts *httptest.Server, token, body string) rpcResponse {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/rpc", strings.NewReader(body))
	req.Header.Set("X-CM-Token", token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestRPC_PlanMethodsDispatch(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantCall string
	}{
		{
			"run_preflight",
			`{"jsonrpc":"2.0","id":1,"method":"RunPreflight","params":{"project":"proj","task":"do the thing"}}`,
			"RunPreflight proj do the thing",
		},
		{
			// Wails/JS callers pass plan_id as a JSON number.
			"execute_plan_number",
			`{"jsonrpc":"2.0","id":2,"method":"ExecutePlan","params":{"plan_id":5}}`,
			"ExecutePlan 5",
		},
		{
			// The MCP tool schema passes all params as strings (cm-mcp).
			"execute_plan_string",
			`{"jsonrpc":"2.0","id":3,"method":"ExecutePlan","params":{"plan_id":"7"}}`,
			"ExecutePlan 7",
		},
		{
			"get_plan",
			`{"jsonrpc":"2.0","id":4,"method":"GetPlan","params":{"plan_id":3}}`,
			"GetPlan 3",
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

func TestRPC_ApprovePlanReturnsApproved(t *testing.T) {
	mgr := &mockManager{}
	srv, token := newTestServer(t, mgr)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"ApprovePlan","params":{"plan":{"project":"proj","original_task":"t"}}}`
	out := planRPC(t, ts, token, body)
	if out.Error != nil {
		t.Fatalf("unexpected RPC error: %s", out.Error.Message)
	}
	raw, _ := json.Marshal(out.Result)
	if !strings.Contains(string(raw), `"approved"`) {
		t.Fatalf("expected approved plan in result, got: %s", raw)
	}
}

func TestRPC_ExecutePlanBadID(t *testing.T) {
	mgr := &mockManager{}
	srv, token := newTestServer(t, mgr)
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"ExecutePlan","params":{"plan_id":"abc"}}`
	out := planRPC(t, ts, token, body)
	if out.Error == nil {
		t.Fatal("expected RPC error for non-numeric plan_id")
	}
	if len(mgr.calls) != 0 {
		t.Fatalf("manager must not be called on bad plan_id, got %v", mgr.calls)
	}
}

func TestParsePlanID(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		want    int64
		wantErr bool
	}{
		{"number", `5`, 5, false},
		{"string", `"7"`, 7, false},
		{"string_spaces", `" 8 "`, 8, false},
		{"not_numeric", `"abc"`, 0, true},
		{"bool", `true`, 0, true},
		{"empty", ``, 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parsePlanID(json.RawMessage(tc.raw))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parsePlanID(%s): expected error, got %d", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePlanID(%s): %v", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("parsePlanID(%s) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}
