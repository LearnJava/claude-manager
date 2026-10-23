package experience

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/store"
)

const ruleErrText = "fatal: boom"

type rulePilotFixture struct {
	t      *testing.T
	st     *store.Store
	now    time.Time
	drafts int
	dup    bool
	draftE error
	pilot  *SkillPilot
}

func newRulePilotFixture(t *testing.T) *rulePilotFixture {
	t.Helper()
	f := &rulePilotFixture{t: t, st: newTestStore(t), now: time.Now().UTC().Truncate(time.Second)}
	f.pilot = NewSkillPilot(SkillPilotDeps{
		Store: f.st,
		Now:   func() time.Time { return f.now },
		DraftRule: func(context.Context, string, analysis.FailureRuleInput) (*analysis.FailureRuleDraft, error) {
			f.drafts++
			if f.draftE != nil {
				return nil, f.draftE
			}
			return &analysis.FailureRuleDraft{Rule: "Quote the path before cd.", Duplicate: f.dup, CostUSD: 0.05}, nil
		},
		Candidates: func(*store.Store, string) ([]SkillCandidate, error) { return nil, nil },
	})
	return f
}

// run inserts one indexed run starting at ts with an optional boom error.
func (f *rulePilotFixture) run(ts time.Time, status string, hit bool) {
	f.t.Helper()
	r := &store.SessionRun{Project: "proj", Session: "S", StartedAt: ts, Status: status}
	if err := f.st.InsertRun(r); err != nil {
		f.t.Fatal(err)
	}
	row := store.ActionRow{Project: "proj", Session: "S", RunID: &r.ID, CLISessionID: "c", Tool: "Bash",
		Sig: "Bash:x", Arg: "x", Timestamp: ts, IsError: hit}
	if hit {
		row.ErrText = ruleErrText
	}
	if err := f.st.InsertActions([]store.ActionRow{row}); err != nil {
		f.t.Fatal(err)
	}
}

// before seeds n runs ending before `at`, the first `hits` of which hit.
func (f *rulePilotFixture) before(at time.Time, n, hits int, status string) {
	for i := 0; i < n; i++ {
		f.run(at.Add(-time.Duration(i+1)*time.Hour), status, i < hits)
	}
}

func (f *rulePilotFixture) after(at time.Time, n, hits int, status string) {
	for i := 0; i < n; i++ {
		f.run(at.Add(time.Duration(i+1)*time.Minute), status, i < hits)
	}
}

func (f *rulePilotFixture) step() SkillPilotReport {
	f.t.Helper()
	var rep SkillPilotReport
	if err := f.pilot.rulesStep(context.Background(), SkillPilotParams{Project: "proj"}, &rep); err != nil {
		f.t.Fatalf("rulesStep: %v", err)
	}
	return rep
}

func (f *rulePilotFixture) insertTrial(decided time.Time) int64 {
	f.t.Helper()
	r := &store.FailureRule{Project: "proj", ErrorKey: ErrorKey(ruleErrText), RuleText: "Quote the path.",
		Status: store.FailureRuleTrial, Origin: store.FailureRuleOriginAuto, DecidedAt: &decided, CreatedAt: decided}
	if err := f.st.InsertFailureRule(r); err != nil {
		f.t.Fatal(err)
	}
	return r.ID
}

func (f *rulePilotFixture) rule(id int64) *store.FailureRule {
	f.t.Helper()
	r, err := f.st.GetFailureRule(id)
	if err != nil || r == nil {
		f.t.Fatalf("GetFailureRule = %v, %v", r, err)
	}
	return r
}

func TestRulesStep_AppliesNewCandidateOneAtATime(t *testing.T) {
	f := newRulePilotFixture(t)
	f.before(f.now, 5, 4, "completed")
	rep := f.step()
	if f.drafts != 1 || len(rep.RulesApplied) != 1 {
		t.Fatalf("drafts = %d, applied = %v", f.drafts, rep.RulesApplied)
	}
	rules, _ := f.st.ListFailureRules("proj")
	if len(rules) != 1 || rules[0].Status != store.FailureRuleTrial || rules[0].Origin != store.FailureRuleOriginAuto || rules[0].DecidedAt == nil {
		t.Fatalf("rules = %+v", rules)
	}
	f.step() // a trial is running: no second draft
	if f.drafts != 1 {
		t.Errorf("a new rule was drafted during a trial (%d calls)", f.drafts)
	}
}

func TestRulesStep_ReadyDraftBeforeLLM(t *testing.T) {
	f := newRulePilotFixture(t)
	d := &store.FailureRule{Project: "proj", ErrorKey: "k", RuleText: "Use forward slashes.", Status: store.FailureRuleDraft, CreatedAt: f.now}
	if err := f.st.InsertFailureRule(d); err != nil {
		t.Fatal(err)
	}
	rep := f.step()
	if f.drafts != 0 || len(rep.RulesApplied) != 1 {
		t.Fatalf("drafts = %d, applied = %v", f.drafts, rep.RulesApplied)
	}
	if got := f.rule(d.ID); got.Status != store.FailureRuleTrial || got.DecidedAt == nil {
		t.Errorf("draft not applied: %+v", got)
	}
}

func TestRulesStep_SkipsErrorThatStoppedRecurring(t *testing.T) {
	f := newRulePilotFixture(t)
	f.before(f.now.Add(-30*24*time.Hour), 3, 3, "completed") // old hits
	f.before(f.now, 20, 0, "completed")                      // the last 20 runs are clean
	f.step()
	if f.drafts != 0 {
		t.Errorf("drafted a rule for an error with no recent hits")
	}
}

func TestRulesStep_DuplicateIsDismissed(t *testing.T) {
	f := newRulePilotFixture(t)
	f.dup = true
	f.before(f.now, 5, 5, "completed")
	rep := f.step()
	rules, _ := f.st.ListFailureRules("proj")
	if len(rules) != 1 || rules[0].Status != store.FailureRuleDismissed || len(rep.RulesApplied) != 0 {
		t.Fatalf("rules = %+v, rep = %+v", rules, rep)
	}
}

func TestRulesStep_TrialDecision(t *testing.T) {
	cases := []struct {
		name        string
		hitsAfter   int
		statusAfter string
		want, code  string
	}{
		{"kept", 0, "completed", store.FailureRuleKept, ""},
		{"no effect", 4, "completed", store.FailureRuleReverted, RuleReasonNoEffect},
		{"worse", 0, "error", store.FailureRuleReverted, RuleReasonWorse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRulePilotFixture(t)
			decided := f.now.Add(-2 * time.Hour)
			f.before(decided, 10, 4, "completed")
			f.after(decided, FailureRuleTrialRuns, tc.hitsAfter, tc.statusAfter)
			id := f.insertTrial(decided)
			rep := f.step()
			got := f.rule(id)
			if got.Status != tc.want {
				t.Fatalf("status = %s, want %s (rep %+v)", got.Status, tc.want, rep)
			}
			if tc.code != "" {
				var r FailureRuleReason
				if err := json.Unmarshal([]byte(got.Reason), &r); err != nil || r.Code != tc.code || r.RunsAfter != FailureRuleTrialRuns {
					t.Errorf("reason = %q (%v)", got.Reason, err)
				}
				if len(rep.RulesReverted) != 1 {
					t.Errorf("rep = %+v", rep)
				}
			} else if len(rep.RulesKept) != 1 {
				t.Errorf("rep = %+v", rep)
			}
		})
	}
}

func TestRulesStep_TrialWaitsForRuns(t *testing.T) {
	f := newRulePilotFixture(t)
	decided := f.now.Add(-2 * time.Hour)
	f.before(decided, 10, 4, "completed")
	f.after(decided, FailureRuleTrialRuns-1, 0, "completed")
	id := f.insertTrial(decided)
	f.step()
	if got := f.rule(id); got.Status != store.FailureRuleTrial {
		t.Errorf("status = %s, want trial", got.Status)
	}
}

func TestTick_RuleSpendStopsSkillDistillation(t *testing.T) {
	f := newPilotFixture(t)
	f.pilot.deps.Now = func() time.Time { return f.now }
	row := &store.FailureRule{Project: "proj", ErrorKey: "k", RuleText: "r", Status: store.FailureRuleKept,
		Origin: store.FailureRuleOriginAuto, CostUSD: 5, CreatedAt: f.now, DecidedAt: &f.now}
	if err := f.st.InsertFailureRule(row); err != nil {
		t.Fatal(err)
	}
	rep, err := f.pilot.Tick(context.Background(), SkillPilotParams{Project: "proj", ProjectPath: f.dir, DailyBudgetUSD: 1})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Idle != "budget" || f.distills != 0 {
		t.Errorf("idle = %q, distills = %d; rule spend must stop the skill distillation", rep.Idle, f.distills)
	}
}

func TestTick_DraftRuleErrorDoesNotBlockSkills(t *testing.T) {
	f := newPilotFixture(t)
	boom := errors.New("draft boom")
	f.pilot.deps.DraftRule = func(context.Context, string, analysis.FailureRuleInput) (*analysis.FailureRuleDraft, error) {
		return nil, boom
	}
	rf := &rulePilotFixture{t: t, st: f.st, now: time.Now().UTC()}
	rf.before(rf.now, 5, 5, "completed")
	f.pilot.deps.Now = func() time.Time { return rf.now }
	rep, err := f.pilot.Tick(context.Background(), SkillPilotParams{Project: "proj", ProjectPath: f.dir})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the DraftRule error", err)
	}
	if f.distills != 1 || len(rep.Applied) != 1 {
		t.Errorf("distills = %d, applied = %v; skills must still run", f.distills, rep.Applied)
	}
}
