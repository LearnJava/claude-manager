package experience

import (
	"context"
	"encoding/json"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/store"
)

const (
	// FailureRuleTrialRuns is how many runs after the application a trial
	// rule is measured over before it is kept or reverted.
	FailureRuleTrialRuns = 10
	// MaxInjectedRules caps the rules (trial + kept) in a session's prompt.
	MaxInjectedRules = 10
	// MinRecentHits is how many of the last runs must have hit an error for it
	// to be worth a rule: an error that stopped recurring cannot be measured.
	MinRecentHits = 2
	// completedDropLimit: a trial whose share of completed runs fell by this
	// much is "worse" and reverted.
	completedDropLimit = 0.2

	RuleReasonNoEffect = "no_effect"
	RuleReasonWorse    = "worse"
)

// FailureRuleReason is the JSON stored in failure_rules.reason for a rule the
// autopilot decided on.
type FailureRuleReason struct {
	Code         string `json:"code"`
	Text         string `json:"text,omitempty"`
	HitsBefore   int    `json:"hits_before"`
	RunsBefore   int    `json:"runs_before"`
	HitsAfter    int    `json:"hits_after"`
	RunsAfter    int    `json:"runs_after"`
	CompletedBef int    `json:"completed_before"`
	CompletedAft int    `json:"completed_after"`
}

func newFailureRuleReason(code string, e FailureEffect) string {
	b, _ := json.Marshal(FailureRuleReason{
		Code: code, HitsBefore: e.HitsBefore, RunsBefore: e.RunsBefore,
		HitsAfter: e.HitsAfter, RunsAfter: e.RunsAfter,
		CompletedBef: e.CompletedBefore, CompletedAft: e.CompletedAfter,
	})
	return string(b)
}

func shortRule(s string) string {
	r := []rune(s)
	if len(r) > 80 {
		return string(r[:80])
	}
	return s
}

func rate(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) / float64(d)
}

// autoSpentToday sums what the autopilot spent today on this project: the
// cost of its auto skills and auto failure rules. One budget covers both.
func autoSpentToday(st *store.Store, project string, now time.Time) (float64, error) {
	spent := 0.0
	skills, err := st.ListSkills(project)
	if err != nil {
		return 0, err
	}
	for _, sk := range skills {
		if sk.Origin == store.SkillOriginAuto && sameDay(sk.CreatedAt, now) {
			spent += sk.CostUSD
		}
	}
	rules, err := st.ListFailureRules(project)
	if err != nil {
		return 0, err
	}
	for _, r := range rules {
		if r.Origin == store.FailureRuleOriginAuto && sameDay(r.CreatedAt, now) {
			spent += r.CostUSD
		}
	}
	return spent, nil
}

// rulesStep decides on a finished rule trial and, when none is running and
// the limits allow, applies one more rule (a ready draft first, else a freshly
// drafted one). Applying means status "trial": the rule goes into the system
// prompt of the project's Claude sessions; CLAUDE.md is never touched.
func (p *SkillPilot) rulesStep(ctx context.Context, params SkillPilotParams, rep *SkillPilotReport) error {
	st := p.deps.Store
	rules, err := st.ListFailureRules(params.Project)
	if err != nil {
		return err
	}
	now := p.deps.Now()

	trialing := false
	injected := 0
	for _, r := range rules {
		if r.Status != store.FailureRuleTrial {
			if r.Status == store.FailureRuleKept {
				injected++
			}
			continue
		}
		injected++
		eff, err := FailureRuleEffect(st, params.Project, r)
		if err != nil {
			return err
		}
		if eff.RunsAfter < FailureRuleTrialRuns {
			trialing = true
			continue
		}
		code, status := "", store.FailureRuleKept
		switch {
		case rate(eff.CompletedBefore, eff.RunsBefore)-rate(eff.CompletedAfter, eff.RunsAfter) >= completedDropLimit:
			code, status = RuleReasonWorse, store.FailureRuleReverted
		case rate(eff.HitsAfter, eff.RunsAfter)*2 > rate(eff.HitsBefore, eff.RunsBefore):
			code, status = RuleReasonNoEffect, store.FailureRuleReverted
		}
		if err := st.UpdateFailureRule(r.ID, status, r.RuleText, r.DecidedAt); err != nil {
			return err
		}
		if status == store.FailureRuleReverted {
			if err := st.SetFailureRuleReason(r.ID, newFailureRuleReason(code, eff)); err != nil {
				return err
			}
			rep.RulesReverted = append(rep.RulesReverted, shortRule(r.RuleText))
			injected--
		} else {
			rep.RulesKept = append(rep.RulesKept, shortRule(r.RuleText))
		}
	}
	// One experiment at a time: two rules mid-trial would blur both measures.
	if trialing || injected >= MaxInjectedRules {
		return nil
	}

	var lastAuto, draft *store.FailureRule
	for i := range rules {
		r := &rules[i]
		if r.Origin == store.FailureRuleOriginAuto && (lastAuto == nil || r.CreatedAt.After(lastAuto.CreatedAt)) {
			lastAuto = r
		}
		if r.Status == store.FailureRuleDraft && r.RuleText != "" && draft == nil {
			draft = r // newest first
		}
	}
	if lastAuto != nil {
		runs, err := indexedRuns(st, params.Project)
		if err != nil {
			return err
		}
		since := 0
		for _, r := range runs {
			if r.StartedAt.After(lastAuto.CreatedAt) {
				since++
			}
		}
		if since < AutoDistillGapRuns {
			return nil
		}
	}
	spent, err := autoSpentToday(st, params.Project, now)
	if err != nil {
		return err
	}
	if params.DailyBudgetUSD > 0 && spent >= params.DailyBudgetUSD {
		return nil
	}

	if draft != nil {
		if err := st.UpdateFailureRule(draft.ID, store.FailureRuleTrial, draft.RuleText, &now); err != nil {
			return err
		}
		rep.RulesApplied = append(rep.RulesApplied, shortRule(draft.RuleText))
		return nil
	}
	if p.deps.DraftRule == nil {
		return nil
	}

	cands, err := FailureRuleCandidates(st, params.Project)
	if err != nil {
		return err
	}
	for _, c := range cands {
		eff, err := failureEffectAt(st, params.Project, c.ErrorKey, now)
		if err != nil {
			return err
		}
		if eff.HitsBefore < MinRecentHits {
			continue
		}
		in := analysis.FailureRuleInput{ErrorKey: c.ErrorKey, DistinctRuns: c.DistinctRuns}
		for _, e := range c.Examples {
			in.Examples = append(in.Examples, analysis.FailureRuleExample{FailedArg: e.FailedArg, ErrText: e.ErrText, FixedArg: e.FixedArg})
		}
		d, err := p.deps.DraftRule(ctx, params.ProjectPath, in)
		if err != nil {
			return err
		}
		examples, _ := json.Marshal(c.Examples)
		row := &store.FailureRule{
			Project: params.Project, ErrorKey: c.ErrorKey, ExamplesJSON: string(examples),
			RuleText: d.Rule, DistinctRuns: c.DistinctRuns, CostUSD: d.CostUSD,
			CreatedAt: now, Origin: store.FailureRuleOriginAuto,
		}
		if d.Duplicate || d.Rule == "" {
			row.Status = store.FailureRuleDismissed
			return st.InsertFailureRule(row)
		}
		row.Status = store.FailureRuleTrial
		row.DecidedAt = &now
		if err := st.InsertFailureRule(row); err != nil {
			return err
		}
		rep.RulesApplied = append(rep.RulesApplied, shortRule(row.RuleText))
		return nil
	}
	return nil
}
