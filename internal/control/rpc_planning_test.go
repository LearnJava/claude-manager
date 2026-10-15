package control

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"claude-manager/internal/analysis"
)

type planningApp struct {
	*mockApp
	calls *[]string
}

func (a planningApp) rec(s string) { *a.calls = append(*a.calls, s) }
func (a planningApp) ApproveRoadmap(id int64, overwrite bool) (*analysis.TaskPlan, error) {
	a.rec(fmt.Sprintf("ApproveRoadmap %d %v", id, overwrite))
	return &analysis.TaskPlan{ID: id, Status: analysis.PlanStatusApproved}, nil
}
func (a planningApp) GetSessionRoadmap(p, s string) (*analysis.RoadmapView, error) {
	a.rec("GetSessionRoadmap " + p + " " + s)
	if s == "none" {
		return nil, nil
	}
	return &analysis.RoadmapView{Title: "R", Total: 2, Nodes: []*analysis.RoadmapNode{
		{Kind: "phase", Name: "Phase", Children: []*analysis.RoadmapNode{{Kind: "task", Name: "T1"}}},
		{Kind: "task", Name: "T2"},
	}}, nil
}
func (a planningApp) GetRoadmapTaskDetail(p, rel string) (string, error) {
	a.rec("GetRoadmapTaskDetail " + p + " " + rel)
	return "body", nil
}
func (a planningApp) GetRoadmapRowDetail(p, f string, line int) (string, error) {
	a.rec(fmt.Sprintf("GetRoadmapRowDetail %s %s %d", p, f, line))
	return "row", nil
}

func newPlanningServer(t *testing.T, mgr *mockManager) (*httptest.Server, string, *[]string) {
	t.Helper()
	var calls []string
	srv, token := newTestServer(t, mgr)
	srv.app = planningApp{&mockApp{}, &calls}
	srv.registry = srv.buildRegistry()
	ts := httptest.NewServer(srv.srv.Handler)
	t.Cleanup(ts.Close)
	return ts, token, &calls
}

func TestRPC_RoadmapManagerMethods(t *testing.T) {
	mgr := &mockManager{}
	ts, token, _ := newPlanningServer(t, mgr)
	call(t, ts, token, "GenerateRoadmap", `{"project":"p","idea":"an app","model":"opus"}`)
	planRPC(t, ts, token, rpcBody("GetLatestDraftRoadmap", `{"project":"p"}`))
	want := []string{"GenerateRoadmap p an app opus", "GetLatestDraftRoadmap p"}
	if strings.Join(mgr.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls %v, want %v", mgr.calls, want)
	}
	if msg := callErr(t, ts, token, "GenerateRoadmap", `{"project":"p"}`); !strings.Contains(msg, "idea") {
		t.Errorf("missing idea: %s", msg)
	}
}

func TestRPC_RoadmapAppMethods(t *testing.T) {
	ts, token, calls := newPlanningServer(t, &mockManager{})
	call(t, ts, token, "ApproveRoadmap", `{"plan_id":"9","overwrite":true}`)
	call(t, ts, token, "GetSessionRoadmap", `{"project":"p","session":"P1"}`)
	planRPC(t, ts, token, rpcBody("GetRoadmapTaskDetail", `{"project":"p","rel_path":"tasks/1.md"}`))
	planRPC(t, ts, token, rpcBody("GetRoadmapRowDetail", `{"project":"p","roadmap_file":"ROADMAP.md","line":7}`))
	want := "ApproveRoadmap 9 true|GetSessionRoadmap p P1|GetRoadmapTaskDetail p tasks/1.md|GetRoadmapRowDetail p ROADMAP.md 7"
	if got := strings.Join(*calls, "|"); got != want {
		t.Fatalf("calls %q, want %q", got, want)
	}
	if msg := callErr(t, ts, token, "ApproveRoadmap", `{}`); !strings.Contains(msg, "plan_id") {
		t.Errorf("missing plan_id: %s", msg)
	}
}

func TestRPC_RoadmapNeedsApp(t *testing.T) {
	srv, token := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()
	if msg := callErr(t, ts, token, "ApproveRoadmap", `{"plan_id":1}`); !strings.Contains(msg, "needs the Claude Manager app") {
		t.Errorf("no app: %s", msg)
	}
}

func TestFlattenNodes(t *testing.T) {
	tree := []any{
		map[string]any{"name": "A", "children": []any{map[string]any{"name": "A1"}}},
		map[string]any{"name": "B"},
	}
	flat := flattenNodes(tree, 0, nil)
	var got []string
	for _, n := range flat {
		m := n.(map[string]any)
		if _, has := m["children"]; has {
			t.Errorf("children kept: %v", m)
		}
		got = append(got, fmt.Sprintf("%v %v", m["name"], m["depth"]))
	}
	if strings.Join(got, ",") != "A 0,A1 1,B 0" {
		t.Errorf("order/depth: %v", got)
	}
}

func TestTextWindow(t *testing.T) {
	text := strings.Repeat("я", MaxResponseBytes) // two bytes per rune
	first, p, err := textWindow(text, "")
	if err != nil || !p.Truncated || len(first) > MaxResponseBytes || len(first)%2 != 0 {
		t.Fatalf("first window: len %d, %+v, %v", len(first), p, err)
	}
	rest, p2, _ := textWindow(text, p.NextCursor)
	if p2.Truncated != (len(first)+len(rest) < len(text)) || first+rest != text[:len(first)+len(rest)] {
		t.Fatalf("windows do not join: %d+%d of %d", len(first), len(rest), len(text))
	}
	if _, _, err := textWindow(text, "-1"); err == nil {
		t.Error("negative cursor accepted")
	}
	if short, p, _ := textWindow("abc", ""); short != "abc" || p.Truncated {
		t.Errorf("short text: %q %+v", short, p)
	}
}
