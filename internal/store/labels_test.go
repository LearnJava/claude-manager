package store

import (
	"testing"
	"time"
)

func TestTaskLabelRejectsUnknownLabel(t *testing.T) {
	s := newTestStore(t)
	for _, bad := range []string{"", "lazy", "TESTS_FITTED_TO_CODE"} {
		l := &TaskLabel{Project: "p", TaskPtr: "T.md:1", Label: bad}
		if err := s.InsertTaskLabel(l); err == nil || l.ID != 0 {
			t.Errorf("label %q: want an error and no row, got id=%d err=%v", bad, l.ID, err)
		}
	}
	if list, _ := s.ListTaskLabels("p", 0); len(list) != 0 {
		t.Errorf("a refused label was written: %+v", list)
	}
}

func TestTaskLabelLifecycle(t *testing.T) {
	s := newTestStore(t)
	for i, name := range TaskLabelNames {
		l := &TaskLabel{Project: "p", TaskPtr: "T.md:1", RunID: int64(i + 1), Label: name, Reason: "why", Model: "haiku", Tokens: 100}
		if err := s.InsertTaskLabel(l); err != nil || l.ID == 0 {
			t.Fatalf("insert %s: %+v %v", name, l, err)
		}
	}
	if err := s.InsertTaskLabel(&TaskLabel{Project: "q", Label: LabelGarbageLeft, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListTaskLabels("p", 0)
	if err != nil || len(list) != len(TaskLabelNames) {
		t.Fatalf("list = %d %v", len(list), err)
	}
	if list[0].Label != TaskLabelNames[len(TaskLabelNames)-1] || list[0].Tokens != 100 || list[0].Model != "haiku" {
		t.Errorf("newest first: %+v", list[0])
	}
	if list, _ := s.ListTaskLabels("p", 2); len(list) != 2 {
		t.Errorf("limit: got %d", len(list))
	}
}

func TestAddRunUsage(t *testing.T) {
	s := newTestStore(t)
	run := &SessionRun{Project: "p", Session: "s", StartedAt: time.Now(), Status: "running",
		InputTokens: 10, OutputTokens: 5, TotalCostUSD: 1.5}
	if err := s.InsertRun(run); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRunUsage(run.ID, 7, 3, 0.25); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRun(run.ID)
	if err != nil || got == nil {
		t.Fatalf("get: %v %v", got, err)
	}
	if got.InputTokens != 17 || got.OutputTokens != 8 || got.TotalCostUSD != 1.75 {
		t.Errorf("run usage = in %d out %d cost %v", got.InputTokens, got.OutputTokens, got.TotalCostUSD)
	}
	if err := s.AddRunUsage(9999, 1, 1, 1); err != nil {
		t.Errorf("a missing run must change nothing, got %v", err)
	}
}
