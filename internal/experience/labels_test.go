package experience

import (
	"context"
	"errors"
	"testing"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/store"
)

// stubJudge counts its calls and answers with a canned verdict.
type stubJudge struct {
	calls int
	res   *analysis.TaskLabelResult
	err   error
	seen  analysis.TaskLabelInput
}

func (j *stubJudge) judge(_ context.Context, in analysis.TaskLabelInput) (*analysis.TaskLabelResult, error) {
	j.calls++
	j.seen = in
	return j.res, j.err
}

func labelFixture(t *testing.T) (*store.Store, int64) {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	run := &store.SessionRun{Project: "p", Session: "s", StartedAt: time.Now(), Status: "unfinished",
		InputTokens: 1000, OutputTokens: 200, TotalCostUSD: 2}
	if err := st.InsertRun(run); err != nil {
		t.Fatal(err)
	}
	return st, run.ID
}

func badTask(runID int64) LabelInput {
	return LabelInput{Project: "p", TaskPtr: "T.md:5", RunID: runID, Outcome: OutcomeGatesRed,
		TaskText: "## T-1: fix it", Feedback: "FAIL TestX", Diff: "diff --git a/x_test.go"}
}

func verdict(label string) *analysis.TaskLabelResult {
	return &analysis.TaskLabelResult{Label: label, Reason: "x_test.go skips TestX", Model: "haiku",
		InputTokens: 300, OutputTokens: 40, CostUSD: 0.01}
}

func TestLabelTaskStoresLabelAndChargesTheRun(t *testing.T) {
	st, runID := labelFixture(t)
	j := &stubJudge{res: verdict(store.LabelTestsFitted)}
	l, err := LabelTask(context.Background(), st, badTask(runID), j.judge)
	if err != nil || l == nil {
		t.Fatalf("label = %+v %v", l, err)
	}
	if l.Label != store.LabelTestsFitted || l.Reason == "" || l.Model != "haiku" || l.Tokens != 340 || l.RunID != runID {
		t.Errorf("stored = %+v", l)
	}
	if j.seen.TaskText != "## T-1: fix it" || j.seen.Feedback != "FAIL TestX" || j.seen.Outcome == "" {
		t.Errorf("the judge was shown %+v", j.seen)
	}
	// The judgement is part of the task's spend.
	run, _ := st.GetRun(runID)
	if run.InputTokens != 1300 || run.OutputTokens != 240 || run.TotalCostUSD != 2.01 {
		t.Errorf("run spend = in %d out %d cost %v, want the judge's usage added", run.InputTokens, run.OutputTokens, run.TotalCostUSD)
	}
}

func TestLabelTaskNeverJudgesAGoodEnding(t *testing.T) {
	st, runID := labelFixture(t)
	j := &stubJudge{res: verdict(store.LabelGarbageLeft)}
	for _, outcome := range []string{"", "closed", "completed", "slice"} {
		in := badTask(runID)
		in.Outcome = outcome
		if l, err := LabelTask(context.Background(), st, in, j.judge); l != nil || err != nil {
			t.Errorf("outcome %q: %+v %v", outcome, l, err)
		}
	}
	empty := badTask(runID)
	empty.TaskText, empty.Feedback = "", " "
	if l, err := LabelTask(context.Background(), st, empty, j.judge); l != nil || err != nil {
		t.Errorf("nothing to judge from: %+v %v", l, err)
	}
	if j.calls != 0 {
		t.Errorf("the model was called %d times for tasks that need no label", j.calls)
	}
	if run, _ := st.GetRun(runID); run.InputTokens != 1000 {
		t.Errorf("a skipped task changed the run's spend: %d", run.InputTokens)
	}
}

func TestLabelTaskRefusesALabelOutsideTheList(t *testing.T) {
	st, runID := labelFixture(t)
	j := &stubJudge{res: verdict("sloppy")}
	l, err := LabelTask(context.Background(), st, badTask(runID), j.judge)
	if err == nil || l != nil {
		t.Fatalf("want an error, got %+v %v", l, err)
	}
	if list, _ := st.ListTaskLabels("p", 0); len(list) != 0 {
		t.Errorf("an invalid label was stored: %+v", list)
	}
}

func TestLabelTaskNoneAndFailureAndRepeat(t *testing.T) {
	st, runID := labelFixture(t)
	ctx := context.Background()

	// "none": no label, but the call was made and is charged.
	none := &stubJudge{res: verdict(analysis.LabelNone)}
	if l, err := LabelTask(ctx, st, badTask(runID), none.judge); l != nil || err != nil {
		t.Fatalf("none: %+v %v", l, err)
	}
	if run, _ := st.GetRun(runID); run.InputTokens != 1300 {
		t.Errorf("a none verdict still cost tokens: run in = %d", run.InputTokens)
	}

	// A failing judge stores nothing and charges nothing.
	boom := &stubJudge{err: errors.New("boom")}
	if l, err := LabelTask(ctx, st, badTask(runID), boom.judge); l != nil || err == nil {
		t.Fatalf("failing judge: %+v %v", l, err)
	}
	if run, _ := st.GetRun(runID); run.InputTokens != 1300 {
		t.Errorf("a failed judge changed the spend: %d", run.InputTokens)
	}

	// A run is judged once.
	ok := &stubJudge{res: verdict(store.LabelWrongTask)}
	if l, _ := LabelTask(ctx, st, badTask(runID), ok.judge); l == nil {
		t.Fatal("first real verdict must be stored")
	}
	if l, _ := LabelTask(ctx, st, badTask(runID), ok.judge); l != nil || ok.calls != 1 {
		t.Errorf("a labelled run was judged again: %+v calls=%d", l, ok.calls)
	}
}

func TestLabelTaskWithoutJudgeOrStoreDoesNothing(t *testing.T) {
	st, runID := labelFixture(t)
	if l, err := LabelTask(context.Background(), st, badTask(runID), nil); l != nil || err != nil {
		t.Errorf("no judge: %+v %v", l, err)
	}
	j := &stubJudge{res: verdict(store.LabelGarbageLeft)}
	if l, err := LabelTask(context.Background(), nil, badTask(runID), j.judge); l != nil || err != nil || j.calls != 0 {
		t.Errorf("no store: %+v %v calls=%d", l, err, j.calls)
	}
}

func TestBuildLabelReport(t *testing.T) {
	st, runID := labelFixture(t)
	rep, err := BuildLabelReport(st, "p")
	if err != nil || rep.Total != 0 || len(rep.Counts) != len(store.TaskLabelNames) || len(rep.Recent) != 0 {
		t.Fatalf("empty report = %+v %v", rep, err)
	}
	for _, name := range []string{store.LabelTestsFitted, store.LabelTestsFitted, store.LabelGarbageLeft} {
		if err := st.InsertTaskLabel(&store.TaskLabel{Project: "p", TaskPtr: "T.md:1", RunID: runID, Label: name, Tokens: 10}); err != nil {
			t.Fatal(err)
		}
	}
	rep, _ = BuildLabelReport(st, "p")
	if rep.Total != 3 || rep.Tokens != 30 || rep.Counts[0].Label != store.LabelTestsFitted || rep.Counts[0].Count != 2 {
		t.Errorf("report = %+v", rep)
	}
	if rep.Recent[0].Label != store.LabelGarbageLeft {
		t.Errorf("newest first, got %+v", rep.Recent[0])
	}
	if other, _ := BuildLabelReport(st, "q"); other.Total != 0 {
		t.Errorf("another project's labels leaked: %+v", other)
	}
}
