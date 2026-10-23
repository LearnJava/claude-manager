package session

import (
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/store"
)

// LN-40 invariant 6: with no learned rules the argv is what it was before.
func TestBuildCLIArgs_LearnedRules(t *testing.T) {
	cfg := config.SessionConfig{Model: "sonnet", SystemPromptAppend: "Be terse."}
	base := New(Params{Config: cfg}).buildCLIArgs(false)
	if got := appendedPrompt(base); got != "Be terse." {
		t.Fatalf("appended prompt = %q", got)
	}
	if hasFlag(New(Params{Config: config.SessionConfig{Model: "sonnet"}}).buildCLIArgs(false), "--append-system-prompt") {
		t.Error("no rules and no append must keep the flag absent")
	}
	block := "## Learned from failures (Claude Manager)\n- Quote paths."
	args := New(Params{Config: cfg, LearnedRules: block}).buildCLIArgs(false)
	if got := appendedPrompt(args); got != "Be terse.\n\n"+block {
		t.Errorf("appended prompt = %q", got)
	}
	only := New(Params{Config: config.SessionConfig{Model: "sonnet"}, LearnedRules: block}).buildCLIArgs(false)
	if got := appendedPrompt(only); got != block {
		t.Errorf("rules alone: %q", got)
	}
}

func TestLearnedRulesFor(t *testing.T) {
	m, st := newTestManagerWithStore(t)
	proj := &m.cfg.Projects[0]
	add := func(key, text, status string) {
		now := time.Now().UTC()
		if err := st.InsertFailureRule(&store.FailureRule{Project: proj.Name, ErrorKey: key, RuleText: text, Status: status, DecidedAt: &now}); err != nil {
			t.Fatal(err)
		}
	}
	add("a", "Rule A.", store.FailureRuleTrial)
	add("b", "Rule B.", store.FailureRuleKept)
	add("c", "Rule C.", store.FailureRuleAccepted)
	add("d", "Rule D.", store.FailureRuleReverted)
	add("e", "Rule E.", store.FailureRuleDraft)

	if got := m.learnedRulesFor(proj); got != "" {
		t.Errorf("auto_skills off must add nothing, got %q", got)
	}
	proj.AutoSkills = true
	got := m.learnedRulesFor(proj)
	if !strings.HasPrefix(got, "## Learned from failures (Claude Manager)\n") ||
		!strings.Contains(got, "\n- Rule A.") || !strings.Contains(got, "\n- Rule B.") {
		t.Fatalf("block = %q", got)
	}
	for _, no := range []string{"Rule C.", "Rule D.", "Rule E."} {
		if strings.Contains(got, no) {
			t.Errorf("%s must not be injected: %q", no, got)
		}
	}

	m.cfg.Projects[0].Name = "other"
	if got := m.learnedRulesFor(&m.cfg.Projects[0]); got != "" {
		t.Errorf("project without trial/kept rules: %q", got)
	}
	if got := (&SessionManager{cfg: &config.AppConfig{}}).learnedRulesFor(proj); got != "" {
		t.Errorf("no store: %q", got)
	}
}
