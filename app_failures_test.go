package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/config"
	"claude-manager/internal/store"
)

func failuresApp(t *testing.T) (*App, string) {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dir := t.TempDir()
	now := time.Now().UTC()
	var rows []store.ActionRow
	for i := 1; i <= 3; i++ {
		cli := fmt.Sprintf("r%d", i)
		rows = append(rows,
			store.ActionRow{Project: "p", Session: "S", CLISessionID: cli, StepIndex: 0, Tool: "Bash", Sig: "Bash:x",
				Arg: "c", IsError: true, ErrText: "fatal: boom", Timestamp: now},
			store.ActionRow{Project: "p", Session: "S", CLISessionID: cli, StepIndex: 1, Tool: "Bash", Sig: "Bash:y",
				Arg: "d", IsError: true, ErrText: "no such thing", Timestamp: now})
	}
	if err := st.InsertActions(rows); err != nil {
		t.Fatal(err)
	}
	a := &App{store: st, cfg: &config.AppConfig{Projects: []config.ProjectConfig{{Name: "p", Path: dir}}}}
	return a, dir
}

func TestDraftFailureRules_DuplicateIsDismissed(t *testing.T) {
	a, _ := failuresApp(t)
	a.draftFailureRule = func(_ context.Context, _ string, in analysis.FailureRuleInput) (*analysis.FailureRuleDraft, error) {
		if len(in.Examples) == 0 {
			t.Errorf("no examples passed")
		}
		return &analysis.FailureRuleDraft{Rule: "Rule for " + in.ErrorKey, Duplicate: in.ErrorKey == "no such thing", CostUSD: 0.01}, nil
	}
	got, err := a.DraftFailureRules("p", 0)
	if err != nil || len(got) != 2 {
		t.Fatalf("drafted %+v, %v", got, err)
	}
	status := map[string]string{}
	for _, r := range got {
		status[r.ErrorKey] = r.Status
		if r.CostUSD != 0.01 || r.ID == 0 {
			t.Errorf("row %+v", r)
		}
	}
	if status["no such thing"] != store.FailureRuleDismissed || status["fatal: boom"] != store.FailureRuleDraft {
		t.Errorf("statuses %v", status)
	}
	st, err := a.GetFailureRules("p")
	if err != nil || len(st.Rules) != 2 || st.Pending != 0 {
		t.Fatalf("state %+v, %v", st, err)
	}
	if len(st.Rules[0].Examples) == 0 {
		t.Errorf("examples not decoded")
	}
}

func TestAcceptFailureRule_WritesClaudeMd(t *testing.T) {
	a, dir := failuresApp(t)
	a.draftFailureRule = func(_ context.Context, _ string, in analysis.FailureRuleInput) (*analysis.FailureRuleDraft, error) {
		return &analysis.FailureRuleDraft{Rule: "draft text"}, nil
	}
	rules, err := a.DraftFailureRules("p", 1)
	if err != nil || len(rules) != 1 {
		t.Fatal(rules, err)
	}
	if err := a.AcceptFailureRule(rules[0].ID, "  Edited rule.  "); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if !strings.Contains(string(data), "- Edited rule.") {
		t.Errorf("CLAUDE.md = %q", data)
	}
	r, _ := a.store.GetFailureRule(rules[0].ID)
	if r.Status != store.FailureRuleAccepted || r.RuleText != "Edited rule." || r.DecidedAt == nil {
		t.Errorf("row %+v", r)
	}
	if err := a.AcceptFailureRule(rules[0].ID, "again"); err == nil {
		t.Errorf("accepting twice must fail")
	}
}

func TestDismissFailureRule(t *testing.T) {
	a, dir := failuresApp(t)
	a.draftFailureRule = func(_ context.Context, _ string, in analysis.FailureRuleInput) (*analysis.FailureRuleDraft, error) {
		return &analysis.FailureRuleDraft{Rule: "x"}, nil
	}
	rules, _ := a.DraftFailureRules("p", 1)
	if err := a.DismissFailureRule(rules[0].ID); err != nil {
		t.Fatal(err)
	}
	r, _ := a.store.GetFailureRule(rules[0].ID)
	if r.Status != store.FailureRuleDismissed {
		t.Errorf("status %s", r.Status)
	}
	if _, err := os.Stat(filepath.Join(dir, "CLAUDE.md")); err == nil {
		t.Errorf("dismiss must not write CLAUDE.md")
	}
}
