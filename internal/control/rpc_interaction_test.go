package control

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/experience"
)

// interactionApp is a mockApp that also implements InteractionApp.
type interactionApp struct {
	*mockApp
	calls *[]string
}

func (a interactionApp) GetPermissionCandidates(project string, days int) (*experience.CandidateSet, error) {
	*a.calls = append(*a.calls, fmt.Sprintf("GetPermissionCandidates %s %d", project, days))
	return &experience.CandidateSet{}, nil
}

func (a interactionApp) AddPermissionRule(project, name, tool, pattern, decision string) error {
	*a.calls = append(*a.calls, "AddPermissionRule "+project+"/"+name+" "+tool+" "+pattern+" "+decision)
	return nil
}

func TestRPC_InteractionMethods(t *testing.T) {
	var calls []string
	mgr := &mockManager{}
	srv, token := newTestServer(t, mgr)
	srv.app = interactionApp{&mockApp{}, &calls}
	srv.registry = srv.buildRegistry()
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()

	if out := planRPC(t, ts, token, rpcBody("AnswerQuestion", `{"id":"p/s","question_id":"q1","answer":"yes"}`)); out.Error != nil {
		t.Fatal(out.Error.Message)
	}
	if got := strings.Join(mgr.calls, "|"); got != "AnswerQuestion p/s q1 yes" {
		t.Errorf("manager calls %q", got)
	}

	if out := planRPC(t, ts, token, rpcBody("AddPermissionRule", `{"project":"p","session":"s","tool":"Bash","pattern":"ls*","decision":"allow"}`)); out.Error != nil {
		t.Fatal(out.Error.Message)
	}
	if out := planRPC(t, ts, token, rpcBody("GetPermissionCandidates", `{"project":"p","days":30}`)); out.Error != nil {
		t.Fatal(out.Error.Message)
	}
	if got := strings.Join(calls, "|"); got != "AddPermissionRule p/s Bash ls* allow|GetPermissionCandidates p 30" {
		t.Errorf("app calls %q", got)
	}

	out := planRPC(t, ts, token, rpcBody("AddPermissionRule", `{"project":"p","session":"s","tool":"Bash","decision":"maybe"}`))
	if out.Error == nil || !strings.Contains(out.Error.Message, "allow or deny") {
		t.Errorf("bad decision: %+v", out.Error)
	}

	out = planRPC(t, ts, token, rpcBody("GetPending", `{"id":"p/s"}`))
	m, _ := out.Result.(map[string]any)
	if out.Error != nil || m["permissions"] == nil || m["questions"] == nil {
		t.Errorf("GetPending must return both lists even when empty: %+v", out)
	}
}

func TestRPC_InteractionNeedsApp(t *testing.T) {
	srv, token := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()
	for _, tc := range []struct{ method, params string }{
		{"AddPermissionRule", `{"project":"p","session":"s","tool":"Bash","decision":"allow"}`},
		{"GetPermissionCandidates", `{"project":"p"}`},
	} {
		out := planRPC(t, ts, token, rpcBody(tc.method, tc.params))
		if out.Error == nil || !strings.Contains(out.Error.Message, "needs the Claude Manager app") {
			t.Errorf("%s without app: %+v", tc.method, out.Error)
		}
	}
}

// TestE2E_QuestionAnsweredThroughRPC: a real session asks an ask-user
// question, get_pending lists it, AnswerQuestion resumes the same process.
func TestE2E_QuestionAnsweredThroughRPC(t *testing.T) {
	root := projectRoot()
	claudePath := buildFakeclaude(t)
	t.Setenv("FAKECLAUDE_SCENARIO", filepath.Join(root, "testdata", "scenarios"))
	ts, token := newE2EHarness(t, filepath.Join(root, "testdata", "configs", "ask-user.toml"), claudePath, root, "")
	runner := NewE2ERunner(ts.URL, token)

	steps := []E2EStep{
		{Do: "StartSession", With: json.RawMessage(`{"project":"lumen","session":"P1"}`)},
		{Wait: "session:status", Match: map[string]json.RawMessage{"id": json.RawMessage(`"lumen/P1"`), "status": json.RawMessage(`"waiting_for_user"`)}, TimeoutMs: 8000},
	}
	if err := runner.Run(&E2EScenario{Steps: steps}); err != nil {
		t.Fatal(err)
	}

	out := planRPC(t, ts, token, rpcBody("GetPending", `{"id":"lumen/P1"}`))
	m, _ := out.Result.(map[string]any)
	qs, _ := m["questions"].([]any)
	if out.Error != nil || len(qs) != 1 {
		t.Fatalf("GetPending: %+v", out)
	}
	q := qs[0].(map[string]any)["question"].(map[string]any)
	if q["question"] != "Which budget?" {
		t.Errorf("question = %v", q["question"])
	}

	params, _ := json.Marshal(map[string]string{"id": "lumen/P1", "question_id": q["id"].(string), "answer": "Keep 2ms"})
	if r := planRPC(t, ts, token, rpcBody("AnswerQuestion", string(params))); r.Error != nil {
		t.Fatal(r.Error.Message)
	}
	err := runner.Run(&E2EScenario{Steps: []E2EStep{
		{Wait: "session:status", Match: map[string]json.RawMessage{"id": json.RawMessage(`"lumen/P1"`), "status": json.RawMessage(`"idle"`)}, TimeoutMs: 8000},
		{Assert: "GetSessionMetrics", With: json.RawMessage(`{"id":"lumen/P1"}`), Expect: map[string]json.RawMessage{"num_turns": json.RawMessage(`2`)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	after := planRPC(t, ts, token, rpcBody("GetPending", `{"id":"lumen/P1"}`))
	if m, _ := after.Result.(map[string]any); len(m["questions"].([]any)) != 0 {
		t.Errorf("question still pending after the answer: %+v", after.Result)
	}
}
