package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/experience"
	"claude-manager/internal/store"
)

// defaultDraftFailureRules is how many candidates DraftFailureRules drafts
// when the caller passes limit <= 0.
const defaultDraftFailureRules = 3

// FailureRuleView is one failure_rules row for the "Failures" tab: the stored
// rule, the examples it was drafted from and, for an accepted rule, how often
// its error showed up before and after the decision (LEARN-TASKS.md LN-38).
type FailureRuleView struct {
	store.FailureRule
	Examples []experience.ErrorExample
	Effect   experience.FailureEffect
	// Why is the parsed Reason of a decided autopilot rule (nil otherwise).
	Why *experience.FailureRuleReason
	// TrialRuns is how many runs a trial lasts (experience.FailureRuleTrialRuns).
	TrialRuns int
}

// FailureRulesState is GetFailureRules' answer: the project's rules plus how
// many recurring errors have no rule row yet.
type FailureRulesState struct {
	Rules   []FailureRuleView
	Pending int
}

// GetFailureRules lists a project's failure rules (newest first, with the
// effect of the accepted ones) and counts the recurring errors still waiting
// for a draft. Empty without enough history.
func (a *App) GetFailureRules(project string) (*FailureRulesState, error) {
	if a.store == nil {
		return nil, fmt.Errorf("no store")
	}
	rows, err := a.store.ListFailureRules(project)
	if err != nil {
		return nil, err
	}
	st := &FailureRulesState{Rules: make([]FailureRuleView, 0, len(rows))}
	for _, r := range rows {
		v := FailureRuleView{FailureRule: r, Why: experience.ParseFailureRuleReason(r.Reason), TrialRuns: experience.FailureRuleTrialRuns}
		if r.ExamplesJSON != "" {
			_ = json.Unmarshal([]byte(r.ExamplesJSON), &v.Examples)
		}
		if v.Effect, err = experience.FailureRuleEffect(a.store, project, r); err != nil {
			return nil, err
		}
		st.Rules = append(st.Rules, v)
	}
	pending, err := experience.FailureRuleCandidates(a.store, project)
	if err != nil {
		return nil, err
	}
	st.Pending = len(pending)
	return st, nil
}

// draftFailureRuleModel runs the drafting model; a test replaces it.
func (a *App) draftFailureRuleModel(ctx context.Context, projectPath string, in analysis.FailureRuleInput) (*analysis.FailureRuleDraft, error) {
	if a.draftFailureRule != nil {
		return a.draftFailureRule(ctx, projectPath, in)
	}
	return analysis.DraftFailureRule(ctx, projectPath, in, a.analysisConfig(""))
}

// DraftFailureRules drafts a one-line rule for the first limit (<= 0 → 3)
// recurring errors without a rule and stores each as a draft. A draft the
// model calls a duplicate of what CLAUDE.md already says is stored as
// dismissed, so the error is not offered again. Spends tokens; writes nothing
// into the project.
func (a *App) DraftFailureRules(project string, limit int) ([]store.FailureRule, error) {
	if a.store == nil {
		return nil, fmt.Errorf("no store")
	}
	if limit <= 0 {
		limit = defaultDraftFailureRules
	}
	projectPath, err := a.projectPath(project)
	if err != nil {
		return nil, err
	}
	cands, err := experience.FailureRuleCandidates(a.store, project)
	if err != nil {
		return nil, err
	}
	if len(cands) > limit {
		cands = cands[:limit]
	}
	out := make([]store.FailureRule, 0, len(cands))
	for _, c := range cands {
		in := analysis.FailureRuleInput{ErrorKey: c.ErrorKey, DistinctRuns: c.DistinctRuns}
		for _, e := range c.Examples {
			in.Examples = append(in.Examples, analysis.FailureRuleExample{FailedArg: e.FailedArg, ErrText: e.ErrText, FixedArg: e.FixedArg})
		}
		draft, err := a.draftFailureRuleModel(context.Background(), projectPath, in)
		if err != nil {
			return out, err
		}
		examples, _ := json.Marshal(c.Examples)
		row := store.FailureRule{
			Project: project, ErrorKey: c.ErrorKey, ExamplesJSON: string(examples),
			RuleText: draft.Rule, Status: store.FailureRuleDraft,
			DistinctRuns: c.DistinctRuns, CostUSD: draft.CostUSD,
		}
		if draft.Duplicate {
			now := time.Now().UTC()
			row.Status = store.FailureRuleDismissed
			row.DecidedAt = &now
		}
		if err := a.store.InsertFailureRule(&row); err != nil {
			return out, err
		}
		out = append(out, row)
	}
	return out, nil
}

// AcceptFailureRule appends the rule (possibly edited by the user) to the
// project's CLAUDE.md and marks it accepted. This is the only place the layer
// writes into a user's repository, and only on an explicit "Accept".
func (a *App) AcceptFailureRule(id int64, ruleText string) error {
	if a.store == nil {
		return fmt.Errorf("no store")
	}
	r, err := a.store.GetFailureRule(id)
	if err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("failure rule %d not found", id)
	}
	// A draft, or a rule the autopilot applies (trial/kept): moving it into
	// CLAUDE.md takes it out of the system prompt, so it is not stated twice.
	switch r.Status {
	case store.FailureRuleDraft, store.FailureRuleTrial, store.FailureRuleKept:
	default:
		return fmt.Errorf("failure rule %d is %s, only a draft or an applied rule can be accepted", id, r.Status)
	}
	ruleText = strings.TrimSpace(ruleText)
	if ruleText == "" {
		return fmt.Errorf("the rule text is empty")
	}
	projectPath, err := a.projectPath(r.Project)
	if err != nil {
		return err
	}
	if err := experience.AppendClaudeMdRule(projectPath, ruleText); err != nil {
		return err
	}
	now := time.Now().UTC()
	return a.store.UpdateFailureRule(id, store.FailureRuleAccepted, ruleText, &now)
}

// DismissFailureRule turns a draft down; the error is not proposed again.
func (a *App) DismissFailureRule(id int64) error {
	if a.store == nil {
		return fmt.Errorf("no store")
	}
	r, err := a.store.GetFailureRule(id)
	if err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("failure rule %d not found", id)
	}
	if r.Status != store.FailureRuleDraft {
		return fmt.Errorf("failure rule %d is %s, only a draft can be dismissed", id, r.Status)
	}
	now := time.Now().UTC()
	return a.store.UpdateFailureRule(id, store.FailureRuleDismissed, r.RuleText, &now)
}

// RevertFailureRule switches an autopilot rule (trial or kept) off: it leaves
// the system prompt of the project's sessions at their next start.
func (a *App) RevertFailureRule(id int64) error {
	return a.decideAppliedRule(id, "revert", []string{store.FailureRuleTrial, store.FailureRuleKept},
		store.FailureRuleReverted, experience.RuleReasonRevertedManual)
}

// RestoreFailureRule brings a reverted rule back as kept: the user's explicit
// choice, so no new trial.
func (a *App) RestoreFailureRule(id int64) error {
	return a.decideAppliedRule(id, "restore", []string{store.FailureRuleReverted},
		store.FailureRuleKept, experience.RuleReasonRestoredManual)
}

func (a *App) decideAppliedRule(id int64, verb string, from []string, to, code string) error {
	if a.store == nil {
		return fmt.Errorf("no store")
	}
	r, err := a.store.GetFailureRule(id)
	if err != nil {
		return err
	}
	if r == nil {
		return fmt.Errorf("failure rule %d not found", id)
	}
	ok := false
	for _, s := range from {
		ok = ok || r.Status == s
	}
	if !ok {
		return fmt.Errorf("failure rule %d is %s, cannot %s it", id, r.Status, verb)
	}
	eff, err := experience.FailureRuleEffect(a.store, r.Project, *r)
	if err != nil {
		return err
	}
	if err := a.store.UpdateFailureRule(id, to, r.RuleText, r.DecidedAt); err != nil {
		return err
	}
	return a.store.SetFailureRuleReason(id, experience.NewFailureRuleReason(code, eff))
}
