package control

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"claude-manager/internal/config"
	"claude-manager/internal/experience"
	"claude-manager/internal/store"
)

type skillsApp struct {
	*mockApp
	calls *[]string
}

func (a skillsApp) rec(s string) { *a.calls = append(*a.calls, s) }
func (a skillsApp) GetConfig() *config.AppConfig {
	return &config.AppConfig{Projects: []config.ProjectConfig{{Name: "p", Gates: []string{"go test ./..."}}}}
}
func (a skillsApp) GetSkillCandidates(p string) ([]experience.SkillCandidate, error) {
	a.rec("GetSkillCandidates " + p)
	return []experience.SkillCandidate{
		{Sig: []string{"Bash:ls"}, Score: 1},
		{Sig: []string{"Bash:go test", "Edit"}, Score: 5},
	}, nil
}
func (a skillsApp) DistillSkill(p string, c experience.SkillCandidate, gates []string, model string, min float64) (*store.Skill, error) {
	a.rec(fmt.Sprintf("DistillSkill %s %v %v %q %v", p, c.Sig, gates, model, min))
	return &store.Skill{ID: 7, Project: p, Name: "run-tests", Status: store.SkillStatusDraft, MD: "# draft"}, nil
}
func (a skillsApp) GetSkills(p string) ([]store.Skill, error) {
	a.rec("GetSkills " + p)
	return []store.Skill{
		{ID: 7, Name: "run-tests", Status: store.SkillStatusDraft, MD: "# draft"},
		{ID: 8, Name: "old", Status: store.SkillStatusArchived, MD: "# old"},
	}, nil
}
func (a skillsApp) ApproveSkill(id int64, md string, overwrite bool) (string, error) {
	a.rec(fmt.Sprintf("ApproveSkill %d %q %v", id, md, overwrite))
	return "/proj/.claude/skills/run-tests/SKILL.md", nil
}
func (a skillsApp) ArchiveSkill(id int64) error { a.rec(fmt.Sprintf("ArchiveSkill %d", id)); return nil }
func (a skillsApp) RestoreSkill(id int64, overwrite bool) (string, error) {
	a.rec(fmt.Sprintf("RestoreSkill %d %v", id, overwrite))
	return "/proj/.claude/skills/old/SKILL.md", nil
}
func (a skillsApp) GetSkillUsage(p string) (map[int64]experience.SkillUsage, error) {
	a.rec("GetSkillUsage " + p)
	return map[int64]experience.SkillUsage{10: {SkillID: 10, Name: "b"}, 2: {SkillID: 2, Name: "a", Loads: 3}}, nil
}
func (a skillsApp) GetSkillQuality(p string) ([]experience.SkillEffect, error) {
	a.rec("GetSkillQuality " + p)
	return []experience.SkillEffect{{SkillID: 7, SkillName: "run-tests"}}, nil
}
func (a skillsApp) SkillAutopilotOf(p string) (any, error) {
	a.rec("SkillAutopilotOf " + p)
	return map[string]any{"enabled": false, "tracking": false, "trial_runs": 5}, nil
}
func (a skillsApp) SetSkillAutopilot(p string, on bool, budget float64) error {
	a.rec(fmt.Sprintf("SetSkillAutopilot %s %v %v", p, on, budget))
	return nil
}
func (a skillsApp) RunSkillAutopilot(p string) (experience.SkillPilotReport, error) {
	a.rec("RunSkillAutopilot " + p)
	return experience.SkillPilotReport{}, fmt.Errorf("skill autopilot is off for %s", p)
}

func newSkillsServer(t *testing.T) (*httptest.Server, string, *[]string) {
	t.Helper()
	var calls []string
	srv, token := newTestServer(t, &mockManager{})
	srv.app = skillsApp{&mockApp{}, &calls}
	srv.registry = srv.buildRegistry()
	ts := httptest.NewServer(srv.srv.Handler)
	t.Cleanup(ts.Close)
	return ts, token, &calls
}

func TestRPC_SkillsMethods(t *testing.T) {
	ts, token, calls := newSkillsServer(t)
	for _, c := range []struct{ method, params string }{
		{"GetSkillCandidates", `{"project":"p"}`},
		{"GetSkills", `{"project":"p"}`},
		{"GetSkillUsage", `{"project":"p"}`},
		{"GetSkillQuality", `{"project":"p"}`},
		{"GetSkillAutopilot", `{"project":"p"}`},
		{"DistillSkill", `{"project":"p","sig":["Bash:go test","Edit"],"model":"sonnet"}`},
		{"ApproveSkill", `{"project":"p","skill_id":"7"}`},
		{"ApproveSkill", `{"skill_id":7,"md":"# mine","overwrite":true}`},
		{"ArchiveSkill", `{"skill_id":"7"}`},
		{"RestoreSkill", `{"skill_id":8,"overwrite":true}`},
		{"SetSkillAutopilot", `{"project":"p","enabled":true,"daily_budget_usd":2}`},
	} {
		planRPC(t, ts, token, rpcBody(c.method, c.params))
	}
	want := []string{
		"GetSkillCandidates p", "GetSkills p", "GetSkillUsage p", "GetSkillQuality p", "SkillAutopilotOf p",
		// the candidate is found by signature and the project's gates are filled in
		"GetSkillCandidates p", `DistillSkill p [Bash:go test Edit] [go test ./...] "sonnet" 0`,
		// no md: the stored draft is approved
		"GetSkills p", `ApproveSkill 7 "# draft" false`,
		`ApproveSkill 7 "# mine" true`, "ArchiveSkill 7", "RestoreSkill 8 true", "SetSkillAutopilot p true 2",
	}
	if got := strings.Join(*calls, "|"); got != strings.Join(want, "|") {
		t.Fatalf("calls\n got %q\nwant %q", got, strings.Join(want, "|"))
	}
}

func TestRPC_SkillsErrors(t *testing.T) {
	ts, token, _ := newSkillsServer(t)
	for _, c := range []struct{ method, params, want string }{
		{"DistillSkill", `{"project":"p","sig":["nope"]}`, "no skill candidate with signature"},
		{"DistillSkill", `{"project":"p"}`, "sig"},
		{"ApproveSkill", `{"project":"p"}`, "skill_id"},
		{"ArchiveSkill", `{"skill_id":"x"}`, "not a number"},
		{"RunSkillAutopilot", `{"project":"p"}`, "autopilot is off"},
	} {
		if msg := callErr(t, ts, token, c.method, c.params); !strings.Contains(msg, c.want) {
			t.Errorf("%s: %q lacks %q", c.method, msg, c.want)
		}
	}
}

func TestRPC_SkillsNeedApp(t *testing.T) {
	srv, token := newTestServer(t, &mockManager{})
	ts := httptest.NewServer(srv.srv.Handler)
	defer ts.Close()
	if msg := callErr(t, ts, token, "GetSkills", `{"project":"p"}`); !strings.Contains(msg, "needs the Claude Manager app") {
		t.Errorf("no app: %s", msg)
	}
}

func TestExplainError_AutopilotOff(t *testing.T) {
	msg := ExplainError("run_skill_autopilot", fmt.Errorf("rpc error -32000: skill autopilot is off for lumen"), nil)
	for _, want := range []string{"set_skill_autopilot", "auto_skills", "lumen"} {
		if !strings.Contains(msg, want) {
			t.Errorf("%q lacks %q", msg, want)
		}
	}
}

func TestSkillTools_Shaping(t *testing.T) {
	out, _, err := ShapeResult("get_skills", []byte(`{"project":"p","status":"draft"}`), []any{
		map[string]any{"ID": 7.0, "Name": "a", "Status": "draft", "MD": "text", "DraftJSON": "{}"},
		map[string]any{"ID": 8.0, "Name": "b", "Status": "archived"},
	})
	if err != nil {
		t.Fatal(err)
	}
	b := mustJSON(out)
	if !strings.Contains(b, `"name":"a"`) || strings.Contains(b, `"name":"b"`) || strings.Contains(b, "text") || strings.Contains(b, "DraftJSON") {
		t.Errorf("status filter / md trimming: %s", b)
	}

	out, _, _ = ShapeResult("get_skill_usage", []byte(`{"project":"p"}`), map[string]any{
		"10": map[string]any{"skill_id": 10.0}, "2": map[string]any{"skill_id": 2.0},
	})
	if b := mustJSON(out); strings.Index(b, `"skill_id":2`) > strings.Index(b, `"skill_id":10`) {
		t.Errorf("usage not ordered by skill id: %s", b)
	}

	_, sum, _ := ShapeResult("get_skill_candidates", []byte(`{"project":"p"}`), []any{})
	if !strings.Contains(sum, "experience_tracking") {
		t.Errorf("empty candidates do not name the flag: %q", sum)
	}
	_, sum, _ = ShapeResult("get_skill_autopilot", []byte(`{"project":"p"}`), map[string]any{"enabled": false})
	if !strings.Contains(sum, "auto_skills") {
		t.Errorf("off autopilot does not name the flag: %q", sum)
	}
}
