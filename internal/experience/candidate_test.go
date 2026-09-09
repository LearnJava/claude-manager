package experience

import (
	"fmt"
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

// TestMineCandidates_ErrorRunDoesNotRaiseScore is the LN-08 "cannot learn
// from failed runs" test: adding an occurrence from a status=error run must
// leave Score exactly unchanged — outcomeWeight(error) is 0, so it
// contributes nothing to either factor of the score formula.
func TestMineCandidates_ErrorRunDoesNotRaiseScore(t *testing.T) {
	ts := time.Now()
	mk := func(key string, extra ...CandidateRun) []CandidateRun {
		base := []CandidateRun{
			run("r1", "completed", row("A", "Bash", "a", 0, 100, ts), row("B", "Bash", "b", 1, 10, ts)),
			run("r2", "completed", row("A", "Bash", "a", 0, 100, ts), row("B", "Bash", "b", 1, 10, ts)),
			run("r3", "completed", row("A", "Bash", "a", 0, 100, ts), row("B", "Bash", "b", 1, 10, ts)),
		}
		return append(base, extra...)
	}

	before := MineCandidates(mk("before"), DefaultMinRunShare, nil)
	cBefore := findCandidate(before, "A", "B")
	if cBefore == nil {
		t.Fatalf("no [A B] candidate before: %+v", before)
	}

	afterRuns := mk("after", run("r4", "error", row("A", "Bash", "a", 0, 9999, ts), row("B", "Bash", "b", 1, 9999, ts)))
	after := MineCandidates(afterRuns, DefaultMinRunShare, nil)
	cAfter := findCandidate(after, "A", "B")
	if cAfter == nil {
		t.Fatalf("no [A B] candidate after: %+v", after)
	}

	if cAfter.DistinctRuns != cBefore.DistinctRuns+1 {
		t.Errorf("DistinctRuns after = %d, want %d (raw count still grows)", cAfter.DistinctRuns, cBefore.DistinctRuns+1)
	}
	if cAfter.Score != cBefore.Score {
		t.Errorf("Score after = %v, want unchanged %v (an error run must not pull the candidate up)", cAfter.Score, cBefore.Score)
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
