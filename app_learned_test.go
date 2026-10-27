package main

import (
	"testing"

	"claude-manager/internal/config"
	"claude-manager/internal/store"
)

func learnedApp(t *testing.T, enabled bool) (*App, string) {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.InsertFailureRule(&store.FailureRule{Project: "p", ErrorKey: "k", RuleText: "r", Status: store.FailureRuleKept}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	cfg := &config.AppConfig{Projects: []config.ProjectConfig{{Name: "p", Path: dir}}}
	cfg.Optimization.LearnedFiles = enabled
	return &App{store: st, cfg: cfg}, dir
}

func TestLearnedFiles_OffTouchesNothing(t *testing.T) {
	a, dir := learnedApp(t, false)
	if s, err := a.GetLearnedFiles("p"); err != nil || s.Enabled || len(s.Items) != 0 {
		t.Fatalf("GetLearnedFiles off = %+v, %v", s, err)
	}
	if _, err := a.ApplyLearnedFiles("p", []LearnedPick{{Kind: "rule", Key: "k"}}); err == nil {
		t.Fatal("ApplyLearnedFiles worked with the flag off")
	}
	if n := dirEntries(t, dir); n != 0 {
		t.Fatalf("%d entries created in the project with the flag off", n)
	}
}

func TestLearnedFiles_WriteOnlyOnTick(t *testing.T) {
	a, dir := learnedApp(t, true)
	s, err := a.GetLearnedFiles("p")
	if err != nil || !s.Enabled || len(s.Items) != 1 || s.Items[0].Action != "export_new" {
		t.Fatalf("GetLearnedFiles = %+v, %v", s, err)
	}
	if n := dirEntries(t, dir); n != 0 {
		t.Fatalf("looking created %d entries", n)
	}
	r, err := a.ApplyLearnedFiles("p", []LearnedPick{{Kind: "rule", Key: "k"}})
	if err != nil || len(r.Done) != 1 || len(r.Failed) != 0 {
		t.Fatalf("ApplyLearnedFiles = %+v, %v", r, err)
	}
	if s, _ = a.GetLearnedFiles("p"); len(s.Items) != 0 {
		t.Fatalf("still pending after export: %+v", s.Items)
	}
}
