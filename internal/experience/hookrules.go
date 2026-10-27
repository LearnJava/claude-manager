package experience

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"claude-manager/internal/store"
)

const (
	// HookRuleTrialRuns is how many runs after the install a hook rule is
	// measured over before it is kept or taken out.
	HookRuleTrialRuns = 10
	// hookWorseSteps: a trial whose runs got this much longer (steps per run)
	// is "worse" and taken out.
	hookWorseSteps = 1.1
	// hookWorseErrors: same for errors per run, and by at least one whole error
	// per run so a baseline of nearly zero does not trip on noise.
	hookWorseErrors = 1.25

	HookReasonWorse = "worse"
	HookReasonKept  = "kept"
	// HookReasonManual is a rule the user took out by hand.
	HookReasonManual = "reverted_manual"

	maxHookParam = 300
)

// HookRuleReason is the JSON stored in hook_rules.reason for a decided rule.
type HookRuleReason struct {
	Code   string     `json:"code"`
	Effect HookEffect `json:"effect"`
}

// HookEffect compares the runs before a hook rule went in with the runs after.
// Steps and Errors are per-run averages.
type HookEffect struct {
	RunsBefore   int     `json:"runs_before"`
	RunsAfter    int     `json:"runs_after"`
	StepsBefore  float64 `json:"steps_before"`
	StepsAfter   float64 `json:"steps_after"`
	ErrorsBefore float64 `json:"errors_before"`
	ErrorsAfter  float64 `json:"errors_after"`
}

// Worse reports whether the runs got longer or more erroneous.
func (e HookEffect) Worse() bool {
	if e.RunsBefore == 0 || e.RunsAfter == 0 {
		return false
	}
	return e.StepsAfter > e.StepsBefore*hookWorseSteps ||
		(e.ErrorsAfter > e.ErrorsBefore*hookWorseErrors && e.ErrorsAfter-e.ErrorsBefore >= 1)
}

// ParseHookRuleReason reads a stored reason; nil when empty or malformed.
func ParseHookRuleReason(raw string) *HookRuleReason {
	if raw == "" {
		return nil
	}
	var r HookRuleReason
	if json.Unmarshal([]byte(raw), &r) != nil {
		return nil
	}
	return &r
}

// HookReasonJSON is the reason JSON for a decision with the measured effect e.
func HookReasonJSON(code string, e HookEffect) string {
	b, _ := json.Marshal(HookRuleReason{Code: code, Effect: e})
	return string(b)
}

// HookRuleEffect measures a hook rule installed at decidedAt: the last
// effectBeforeRuns runs that started before it versus every run after it.
func HookRuleEffect(st *store.Store, project string, decidedAt time.Time) (HookEffect, error) {
	var eff HookEffect
	rows, err := st.ActionRowsForCandidates(project, effectWindowDays)
	if err != nil {
		return eff, err
	}
	type runInfo struct {
		start  time.Time
		steps  int
		errors int
	}
	runs := make(map[string]*runInfo)
	for _, row := range rows {
		k := actionRunKey(row.ActionRow)
		ri, ok := runs[k]
		if !ok {
			ri = &runInfo{start: row.Timestamp}
			runs[k] = ri
		}
		if row.Timestamp.Before(ri.start) {
			ri.start = row.Timestamp
		}
		ri.steps++
		if row.IsError {
			ri.errors++
		}
	}
	var before, after []*runInfo
	for _, ri := range runs {
		if ri.start.Before(decidedAt) {
			before = append(before, ri)
		} else {
			after = append(after, ri)
		}
	}
	sort.Slice(before, func(i, j int) bool { return before[i].start.After(before[j].start) })
	if len(before) > effectBeforeRuns {
		before = before[:effectBeforeRuns]
	}
	avg := func(rs []*runInfo) (steps, errs float64) {
		for _, r := range rs {
			steps += float64(r.steps)
			errs += float64(r.errors)
		}
		if len(rs) > 0 {
			steps /= float64(len(rs))
			errs /= float64(len(rs))
		}
		return
	}
	eff.RunsBefore, eff.RunsAfter = len(before), len(after)
	eff.StepsBefore, eff.ErrorsBefore = avg(before)
	eff.StepsAfter, eff.ErrorsAfter = avg(after)
	return eff, nil
}

// ActiveHookRules are the rules in the project's hook: trial and kept ones,
// oldest first.
func ActiveHookRules(rules []store.HookRule) []store.HookRule {
	var out []store.HookRule
	for _, r := range rules {
		if r.Status == store.HookRuleTrial || r.Status == store.HookRuleKept {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// HookRuleCandidates proposes hook rules the project's own history justifies
// and that have no row yet (any status, so a refused one is not offered
// again): one per waste category that occurred, and one per command that
// failed in at least MinCandidateRuns runs and was later fixed by a different
// form. Read only; the caller stores them as drafts.
func HookRuleCandidates(st *store.Store, project string, th WasteThresholds) ([]store.HookRule, error) {
	th = th.withDefaults()
	existing, err := st.ListHookRules(project)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(existing))
	for _, r := range existing {
		known[r.Kind+"\x00"+r.Param] = true
	}
	var out []store.HookRule
	add := func(r store.HookRule) {
		if !known[r.Kind+"\x00"+r.Param] {
			known[r.Kind+"\x00"+r.Param] = true
			r.Project = project
			out = append(out, r)
		}
	}

	report, err := BuildWasteReport(st, project, th)
	if err != nil {
		return nil, err
	}
	for _, c := range report.Categories {
		switch c.Category {
		case WasteRepeatRead:
			add(store.HookRule{Kind: HookKindRepeatRead})
		case WasteEmptySearch:
			add(store.HookRule{Kind: HookKindEmptySearch})
		case WasteOversizedRead:
			add(store.HookRule{Kind: HookKindOversizedRead, Param: strconv.Itoa(th.OversizedReadTokens)})
		}
	}

	rows, err := st.ActionRowsForCandidates(project, candidateWindowDays)
	if err != nil {
		return nil, err
	}
	plain := make([]store.ActionRow, len(rows))
	for i, r := range rows {
		plain[i] = r.ActionRow
	}
	for _, p := range failedCommands(plain) {
		add(store.HookRule{Kind: HookKindFailureCmd, Param: p.failed, Hint: "use: " + p.fixed})
	}
	return out, nil
}

type failedCommand struct{ failed, fixed string }

// failedCommands finds Bash commands that failed (not as noise) in at least
// MinCandidateRuns distinct runs and were each time followed by a different,
// successful form. The most common fix wins; ties go to the earlier one.
func failedCommands(rows []store.ActionRow) []failedCommand {
	byRun := make(map[string][]store.ActionRow)
	for _, r := range rows {
		k := actionRunKey(r)
		byRun[k] = append(byRun[k], r)
	}
	type acc struct {
		runs  map[string]bool
		fixes map[string]int
	}
	groups := make(map[string]*acc)
	var order []string
	for rk, grp := range byRun {
		sort.SliceStable(grp, func(i, j int) bool { return grp[i].StepIndex < grp[j].StepIndex })
		for i, r := range grp {
			cmd := strings.TrimSpace(r.Arg)
			if r.Tool != "Bash" || !r.IsError || cmd == "" || len(cmd) > maxHookParam || isNoiseErr(r.ErrText) {
				continue
			}
			fixed := strings.TrimSpace(fixedArg(grp, i))
			if fixed == "" || fixed == cmd || len(fixed) > maxHookParam {
				continue
			}
			a, ok := groups[cmd]
			if !ok {
				a = &acc{runs: map[string]bool{}, fixes: map[string]int{}}
				groups[cmd] = a
				order = append(order, cmd)
			}
			a.runs[rk] = true
			a.fixes[fixed]++
		}
	}
	sort.Strings(order)
	var out []failedCommand
	for _, cmd := range order {
		a := groups[cmd]
		if len(a.runs) < MinCandidateRuns {
			continue
		}
		best, n := "", 0
		for f, c := range a.fixes {
			if c > n || (c == n && f < best) {
				best, n = f, c
			}
		}
		out = append(out, failedCommand{failed: cmd, fixed: best})
	}
	return out
}

// HookRuleChange is one decided trial: Rule is the row before the decision.
type HookRuleChange struct {
	Rule   store.HookRule
	Effect HookEffect
	Kept   bool
}

// EvaluateHookTrials decides on every trial rule that has run
// HookRuleTrialRuns runs: it is kept, or — if the runs got longer or more
// erroneous — taken out of the hook (the files are rewritten). Writing here
// only undoes what the user approved; nothing new is ever installed.
func EvaluateHookTrials(st *store.Store, project, projectPath string) ([]HookRuleChange, error) {
	rules, err := st.ListHookRules(project)
	if err != nil {
		return nil, err
	}
	var changes []HookRuleChange
	reverted := false
	for _, r := range rules {
		if r.Status != store.HookRuleTrial || r.DecidedAt == nil {
			continue
		}
		eff, err := HookRuleEffect(st, project, *r.DecidedAt)
		if err != nil {
			return changes, err
		}
		if eff.RunsAfter < HookRuleTrialRuns {
			continue
		}
		status, code := store.HookRuleKept, HookReasonKept
		if eff.Worse() {
			status, code = store.HookRuleReverted, HookReasonWorse
			reverted = true
		}
		if err := st.UpdateHookRule(r.ID, status, r.DecidedAt, HookReasonJSON(code, eff)); err != nil {
			return changes, err
		}
		changes = append(changes, HookRuleChange{Rule: r, Effect: eff, Kept: status == store.HookRuleKept})
	}
	if !reverted {
		return changes, nil
	}
	rules, err = st.ListHookRules(project)
	if err != nil {
		return changes, err
	}
	plan, err := PlanHook(projectPath, ActiveHookRules(rules))
	if err != nil {
		return changes, err
	}
	if err := ApplyHookPlan(projectPath, plan); err != nil {
		return changes, fmt.Errorf("remove reverted hook rule: %w", err)
	}
	return changes, nil
}
