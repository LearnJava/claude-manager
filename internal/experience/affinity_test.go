package experience

import (
	"reflect"
	"testing"
	"time"

	"claude-manager/internal/store"
)

// TestOrderByCacheAffinity_Deterministic locks in the exact order for a
// fixed input mixing two models and overlapping file sets: sessions must
// come out grouped by model (in first-appearance order), and within a group
// chained by descending file overlap starting from the group's first
// session.
func TestOrderByCacheAffinity_Deterministic(t *testing.T) {
	sessions := []SessionAffinityInput{
		{ID: "S1", Model: "sonnet", Files: []string{"a.go", "b.go"}},
		{ID: "S2", Model: "haiku", Files: []string{"z.go"}},
		{ID: "S3", Model: "sonnet", Files: []string{"c.go"}},                 // no overlap with S1
		{ID: "S4", Model: "sonnet", Files: []string{"a.go", "b.go", "d.go"}}, // heavy overlap with S1
	}

	got := OrderByCacheAffinity(sessions)
	// haiku group (S2) comes after the sonnet group because sonnet appears
	// first in the input; within sonnet, S1 starts the chain, S4 (overlap 2)
	// is picked before S3 (overlap 0).
	want := []string{"S1", "S4", "S3", "S2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("OrderByCacheAffinity() = %v, want %v", got, want)
	}
}

// TestOrderByCacheAffinity_NoHistoryPreservesOrder is the invariant-6
// analog for launch order: with a single model and no run history at all
// (every Files empty), every pairwise overlap is zero, so the greedy chain
// must degenerate to the caller's original order byte-for-byte.
func TestOrderByCacheAffinity_NoHistoryPreservesOrder(t *testing.T) {
	sessions := []SessionAffinityInput{
		{ID: "P1", Model: "sonnet"},
		{ID: "P2", Model: "sonnet"},
		{ID: "P3", Model: "sonnet"},
	}
	got := OrderByCacheAffinity(sessions)
	want := []string{"P1", "P2", "P3"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("no-history order = %v, want %v (unchanged)", got, want)
	}
}

func TestOrderByCacheAffinity_Empty(t *testing.T) {
	if got := OrderByCacheAffinity(nil); got != nil {
		t.Fatalf("empty input: want nil, got %v", got)
	}
}

func TestOrderByCacheAffinity_SingleSession(t *testing.T) {
	got := OrderByCacheAffinity([]SessionAffinityInput{{ID: "Solo", Model: "opus"}})
	want := []string{"Solo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestFileOverlap(t *testing.T) {
	cases := []struct {
		a, b []string
		want int
	}{
		{nil, []string{"x"}, 0},
		{[]string{"x"}, nil, 0},
		{[]string{"a", "b"}, []string{"b", "c"}, 1},
		{[]string{"a", "b"}, []string{"a", "b"}, 2},
		{[]string{"a"}, []string{"b"}, 0},
	}
	for _, c := range cases {
		if got := fileOverlap(c.a, c.b); got != c.want {
			t.Errorf("fileOverlap(%v, %v) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestLastRunFiles_ReadEditWriteDeduped(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	run := &store.SessionRun{Project: "proj", Session: "S1", Model: "sonnet", StartedAt: now, Status: "completed"}
	if err := s.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	if err := s.InsertActions([]store.ActionRow{
		{Project: "proj", Session: "S1", RunID: &run.ID, StepIndex: 0, Tool: "Read", Sig: "Read:*.go", Arg: "a.go", Timestamp: now},
		{Project: "proj", Session: "S1", RunID: &run.ID, StepIndex: 1, Tool: "Edit", Sig: "Edit:*.go", Arg: "b.go", Timestamp: now.Add(time.Second)},
		{Project: "proj", Session: "S1", RunID: &run.ID, StepIndex: 2, Tool: "Read", Sig: "Read:*.go", Arg: "a.go", Timestamp: now.Add(2 * time.Second)}, // dup
		{Project: "proj", Session: "S1", RunID: &run.ID, StepIndex: 3, Tool: "Bash", Sig: "Bash:go test", Arg: "go test ./...", Timestamp: now.Add(3 * time.Second)},
		{Project: "proj", Session: "S1", RunID: &run.ID, StepIndex: 4, Tool: "Write", Sig: "Write:*.go", Arg: "c.go", Timestamp: now.Add(4 * time.Second)},
	}); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	got := LastRunFiles(s, "proj", "S1")
	want := []string{"a.go", "b.go", "c.go"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("LastRunFiles() = %v, want %v", got, want)
	}
}

func TestLastRunFiles_NoPreviousRun(t *testing.T) {
	s := newTestStore(t)
	if got := LastRunFiles(s, "proj", "unknown-session"); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}

func TestLastRunFiles_NilStore(t *testing.T) {
	if got := LastRunFiles(nil, "proj", "S1"); got != nil {
		t.Fatalf("want nil, got %v", got)
	}
}
