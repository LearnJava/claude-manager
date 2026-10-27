package experience

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/store"
)

// hookRun inserts one run starting at ts with `steps` Bash steps, `errs` of them failed.
func hookRun(t *testing.T, st *store.Store, ts time.Time, steps, errs int) {
	t.Helper()
	r := &store.SessionRun{Project: "proj", Session: "S", StartedAt: ts, Status: "completed"}
	if err := st.InsertRun(r); err != nil {
		t.Fatal(err)
	}
	var rows []store.ActionRow
	for i := 0; i < steps; i++ {
		row := store.ActionRow{Project: "proj", Session: "S", RunID: &r.ID, CLISessionID: "c" + ts.Format("150405"),
			StepIndex: i, Tool: "Bash", Sig: "Bash:x", Arg: "x", Timestamp: ts.Add(time.Duration(i) * time.Second), IsError: i < errs}
		if row.IsError {
			row.ErrText = "boom"
		}
		rows = append(rows, row)
	}
	if err := st.InsertActions(rows); err != nil {
		t.Fatal(err)
	}
}

// trialFixture: a repo with an installed repeat_read hook on trial since `at`,
// ten baseline runs before it.
func trialFixture(t *testing.T, beforeSteps, beforeErrs int) (st *store.Store, dir string, at time.Time) {
	t.Helper()
	st = newTestStore(t)
	dir = t.TempDir()
	at = time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	for i := 0; i < 10; i++ {
		hookRun(t, st, at.Add(-time.Duration(i+1)*time.Hour), beforeSteps, beforeErrs)
	}
	rule := &store.HookRule{Project: "proj", Kind: HookKindRepeatRead, Status: store.HookRuleTrial, DecidedAt: &at}
	if err := st.InsertHookRule(rule); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanHook(dir, []store.HookRule{*rule})
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyHookPlan(dir, plan); err != nil {
		t.Fatal(err)
	}
	return st, dir, at
}

func scriptExists(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, filepath.FromSlash(HookScriptRel)))
	return err == nil
}

func TestTrialThatMadeRunsLongerIsTakenOutAndFilesRemoved(t *testing.T) {
	st, dir, at := trialFixture(t, 10, 0)
	for i := 0; i < HookRuleTrialRuns; i++ {
		hookRun(t, st, at.Add(time.Duration(i+1)*time.Minute), 20, 0) // twice as many steps
	}
	changes, err := EvaluateHookTrials(st, "proj", dir)
	if err != nil || len(changes) != 1 || changes[0].Kept {
		t.Fatalf("changes = %+v, err = %v; want one reverted rule", changes, err)
	}
	if scriptExists(dir) {
		t.Error("reverted rule left its script in the repository")
	}
	if _, err := os.Stat(filepath.Join(dir, ".claude")); err == nil {
		t.Error("revert left the folder it created")
	}
	rules, _ := st.ListHookRules("proj")
	if rules[0].Status != store.HookRuleReverted {
		t.Errorf("status = %s", rules[0].Status)
	}
	if why := ParseHookRuleReason(rules[0].Reason); why == nil || why.Code != HookReasonWorse || why.Effect.StepsAfter != 20 {
		t.Errorf("reason = %+v", why)
	}
}

func TestTrialThatMadeMoreErrorsIsTakenOut(t *testing.T) {
	st, dir, at := trialFixture(t, 10, 1)
	for i := 0; i < HookRuleTrialRuns; i++ {
		hookRun(t, st, at.Add(time.Duration(i+1)*time.Minute), 10, 4)
	}
	changes, _ := EvaluateHookTrials(st, "proj", dir)
	if len(changes) != 1 || changes[0].Kept {
		t.Fatalf("changes = %+v; more errors per run must revert", changes)
	}
}

func TestTrialThatHelpedIsKeptAndStaysInstalled(t *testing.T) {
	st, dir, at := trialFixture(t, 10, 1)
	for i := 0; i < HookRuleTrialRuns; i++ {
		hookRun(t, st, at.Add(time.Duration(i+1)*time.Minute), 7, 0)
	}
	changes, _ := EvaluateHookTrials(st, "proj", dir)
	if len(changes) != 1 || !changes[0].Kept || !scriptExists(dir) {
		t.Fatalf("changes = %+v, script = %v; want kept and installed", changes, scriptExists(dir))
	}
}

func TestTrialNotLongEnoughIsLeftAlone(t *testing.T) {
	st, dir, at := trialFixture(t, 10, 0)
	for i := 0; i < HookRuleTrialRuns-1; i++ {
		hookRun(t, st, at.Add(time.Duration(i+1)*time.Minute), 50, 9)
	}
	changes, _ := EvaluateHookTrials(st, "proj", dir)
	if len(changes) != 0 || !scriptExists(dir) {
		t.Errorf("a trial of %d runs must not be decided: %+v", HookRuleTrialRuns-1, changes)
	}
}

func TestRevertingOneRuleKeepsTheOthersInstalled(t *testing.T) {
	st, dir, at := trialFixture(t, 10, 0)
	other := &store.HookRule{Project: "proj", Kind: HookKindEmptySearch, Status: store.HookRuleKept, DecidedAt: &at}
	if err := st.InsertHookRule(other); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < HookRuleTrialRuns; i++ {
		hookRun(t, st, at.Add(time.Duration(i+1)*time.Minute), 30, 0)
	}
	if _, err := EvaluateHookTrials(st, "proj", dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(HookScriptRel)))
	if err != nil || !strings.Contains(string(b), `"kind":"`+HookKindEmptySearch) || strings.Contains(string(b), `"kind":"`+HookKindRepeatRead) {
		t.Errorf("script after reverting repeat_read: %v\n%.300s", err, b)
	}
}

func TestHookEffectWorse(t *testing.T) {
	cases := []struct {
		name string
		e    HookEffect
		want bool
	}{
		{"no runs", HookEffect{}, false},
		{"same", HookEffect{RunsBefore: 5, RunsAfter: 5, StepsBefore: 10, StepsAfter: 10}, false},
		{"longer", HookEffect{RunsBefore: 5, RunsAfter: 5, StepsBefore: 10, StepsAfter: 12}, true},
		{"noise on near-zero errors", HookEffect{RunsBefore: 5, RunsAfter: 5, StepsBefore: 10, StepsAfter: 10, ErrorsBefore: 0, ErrorsAfter: 0.4}, false},
		{"more errors", HookEffect{RunsBefore: 5, RunsAfter: 5, StepsBefore: 10, StepsAfter: 10, ErrorsBefore: 1, ErrorsAfter: 3}, true},
	}
	for _, c := range cases {
		if got := c.e.Worse(); got != c.want {
			t.Errorf("%s: Worse = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFailedCommandsNeedsRepeatsAndAFix(t *testing.T) {
	var rows []store.ActionRow
	row := func(run string, step int, arg string, isErr bool) store.ActionRow {
		return store.ActionRow{CLISessionID: run, StepIndex: step, Tool: "Bash", Sig: "Bash:" + arg, Arg: arg, IsError: isErr, ErrText: map[bool]string{true: "exit 1"}[isErr]}
	}
	for _, run := range []string{"a", "b", "c"} {
		rows = append(rows, row(run, 0, "go test ./...", true), row(run, 1, "go test ./internal/...", false))
	}
	rows = append(rows, row("d", 0, "make x", true), row("d", 1, "make y", false)) // one run only
	rows = append(rows, row("e", 0, "ls nope", true))                               // never fixed
	got := failedCommands(rows)
	if len(got) != 1 || got[0].failed != "go test ./..." || got[0].fixed != "go test ./internal/..." {
		t.Errorf("failedCommands = %+v", got)
	}
}

func TestHookRuleCandidatesSkipKnownAndOnlyOccurredKinds(t *testing.T) {
	st := newTestStore(t)
	now := time.Now().UTC()
	// A run that reads the same file twice → repeat_read waste.
	r := &store.SessionRun{Project: "proj", Session: "S", StartedAt: now, Status: "completed"}
	if err := st.InsertRun(r); err != nil {
		t.Fatal(err)
	}
	mk := func(i int) store.ActionRow {
		return store.ActionRow{Project: "proj", Session: "S", RunID: &r.ID, CLISessionID: "c", StepIndex: i,
			Tool: "Read", Sig: "Read:a", Arg: "/a.go", Timestamp: now.Add(time.Duration(i) * time.Second), ResultChars: 4000}
	}
	if err := st.InsertActions([]store.ActionRow{mk(0), mk(1)}); err != nil {
		t.Fatal(err)
	}
	c, err := HookRuleCandidates(st, "proj", WasteThresholds{})
	if err != nil || len(c) != 1 || c[0].Kind != HookKindRepeatRead || c[0].Project != "proj" {
		t.Fatalf("candidates = %+v, %v; want exactly repeat_read", c, err)
	}
	if err := st.InsertHookRule(&c[0]); err != nil {
		t.Fatal(err)
	}
	c, _ = HookRuleCandidates(st, "proj", WasteThresholds{})
	if len(c) != 0 {
		t.Errorf("a rule that already has a row (any status) was proposed again: %+v", c)
	}
}

func TestPilotTickIgnoresHookRulesWhenOff(t *testing.T) {
	st, dir, at := trialFixture(t, 10, 0)
	for i := 0; i < HookRuleTrialRuns; i++ {
		hookRun(t, st, at.Add(time.Duration(i+1)*time.Minute), 30, 0)
	}
	p := NewSkillPilot(SkillPilotDeps{Store: st, Candidates: func(*store.Store, string) ([]SkillCandidate, error) { return nil, nil }})
	rep, err := p.Tick(t.Context(), SkillPilotParams{Project: "proj", ProjectPath: dir})
	if err != nil || len(rep.HookRulesReverted)+len(rep.HookRulesKept) != 0 || !scriptExists(dir) {
		t.Fatalf("flag off must leave the trial and the files alone: %+v %v", rep, err)
	}
	rep, err = p.Tick(t.Context(), SkillPilotParams{Project: "proj", ProjectPath: dir, HookRules: true})
	if err != nil || len(rep.HookRulesReverted) != 1 || scriptExists(dir) || !rep.Changed() {
		t.Fatalf("flag on must decide the trial: %+v %v", rep, err)
	}
}
