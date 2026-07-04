package worker

import (
	"math"
	"testing"
)

func almostEqual(a, b float64) bool {
	return math.Abs(a-b) < 1e-9
}

func TestBuildQualityReportEmpty(t *testing.T) {
	if got := BuildQualityReport(nil); len(got) != 0 {
		t.Fatalf("nil tasks: want empty report, got %+v", got)
	}
	if got := BuildQualityReport([]*MixedTask{nil}); len(got) != 0 {
		t.Fatalf("nil entries skipped: want empty report, got %+v", got)
	}
}

func TestBuildQualityReportSingleCleanTask(t *testing.T) {
	tasks := []*MixedTask{{
		WorkerName: "step37",
		Status:     TaskStatusDone,
		Rounds: []RoundRecord{{
			Number:  1,
			Applied: []Patch{{File: "a.go"}, {File: "b.go"}},
			Gates:   GateResult{Commands: []GateCommandResult{{Command: "go build"}}, Passed: true},
			Passed:  true,
		}},
	}}

	got := BuildQualityReport(tasks)
	if len(got) != 1 {
		t.Fatalf("want 1 entry, got %d", len(got))
	}
	q := got[0]
	if q.Worker != "step37" || q.TasksTotal != 1 || q.TasksDone != 1 {
		t.Errorf("task counters wrong: %+v", q)
	}
	if !almostEqual(q.AvgRoundsToGreen, 1) {
		t.Errorf("AvgRoundsToGreen = %v, want 1", q.AvgRoundsToGreen)
	}
	if q.PatchesApplied != 2 || q.PatchesRejected != 0 {
		t.Errorf("patch counters wrong: %+v", q)
	}
	if !almostEqual(q.CleanPatchRate, 1) {
		t.Errorf("CleanPatchRate = %v, want 1", q.CleanPatchRate)
	}
	if q.ParseErrors != 0 || q.GateFailures != 0 {
		t.Errorf("defects should be zero: %+v", q)
	}
}

func TestBuildQualityReportDefectsByType(t *testing.T) {
	// Round 1: parse error. Round 2: one rejected patch. Round 3: gates red.
	// Round 4: green. Every defect lands in exactly one bucket.
	tasks := []*MixedTask{{
		WorkerName: "nemotron-ultra",
		Status:     TaskStatusDone,
		Rounds: []RoundRecord{
			{Number: 1, ParseError: "missing >>>END"},
			{Number: 2, Applied: []Patch{{File: "a.go"}}, Rejected: []RejectedPatch{{Reason: "FIND not found"}}},
			{
				Number:  3,
				Applied: []Patch{{File: "a.go"}},
				Gates:   GateResult{Commands: []GateCommandResult{{Command: "go test", ExitCode: 1}}, Passed: false},
			},
			{
				Number:  4,
				Applied: []Patch{{File: "a.go"}},
				Gates:   GateResult{Commands: []GateCommandResult{{Command: "go test"}}, Passed: true},
				Passed:  true,
			},
		},
	}}

	q := BuildQualityReport(tasks)[0]
	if q.ParseErrors != 1 {
		t.Errorf("ParseErrors = %d, want 1", q.ParseErrors)
	}
	if q.GateFailures != 1 {
		t.Errorf("GateFailures = %d, want 1", q.GateFailures)
	}
	if q.PatchesApplied != 3 || q.PatchesRejected != 1 {
		t.Errorf("patches: applied=%d rejected=%d, want 3/1", q.PatchesApplied, q.PatchesRejected)
	}
	if !almostEqual(q.CleanPatchRate, 0.75) {
		t.Errorf("CleanPatchRate = %v, want 0.75", q.CleanPatchRate)
	}
	if !almostEqual(q.AvgRoundsToGreen, 4) {
		t.Errorf("AvgRoundsToGreen = %v, want 4", q.AvgRoundsToGreen)
	}
}

func TestBuildQualityReportStatusBuckets(t *testing.T) {
	tasks := []*MixedTask{
		{WorkerName: "w", Status: TaskStatusDone, Rounds: []RoundRecord{{Number: 1}, {Number: 2}}},
		{WorkerName: "w", Status: TaskStatusDone, Rounds: []RoundRecord{{Number: 1}, {Number: 2}, {Number: 3}, {Number: 4}}},
		{WorkerName: "w", Status: TaskStatusNeedsHuman, Rounds: []RoundRecord{{Number: 1}, {Number: 2}, {Number: 3}}},
		{WorkerName: "w", Status: TaskStatusRunning},
	}

	q := BuildQualityReport(tasks)[0]
	if q.TasksTotal != 4 || q.TasksDone != 2 || q.TasksNeedsHuman != 1 || q.TasksRunning != 1 {
		t.Errorf("status buckets wrong: %+v", q)
	}
	// Only the two done tasks count toward rounds-to-green: (2+4)/2 = 3.
	if !almostEqual(q.AvgRoundsToGreen, 3) {
		t.Errorf("AvgRoundsToGreen = %v, want 3", q.AvgRoundsToGreen)
	}
}

func TestBuildQualityReportSortedByWorker(t *testing.T) {
	tasks := []*MixedTask{
		{WorkerName: "zeta", Status: TaskStatusDone},
		{WorkerName: "alpha", Status: TaskStatusDone},
		{WorkerName: "mid", Status: TaskStatusRunning},
	}
	got := BuildQualityReport(tasks)
	if len(got) != 3 {
		t.Fatalf("want 3 entries, got %d", len(got))
	}
	for i, want := range []string{"alpha", "mid", "zeta"} {
		if got[i].Worker != want {
			t.Errorf("order[%d] = %q, want %q", i, got[i].Worker, want)
		}
	}
}
