package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/experience"
	"claude-manager/internal/store"
)

// hooksApp: a project whose history re-reads one file, so repeat_read is proposed.
func hooksApp(t *testing.T, enabled bool) (*App, string) {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dir := t.TempDir()
	now := time.Now().UTC()
	var rows []store.ActionRow
	for i := 0; i < 2; i++ {
		rows = append(rows, store.ActionRow{Project: "p", Session: "S", CLISessionID: "c", StepIndex: i,
			Tool: "Read", Sig: "Read:a", Arg: "/a.go", ResultChars: 4000, Timestamp: now})
	}
	if err := st.InsertActions(rows); err != nil {
		t.Fatal(err)
	}
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{Name: "p", Path: dir}}}
	cfg.Optimization.HookRules = enabled
	return &App{store: st, cfg: cfg}, dir
}

func dirEntries(t *testing.T, dir string) int {
	t.Helper()
	e, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(e)
}

func TestHookRules_OffTouchesNothing(t *testing.T) {
	a, dir := hooksApp(t, false)
	if st, err := a.GetHookRules("p"); err != nil || st.Enabled || len(st.Rules) != 0 {
		t.Fatalf("GetHookRules off = %+v, %v", st, err)
	}
	if _, err := a.ProposeHookRules("p"); err == nil {
		t.Error("ProposeHookRules must refuse when hook_rules is off")
	}
	if err := a.ApplyHookRule(1); err == nil {
		t.Error("ApplyHookRule must refuse when hook_rules is off")
	}
	if n := dirEntries(t, dir); n != 0 {
		t.Errorf("%d files appeared in the repository with the feature off", n)
	}
	if rules, _ := a.store.ListHookRules("p"); len(rules) != 0 {
		t.Errorf("rows were written with the feature off: %+v", rules)
	}
}

func TestHookRules_ProposeWritesNoFilesApplyNeedsDraft(t *testing.T) {
	a, dir := hooksApp(t, true)
	cands, err := a.ProposeHookRules("p")
	if err != nil || len(cands) != 1 || cands[0].Kind != experience.HookKindRepeatRead {
		t.Fatalf("propose = %+v, %v", cands, err)
	}
	if n := dirEntries(t, dir); n != 0 {
		t.Fatalf("proposing wrote %d files; a draft is a row, not a file", n)
	}
	if again, _ := a.ProposeHookRules("p"); len(again) != 0 {
		t.Errorf("same rule proposed twice: %+v", again)
	}

	plan, err := a.PreviewHookRule(cands[0].ID)
	if err != nil || len(plan.Files) != 2 || !strings.Contains(plan.Files[0].Diff, "+") {
		t.Fatalf("preview = %+v, %v; want a diff of script and settings", plan, err)
	}
	if n := dirEntries(t, dir); n != 0 {
		t.Fatalf("previewing wrote %d files", n)
	}

	if err := a.ApplyHookRule(cands[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude", "hooks", "cm-waste-guard.py")); err != nil {
		t.Errorf("approved rule did not install the script: %v", err)
	}
	if err := a.ApplyHookRule(cands[0].ID); err == nil {
		t.Error("an installed rule is not a draft; applying it again must fail")
	}
	state, _ := a.GetHookRules("p")
	if len(state.Rules) != 1 || state.Rules[0].Status != store.HookRuleTrial {
		t.Fatalf("state = %+v, want the rule on trial", state)
	}
}

func TestHookRules_RemoveRestoresRepositoryAndIsNotProposedAgain(t *testing.T) {
	a, dir := hooksApp(t, true)
	cands, _ := a.ProposeHookRules("p")
	if err := a.ApplyHookRule(cands[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := a.RemoveHookRule(cands[0].ID); err != nil {
		t.Fatal(err)
	}
	if n := dirEntries(t, dir); n != 0 {
		t.Errorf("removing the last rule left %d entries in the repository", n)
	}
	r, _ := a.store.GetHookRule(cands[0].ID)
	if r.Status != store.HookRuleReverted {
		t.Errorf("status = %s", r.Status)
	}
	if again, _ := a.ProposeHookRules("p"); len(again) != 0 {
		t.Errorf("a removed rule came back as a proposal: %+v", again)
	}
}

func TestHookRules_DismissDraftAndOneTrialAtATime(t *testing.T) {
	a, _ := hooksApp(t, true)
	first := &store.HookRule{Project: "p", Kind: experience.HookKindRepeatRead}
	second := &store.HookRule{Project: "p", Kind: experience.HookKindEmptySearch}
	for _, r := range []*store.HookRule{first, second} {
		if err := a.store.InsertHookRule(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.ApplyHookRule(first.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.ApplyHookRule(second.ID); err == nil || !strings.Contains(err.Error(), "trial") {
		t.Errorf("a second rule while one is on trial: %v", err)
	}
	if err := a.RemoveHookRule(second.ID); err != nil {
		t.Fatal(err)
	}
	if r, _ := a.store.GetHookRule(second.ID); r.Status != store.HookRuleDismissed {
		t.Errorf("removing a draft = %s, want dismissed", r.Status)
	}
}
