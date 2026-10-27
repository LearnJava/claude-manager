package main

import (
	"fmt"
	"sync"
	"time"

	"claude-manager/internal/experience"
	"claude-manager/internal/store"
)

// hookRuleMu serializes the methods that read-modify-write a project's hook
// files, so a click racing the autopilot cannot leave the script and the
// hook_rules table disagreeing.
var hookRuleMu sync.Mutex

// HookRuleView is one hook_rules row for the Waste tab: the rule, how the
// runs after its install compare with those before, and — for a decided one —
// why.
type HookRuleView struct {
	store.HookRule
	Effect experience.HookEffect
	Why    *experience.HookRuleReason
}

// HookRulesState is GetHookRules' answer. Enabled mirrors [optimization]
// hook_rules; with it off the list is empty and nothing was touched.
type HookRulesState struct {
	Enabled   bool
	Rules     []HookRuleView
	TrialRuns int
}

func (a *App) hookRulesEnabled() error {
	if a.store == nil {
		return fmt.Errorf("no store")
	}
	if a.cfg == nil || !a.cfg.Optimization.HookRules {
		return fmt.Errorf("hook rules are off: set [optimization] hook_rules = true")
	}
	return nil
}

// GetHookRules lists a project's hook rules (newest first) with the effect of
// those installed. First it decides trials that have run long enough — a bad
// one is taken out of the project's files, which only undoes an approved
// install. Writes nothing when hook_rules is off (WASTE-TASKS.md WS-03).
func (a *App) GetHookRules(project string) (*HookRulesState, error) {
	out := &HookRulesState{TrialRuns: experience.HookRuleTrialRuns, Rules: []HookRuleView{}}
	if a.store == nil || a.cfg == nil || !a.cfg.Optimization.HookRules {
		return out, nil
	}
	out.Enabled = true
	hookRuleMu.Lock()
	defer hookRuleMu.Unlock()
	if path, err := a.projectPath(project); err == nil {
		if _, err := experience.EvaluateHookTrials(a.store, project, path); err != nil {
			return nil, err
		}
	}
	rules, err := a.store.ListHookRules(project)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		v := HookRuleView{HookRule: r, Why: experience.ParseHookRuleReason(r.Reason)}
		if r.DecidedAt != nil && r.Status != store.HookRuleDismissed {
			if v.Effect, err = experience.HookRuleEffect(a.store, project, *r.DecidedAt); err != nil {
				return nil, err
			}
		}
		out.Rules = append(out.Rules, v)
	}
	return out, nil
}

// ProposeHookRules stores the rules the project's history justifies as drafts
// (waste categories that occurred, commands that kept failing with a known
// right form) and returns the new ones. Read only for the repository: a draft
// is a database row, not a file.
func (a *App) ProposeHookRules(project string) ([]store.HookRule, error) {
	if err := a.hookRulesEnabled(); err != nil {
		return nil, err
	}
	var th experience.WasteThresholds
	th.EmptySearchChars = a.cfg.Optimization.WasteEmptySearchChars
	th.OversizedReadTokens = a.cfg.Optimization.WasteOversizedReadTokens
	hookRuleMu.Lock()
	defer hookRuleMu.Unlock()
	cands, err := experience.HookRuleCandidates(a.store, project, th)
	if err != nil {
		return nil, err
	}
	for i := range cands {
		cands[i].Status = store.HookRuleDraft
		if err := a.store.InsertHookRule(&cands[i]); err != nil {
			return nil, err
		}
	}
	return cands, nil
}

// hookRuleWithPath loads rule id and its project's folder.
func (a *App) hookRuleWithPath(id int64) (*store.HookRule, string, error) {
	r, err := a.store.GetHookRule(id)
	if err != nil {
		return nil, "", err
	}
	if r == nil {
		return nil, "", fmt.Errorf("hook rule %d not found", id)
	}
	path, err := a.projectPath(r.Project)
	if err != nil {
		return nil, "", err
	}
	return r, path, nil
}

// planWith is the plan for the project's active rules plus r (or minus it when
// remove is set).
func (a *App) planWith(r *store.HookRule, projectPath string, remove bool) (experience.HookPlan, error) {
	rules, err := a.store.ListHookRules(r.Project)
	if err != nil {
		return experience.HookPlan{}, err
	}
	var want []store.HookRule
	for _, x := range experience.ActiveHookRules(rules) {
		if x.ID != r.ID {
			want = append(want, x)
		}
	}
	if !remove {
		want = append(want, *r)
	}
	return experience.PlanHook(projectPath, want)
}

// PreviewHookRule shows what installing a draft would change in the project's
// repository, file by file as a diff. Writes nothing.
func (a *App) PreviewHookRule(id int64) (*experience.HookPlan, error) {
	if err := a.hookRulesEnabled(); err != nil {
		return nil, err
	}
	r, path, err := a.hookRuleWithPath(id)
	if err != nil {
		return nil, err
	}
	if r.Status != store.HookRuleDraft {
		return nil, fmt.Errorf("hook rule %d is %s, not a draft", id, r.Status)
	}
	plan, err := a.planWith(r, path, false)
	if err != nil {
		return nil, err
	}
	return &plan, nil
}

// ApplyHookRule installs a draft after the user approved its diff: the hook
// script and its registration are written into the project, and the rule goes
// on trial (measured over experience.HookRuleTrialRuns runs, taken out again
// if the runs got longer or more erroneous). One trial at a time.
func (a *App) ApplyHookRule(id int64) error {
	if err := a.hookRulesEnabled(); err != nil {
		return err
	}
	hookRuleMu.Lock()
	defer hookRuleMu.Unlock()
	r, path, err := a.hookRuleWithPath(id)
	if err != nil {
		return err
	}
	if r.Status != store.HookRuleDraft {
		return fmt.Errorf("hook rule %d is %s, not a draft", id, r.Status)
	}
	rules, err := a.store.ListHookRules(r.Project)
	if err != nil {
		return err
	}
	for _, x := range rules {
		if x.Status == store.HookRuleTrial {
			return fmt.Errorf("another hook rule (%s) is still on trial", x.Kind)
		}
	}
	plan, err := a.planWith(r, path, false)
	if err != nil {
		return err
	}
	if err := experience.ApplyHookPlan(path, plan); err != nil {
		return err
	}
	now := time.Now().UTC()
	return a.store.UpdateHookRule(id, store.HookRuleTrial, &now, "")
}

// RemoveHookRule refuses a draft (it is not offered again) or takes an
// installed rule out of the project's hook; the last rule out deletes the
// script and its registration.
func (a *App) RemoveHookRule(id int64) error {
	if err := a.hookRulesEnabled(); err != nil {
		return err
	}
	hookRuleMu.Lock()
	defer hookRuleMu.Unlock()
	r, path, err := a.hookRuleWithPath(id)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	switch r.Status {
	case store.HookRuleDraft:
		return a.store.UpdateHookRule(id, store.HookRuleDismissed, &now, "")
	case store.HookRuleTrial, store.HookRuleKept:
		plan, err := a.planWith(r, path, true)
		if err != nil {
			return err
		}
		if err := experience.ApplyHookPlan(path, plan); err != nil {
			return err
		}
		eff := experience.HookEffect{}
		if r.DecidedAt != nil {
			if eff, err = experience.HookRuleEffect(a.store, r.Project, *r.DecidedAt); err != nil {
				return err
			}
		}
		return a.store.UpdateHookRule(id, store.HookRuleReverted, r.DecidedAt, experience.HookReasonJSON(experience.HookReasonManual, eff))
	}
	return fmt.Errorf("hook rule %d is %s: nothing to remove", id, r.Status)
}
