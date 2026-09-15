package experience

import (
	"fmt"
	"math"
	"testing"
	"time"

	"claude-manager/internal/store"
)

// row builds a synthetic action_signatures row for candidate mining tests —
// only the fields MineCandidates reads (Sig/Tool/Arg/ResultChars/StepIndex/
// Timestamp) need to be realistic.
func row(sig, tool, arg string, stepIndex, resultChars int, ts time.Time) store.ActionRow {
	return store.ActionRow{
		Sig:         sig,
		Tool:        tool,
		Arg:         arg,
		StepIndex:   stepIndex,
		ResultChars: resultChars,
		Timestamp:   ts,
	}
}

func run(key, status string, rows ...store.ActionRow) CandidateRun {
	return CandidateRun{Key: key, Status: status, Rows: rows}
}

func findCandidate(cands []SkillCandidate, sig ...string) *SkillCandidate {
	for i := range cands {
		if len(cands[i].Sig) != len(sig) {
			continue
		}
		match := true
		for k, s := range sig {
			if cands[i].Sig[k] != s {
				match = false
				break
			}
		}
		if match {
			return &cands[i]
		}
	}
	return nil
}

// TestMineCandidates_BasicNGram checks that a contiguous 2-gram recurring
// across most (not all) of a project's runs is picked up with the right
// DistinctRuns/RunShare.
func TestMineCandidates_BasicNGram(t *testing.T) {
	ts := time.Date(2026, 9, 9, 10, 0, 0, 0, time.UTC)
	runs := []CandidateRun{
		run("r1", "completed", row("A", "Bash", "a", 0, 10, ts), row("B", "Bash", "b", 1, 10, ts)),
		run("r2", "completed", row("A", "Bash", "a", 0, 10, ts), row("B", "Bash", "b", 1, 10, ts)),
		run("r3", "completed", row("A", "Bash", "a", 0, 10, ts), row("B", "Bash", "b", 1, 10, ts)),
		run("r4", "completed", row("Z", "Bash", "z", 0, 10, ts)), // no A/B at all
	}
	cands := MineCandidates(runs, DefaultMinRunShare, nil)
	c := findCandidate(cands, "A", "B")
	if c == nil {
		t.Fatalf("no [A B] candidate in %+v", cands)
	}
	if c.DistinctRuns != 3 {
		t.Errorf("DistinctRuns = %d, want 3", c.DistinctRuns)
	}
	if got, want := c.RunShare, 0.75; got != want {
		t.Errorf("RunShare = %v, want %v", got, want)
	}
}

// TestMineCandidates_LoopFlagsSuspectNotInflated is the LN-08 acceptance
// test: three identical tool+arg calls in one run must not inflate the
// candidate above "one occurrence in this run", and must set
// ContextLossSuspect.
func TestMineCandidates_LoopFlagsSuspectNotInflated(t *testing.T) {
	ts := time.Now()
	loopyRun := run("r1", "completed",
		row("Read", "Read", "foo.go", 0, 5, ts),
		row("Read", "Read", "foo.go", 1, 5, ts),
		row("Read", "Read", "foo.go", 2, 5, ts),
	)
	runs := []CandidateRun{
		loopyRun,
		run("r2", "completed", row("Read", "Read", "bar.go", 0, 5, ts)),
		run("r3", "completed", row("Read", "Read", "baz.go", 0, 5, ts)),
	}
	cands := MineCandidates(runs, DefaultMinRunShare, nil)
	c := findCandidate(cands, "Read")
	if c == nil {
		t.Fatalf("no [Read] candidate in %+v", cands)
	}
	if c.DistinctRuns != 3 {
		t.Errorf("DistinctRuns = %d, want 3 (loop run counts once, not three times)", c.DistinctRuns)
	}
	if !c.ContextLossSuspect {
		t.Error("ContextLossSuspect = false, want true (a run had 3 identical tool+arg calls)")
	}
}

// TestMineCandidates_DifferentArgsNotSuspect checks the counterpart: the same
// signature repeated with three *different* args in one run is a legitimate
// pattern (e.g. reading three different files), not a loop.
func TestMineCandidates_DifferentArgsNotSuspect(t *testing.T) {
	ts := time.Now()
	runs := []CandidateRun{
		run("r1", "completed",
			row("Read", "Read", "a.go", 0, 5, ts),
			row("Read", "Read", "b.go", 1, 5, ts),
			row("Read", "Read", "c.go", 2, 5, ts),
		),
		run("r2", "completed", row("Read", "Read", "d.go", 0, 5, ts)),
		run("r3", "completed", row("Read", "Read", "e.go", 0, 5, ts)),
	}
	cands := MineCandidates(runs, DefaultMinRunShare, nil)
	c := findCandidate(cands, "Read")
	if c == nil {
		t.Fatalf("no [Read] candidate in %+v", cands)
	}
	if c.ContextLossSuspect {
		t.Error("ContextLossSuspect = true, want false (different args is not a loop)")
	}
}

// TestMineCandidates_ErrorRunWithSuccessfulStepsGivesReducedWeight is the
// LN-22 acceptance test: a status=error run whose own steps succeeded
// (IsError=false) must still contribute — nonzero, per LN-22's "the first
// ninety-nine steps were fine" motivation — but less than an equivalent
// completed run would (outcomeWeight 0.3 vs 1.0). A leading row with nonzero
// ResultChars before the gram's own start is required so rediscoveryChars
// (and therefore Score) isn't trivially 0 regardless of weight.
func TestMineCandidates_ErrorRunWithSuccessfulStepsGivesReducedWeight(t *testing.T) {
	ts := time.Now()
	mk := func(extra ...CandidateRun) []CandidateRun {
		// The leading row's Sig varies per run (X1/X2/X3/...) so the 3-gram
		// [Xn A B] never recurs across runs and is filtered out by the
		// frequency threshold — only [A B] itself recurs identically, so
		// nested dedup (LN-08) never collapses [A B] into a same-DistinctRuns
		// longer gram that happens to start one call earlier.
		base := []CandidateRun{
			run("r1", "completed", row("X1", "Bash", "x", 0, 50, ts), row("A", "Bash", "a", 1, 100, ts), row("B", "Bash", "b", 2, 10, ts)),
			run("r2", "completed", row("X2", "Bash", "x", 0, 50, ts), row("A", "Bash", "a", 1, 100, ts), row("B", "Bash", "b", 2, 10, ts)),
			run("r3", "completed", row("X3", "Bash", "x", 0, 50, ts), row("A", "Bash", "a", 1, 100, ts), row("B", "Bash", "b", 2, 10, ts)),
		}
		return append(base, extra...)
	}

	before := MineCandidates(mk(), DefaultMinRunShare, nil)
	cBefore := findCandidate(before, "A", "B")
	if cBefore == nil {
		t.Fatalf("no [A B] candidate before: %+v", before)
	}

	errorRun := run("r4", "error", row("X4", "Bash", "x", 0, 50, ts), row("A", "Bash", "a", 1, 100, ts), row("B", "Bash", "b", 2, 10, ts))
	afterError := MineCandidates(mk(errorRun), DefaultMinRunShare, nil)
	cAfterError := findCandidate(afterError, "A", "B")
	if cAfterError == nil {
		t.Fatalf("no [A B] candidate after error run: %+v", afterError)
	}

	completedRun := run("r4", "completed", row("X4", "Bash", "x", 0, 50, ts), row("A", "Bash", "a", 1, 100, ts), row("B", "Bash", "b", 2, 10, ts))
	afterCompleted := MineCandidates(mk(completedRun), DefaultMinRunShare, nil)
	cAfterCompleted := findCandidate(afterCompleted, "A", "B")
	if cAfterCompleted == nil {
		t.Fatalf("no [A B] candidate after completed run: %+v", afterCompleted)
	}

	if cAfterError.DistinctRuns != cBefore.DistinctRuns+1 {
		t.Errorf("DistinctRuns after = %d, want %d (raw count still grows)", cAfterError.DistinctRuns, cBefore.DistinctRuns+1)
	}
	if cAfterError.Score <= cBefore.Score {
		t.Errorf("Score after error run = %v, want > %v (successful steps in an error run are real evidence, LN-22)", cAfterError.Score, cBefore.Score)
	}
	if cAfterError.Score >= cAfterCompleted.Score {
		t.Errorf("Score after error run = %v, want < completed-run Score %v (error still counts for less than completed)", cAfterError.Score, cAfterCompleted.Score)
	}
}

// TestMineCandidates_ErroredStepScoresZeroRegardlessOfRunStatus is the other
// half of LN-22's invariant: an n-gram occurrence whose own steps are errors
// must not score, no matter how the surrounding run ended — not even a
// status=completed run can turn a failed step into evidence.
func TestMineCandidates_ErroredStepScoresZeroRegardlessOfRunStatus(t *testing.T) {
	ts := time.Now()
	mk := func(extra ...CandidateRun) []CandidateRun {
		// The leading row's Sig varies per run (X1/X2/X3/...) so the 3-gram
		// [Xn A B] never recurs across runs and is filtered out by the
		// frequency threshold — only [A B] itself recurs identically, so
		// nested dedup (LN-08) never collapses [A B] into a same-DistinctRuns
		// longer gram that happens to start one call earlier.
		base := []CandidateRun{
			run("r1", "completed", row("X1", "Bash", "x", 0, 50, ts), row("A", "Bash", "a", 1, 100, ts), row("B", "Bash", "b", 2, 10, ts)),
			run("r2", "completed", row("X2", "Bash", "x", 0, 50, ts), row("A", "Bash", "a", 1, 100, ts), row("B", "Bash", "b", 2, 10, ts)),
			run("r3", "completed", row("X3", "Bash", "x", 0, 50, ts), row("A", "Bash", "a", 1, 100, ts), row("B", "Bash", "b", 2, 10, ts)),
		}
		return append(base, extra...)
	}

	before := MineCandidates(mk(), DefaultMinRunShare, nil)
	cBefore := findCandidate(before, "A", "B")
	if cBefore == nil {
		t.Fatalf("no [A B] candidate before: %+v", before)
	}

	erroredStepRow := row("A", "Bash", "a", 1, 9999, ts)
	erroredStepRow.IsError = true
	completedButErroredStep := run("r4", "completed", row("X4", "Bash", "x", 0, 9999, ts), erroredStepRow, row("B", "Bash", "b", 2, 9999, ts))
	after := MineCandidates(mk(completedButErroredStep), DefaultMinRunShare, nil)
	cAfter := findCandidate(after, "A", "B")
	if cAfter == nil {
		t.Fatalf("no [A B] candidate after: %+v", after)
	}

	if cAfter.DistinctRuns != cBefore.DistinctRuns+1 {
		t.Errorf("DistinctRuns after = %d, want %d (raw count still grows)", cAfter.DistinctRuns, cBefore.DistinctRuns+1)
	}
	if cAfter.Score != cBefore.Score {
		t.Errorf("Score after = %v, want unchanged %v (a failed step must not score even in a completed run)", cAfter.Score, cBefore.Score)
	}
}

// TestMineCandidates_NestedDedup builds a scenario with three signatures
// A,B,C where "A B C" always occurs together (3 runs) and "B C" additionally
// occurs alone in a 4th run. Expected: the 1-grams A/B/C and the 2-gram "A B"
// are all dropped in favor of the longer sequences that always contain them
// with the identical run count, but "B C" (4 runs) survives because its
// count is *not* matched by any longer sequence, and "A B C" (3 runs)
// survives as the longest sequence.
func TestMineCandidates_NestedDedup(t *testing.T) {
	ts := time.Now()
	abc := func(key string) CandidateRun {
		return run(key, "completed",
			row("A", "Bash", "a", 0, 1, ts),
			row("B", "Bash", "b", 1, 1, ts),
			row("C", "Bash", "c", 2, 1, ts),
		)
	}
	runs := []CandidateRun{
		abc("r1"), abc("r2"), abc("r3"),
		run("r4", "completed", row("B", "Bash", "b", 0, 1, ts), row("C", "Bash", "c", 1, 1, ts)),
	}
	cands := MineCandidates(runs, DefaultMinRunShare, nil)

	for _, sig := range [][]string{{"A"}, {"B"}, {"C"}, {"A", "B"}} {
		if c := findCandidate(cands, sig...); c != nil {
			t.Errorf("sig %v should have been deduped away, found %+v", sig, c)
		}
	}
	bc := findCandidate(cands, "B", "C")
	if bc == nil {
		t.Fatalf("[B C] should survive dedup (DistinctRuns=4, not matched by any equal-count longer gram): %+v", cands)
	}
	if bc.DistinctRuns != 4 {
		t.Errorf("[B C] DistinctRuns = %d, want 4", bc.DistinctRuns)
	}
	abcCand := findCandidate(cands, "A", "B", "C")
	if abcCand == nil {
		t.Fatalf("[A B C] should survive as the longest sequence: %+v", cands)
	}
	if abcCand.DistinctRuns != 3 {
		t.Errorf("[A B C] DistinctRuns = %d, want 3", abcCand.DistinctRuns)
	}
}

// TestMineCandidates_RelativeThresholdScalesWithProjectSize is LN-08's
// "относительный порог на двух проектах разного размера": the identical
// absolute DistinctRuns=4 must pass in a small project (20% share) and fail
// in a large one (4% share, under the 5% default) — a fixed run count is not
// the gate, the share is.
func TestMineCandidates_RelativeThresholdScalesWithProjectSize(t *testing.T) {
	ts := time.Now()
	build := func(totalRuns, patternRuns int) []CandidateRun {
		var runs []CandidateRun
		for i := 0; i < totalRuns; i++ {
			key := fmt.Sprintf("r%d", i)
			if i < patternRuns {
				runs = append(runs, run(key, "completed", row("A", "Bash", "a", 0, 1, ts), row("B", "Bash", "b", 1, 1, ts)))
			} else {
				runs = append(runs, run(key, "completed", row("Filler", "Bash", "f", 0, 1, ts)))
			}
		}
		return runs
	}

	small := MineCandidates(build(20, 4), DefaultMinRunShare, nil) // 20% share
	if c := findCandidate(small, "A", "B"); c == nil {
		t.Errorf("small project (4/20 = 20%%): [A B] should pass the share threshold, got %+v", small)
	}

	large := MineCandidates(build(100, 4), DefaultMinRunShare, nil) // 4% share, under 5%
	if c := findCandidate(large, "A", "B"); c != nil {
		t.Errorf("large project (4/100 = 4%%): [A B] should fail the share threshold, got %+v", c)
	}
}

// TestMineCandidates_BelowMinRunsAlwaysDropped checks the absolute floor:
// even a 100% run-share is not enough with fewer than MinCandidateRuns runs
// total.
func TestMineCandidates_BelowMinRunsAlwaysDropped(t *testing.T) {
	ts := time.Now()
	runs := []CandidateRun{
		run("r1", "completed", row("A", "Bash", "a", 0, 1, ts), row("B", "Bash", "b", 1, 1, ts)),
		run("r2", "completed", row("A", "Bash", "a", 0, 1, ts), row("B", "Bash", "b", 1, 1, ts)),
	}
	cands := MineCandidates(runs, DefaultMinRunShare, nil)
	if c := findCandidate(cands, "A", "B"); c != nil {
		t.Errorf("2 runs (even 100%% share) is below MinCandidateRuns=3, should be dropped: %+v", c)
	}
}

// TestMineCandidates_RelatedFailures checks the run-key overlap heuristic:
// a FailureCluster whose example fix pair was observed in one of the
// candidate's own runs is attached; an unrelated cluster is not.
func TestMineCandidates_RelatedFailures(t *testing.T) {
	ts := time.Now()
	runs := []CandidateRun{
		run("r1", "completed", row("A", "Bash", "a", 0, 1, ts), row("B", "Bash", "b", 1, 1, ts)),
		run("r2", "completed", row("A", "Bash", "a", 0, 1, ts), row("B", "Bash", "b", 1, 1, ts)),
		run("r3", "completed", row("A", "Bash", "a", 0, 1, ts), row("B", "Bash", "b", 1, 1, ts)),
	}
	related := FailureCluster{ErrorKey: "boom", Examples: []FixPair{{RunKey: "r2", FailedArg: "x"}}}
	unrelated := FailureCluster{ErrorKey: "other", Examples: []FixPair{{RunKey: "does-not-exist", FailedArg: "y"}}}

	cands := MineCandidates(runs, DefaultMinRunShare, []FailureCluster{related, unrelated})
	c := findCandidate(cands, "A", "B")
	if c == nil {
		t.Fatalf("no [A B] candidate: %+v", cands)
	}
	if len(c.RelatedFailures) != 1 || c.RelatedFailures[0].ErrorKey != "boom" {
		t.Errorf("RelatedFailures = %+v, want just the related cluster", c.RelatedFailures)
	}
}

// TestMineCandidates_ImportedFlag checks that a run with unknown status (no
// resolvable session_runs row — a bulk-imported one, LN-17) sets Imported on
// any candidate it contributes to, while still being neutrally weighted
// rather than excluded.
func TestMineCandidates_ImportedFlag(t *testing.T) {
	ts := time.Now()
	runs := []CandidateRun{
		run("r1", "completed", row("A", "Bash", "a", 0, 1, ts), row("B", "Bash", "b", 1, 1, ts)),
		run("r2", "completed", row("A", "Bash", "a", 0, 1, ts), row("B", "Bash", "b", 1, 1, ts)),
		run("r3", "", row("A", "Bash", "a", 0, 1, ts), row("B", "Bash", "b", 1, 1, ts)), // imported, unknown status
	}
	cands := MineCandidates(runs, DefaultMinRunShare, nil)
	c := findCandidate(cands, "A", "B")
	if c == nil {
		t.Fatalf("no [A B] candidate: %+v", cands)
	}
	if !c.Imported {
		t.Error("Imported = false, want true (one contributing run has unknown status)")
	}
}

// TestMineCandidates_Empty checks the trivial no-runs case doesn't panic and
// returns nothing.
func TestMineCandidates_Empty(t *testing.T) {
	if got := MineCandidates(nil, DefaultMinRunShare, nil); got != nil {
		t.Errorf("MineCandidates(nil) = %+v, want nil", got)
	}
}

// TestBuildCandidateRuns_GroupsByRunAndCarriesStatus checks that rows sharing
// a run_id fold into one CandidateRun (ordered by insertion, not
// StepIndex — MineCandidates sorts that itself) with the run's own status,
// and that a bulk-imported row (RunID nil) groups by its CLISessionID
// instead — the same actionRunKey fallback LN-07 already relies on.
func TestBuildCandidateRuns_GroupsByRunAndCarriesStatus(t *testing.T) {
	runID := int64(42)
	rows := []store.CandidateActionRow{
		{ActionRow: store.ActionRow{RunID: &runID, StepIndex: 0, Sig: "A"}, RunStatus: "completed"},
		{ActionRow: store.ActionRow{RunID: &runID, StepIndex: 1, Sig: "B"}, RunStatus: "completed"},
		{ActionRow: store.ActionRow{CLISessionID: "log.md", StepIndex: 0, Sig: "C"}, RunStatus: ""},
	}
	runs := BuildCandidateRuns(rows)
	if len(runs) != 2 {
		t.Fatalf("expected 2 distinct runs, got %d: %+v", len(runs), runs)
	}
	byKey := make(map[string]CandidateRun)
	for _, r := range runs {
		byKey[r.Key] = r
	}
	live := byKey["run:42"]
	if len(live.Rows) != 2 || live.Status != "completed" {
		t.Errorf("run:42 = %+v, want 2 rows with status completed", live)
	}
	imported := byKey["cli:log.md"]
	if len(imported.Rows) != 1 || imported.Status != "" {
		t.Errorf("cli:log.md = %+v, want 1 row with empty status", imported)
	}
}

// TestMineProjectCandidates_EndToEnd seeds a real store with a two-step
// sequence recurring across 3 live session_runs and checks that
// MineProjectCandidates — the function App.GetSkillCandidates calls — wires
// ActionRowsForCandidates' LEFT JOIN status through BuildCandidateRuns into a
// SkillCandidate with a positive score, exercising the store round-trip
// candidate_test.go's other cases (pure in-memory CandidateRun) don't cover.
func TestMineProjectCandidates_EndToEnd(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	// The first two runs are preceded by a "git pull" step (100 result
	// chars) the third run doesn't have — so the 3-gram [pull,status,add]
	// only reaches DistinctRuns=2 (below MinCandidateRuns) and is dropped by
	// the frequency filter, while the 2-gram [status,add] reaches 3 and
	// survives nested dedup untouched (their DistinctRuns differ, so the
	// dedup rule in MineCandidates never fires). This also gives the
	// survivor a nonzero rediscoveryChars in two of its three runs, so its
	// Score is asserted positive below rather than accidentally 0.
	var rows []store.ActionRow
	for i := 0; i < 3; i++ {
		r := &store.SessionRun{Project: "proj", Session: "S1", Model: "sonnet", StartedAt: now, Status: "completed"}
		if err := s.InsertRun(r); err != nil {
			t.Fatalf("InsertRun: %v", err)
		}
		step := 0
		if i < 2 {
			rows = append(rows, store.ActionRow{Project: "proj", Session: "S1", RunID: &r.ID, StepIndex: step, Tool: "Bash",
				Sig: "Bash:git pull", Arg: "git pull", ResultChars: 100, Timestamp: now})
			step++
		}
		rows = append(rows,
			store.ActionRow{Project: "proj", Session: "S1", RunID: &r.ID, StepIndex: step, Tool: "Bash",
				Sig: "Bash:git status", Arg: "git status", Timestamp: now},
			store.ActionRow{Project: "proj", Session: "S1", RunID: &r.ID, StepIndex: step + 1, Tool: "Bash",
				Sig: "Bash:git add <ARG>", Arg: "git add -A", Timestamp: now},
		)
	}
	if err := s.InsertActions(rows); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	cands, err := MineProjectCandidates(s, "proj")
	if err != nil {
		t.Fatalf("MineProjectCandidates: %v", err)
	}
	c := findCandidate(cands, "Bash:git status", "Bash:git add <ARG>")
	if c == nil {
		t.Fatalf("expected the recurring 2-gram to be mined, got %+v", cands)
	}
	if c.DistinctRuns != 3 {
		t.Errorf("DistinctRuns = %d, want 3", c.DistinctRuns)
	}
	if c.Score <= 0 {
		t.Errorf("expected a positive score (2 of 3 runs have rediscovery chars ahead of the match), got %v", c.Score)
	}
	if got := findCandidate(cands, "Bash:git pull", "Bash:git status", "Bash:git add <ARG>"); got != nil {
		t.Errorf("3-gram should be dropped (only 2 distinct runs, below MinCandidateRuns), got %+v", got)
	}
}

// TestMineProjectCandidates_NoHistoryReturnsNil checks that a project with no
// ingested action_signatures rows yields nil, nil — the ordinary state for a
// project that just turned experience_tracking on — rather than an error.
func TestMineProjectCandidates_NoHistoryReturnsNil(t *testing.T) {
	s := newTestStore(t)
	cands, err := MineProjectCandidates(s, "empty-project")
	if err != nil {
		t.Fatalf("MineProjectCandidates: %v", err)
	}
	if cands != nil {
		t.Errorf("expected nil for a project with no ingested history, got %+v", cands)
	}
}

// syntheticScores builds a deterministic, long-tailed score distribution (a
// few very high scores, many low ones) mimicking a real corpus of n mined
// candidates — Score itself scales with weightSum*log(1+rediscoveryChars),
// so a real distribution is never uniform.
func syntheticScores(n int) []float64 {
	scores := make([]float64, n)
	for i := 0; i < n; i++ {
		scores[i] = math.Pow(float64(i+1), 1.7)
	}
	return scores
}

// TestRelativeScoreThreshold_ShareScalesWithCorpusSize is LEARN-TASKS.md
// LN-23's calibration invariant: a corpus of 10 candidates and a corpus of
// 10 000 must clear a *comparable share* of the same topFraction, unlike the
// old absolute analysis.DefaultSkillMinScore (which cut everything on a
// small DB and let almost everything through on a large imported one).
func TestRelativeScoreThreshold_ShareScalesWithCorpusSize(t *testing.T) {
	const topFraction = 0.1
	for _, n := range []int{10, 10_000} {
		scores := syntheticScores(n)
		threshold := RelativeScoreThreshold(scores, topFraction)
		passing := 0
		for _, s := range scores {
			if s >= threshold {
				passing++
			}
		}
		share := float64(passing) / float64(n)
		if share < 0.08 || share > 0.12 {
			t.Errorf("n=%d: passing share = %.4f, want close to topFraction %.2f", n, share, topFraction)
		}
	}
}

func TestRelativeScoreThreshold_EmptyReturnsZero(t *testing.T) {
	if got := RelativeScoreThreshold(nil, 0.1); got != 0 {
		t.Errorf("expected 0 for an empty distribution, got %v", got)
	}
}

func TestRelativeScoreThreshold_TopCandidateAlwaysClears(t *testing.T) {
	// A tiny project (1-2 candidates) must never lose its top candidate to
	// rounding a fractional cutoff down to zero passing entries.
	for _, n := range []int{1, 2, 5} {
		scores := syntheticScores(n)
		threshold := RelativeScoreThreshold(scores, DefaultSkillTopFraction)
		top := scores[n-1] // syntheticScores is ascending; the last is highest
		if top < threshold {
			t.Errorf("n=%d: top score %v does not clear its own threshold %v", n, top, threshold)
		}
	}
}

func TestRelativeScoreThreshold_ZeroFractionFallsBackToDefault(t *testing.T) {
	scores := syntheticScores(100)
	if got, want := RelativeScoreThreshold(scores, 0), RelativeScoreThreshold(scores, DefaultSkillTopFraction); got != want {
		t.Errorf("topFraction<=0 should fall back to DefaultSkillTopFraction: got %v, want %v", got, want)
	}
}

func TestResolveSkillMinScore_ExplicitOverridesRelative(t *testing.T) {
	cands := []SkillCandidate{{Score: 100}, {Score: 50}, {Score: 1}}
	if got := ResolveSkillMinScore(cands, 5, 0); got != 5 {
		t.Errorf("explicit minScore must win over the relative computation: got %v, want 5", got)
	}
}

func TestResolveSkillMinScore_FallsBackToRelativeWhenUnset(t *testing.T) {
	cands := []SkillCandidate{{Score: 100}, {Score: 50}, {Score: 1}}
	got := ResolveSkillMinScore(cands, 0, 0.5)
	want := RelativeScoreThreshold([]float64{100, 50, 1}, 0.5)
	if got != want {
		t.Errorf("expected the relative fallback %v, got %v", want, got)
	}
}
