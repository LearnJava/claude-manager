package store

import (
	"os"
	"strings"
	"testing"
	"time"
)

// newTestStore opens a temporary in-memory SQLite store and registers a cleanup
// function that closes it after the test. The pure-Go modernc.org/sqlite driver
// needs no cgo, so this works in every environment (including CGO_ENABLED=0).
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func ptr[T any](v T) *T { return &v }

// --- SessionRun ---

func TestInsertGetRun(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	run := &SessionRun{
		Project:   "proj",
		Session:   "S1",
		Model:     "sonnet",
		StartedAt: now,
		Status:    "completed",
	}
	if err := s.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	if run.ID == 0 {
		t.Fatal("expected non-zero ID after insert")
	}

	got, err := s.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun: %v", err)
	}
	if got == nil {
		t.Fatal("GetRun returned nil")
	}
	if got.Project != "proj" || got.Session != "S1" || got.Model != "sonnet" {
		t.Errorf("unexpected run fields: %+v", got)
	}
	if !got.StartedAt.Equal(now) {
		t.Errorf("StartedAt: got %v want %v", got.StartedAt, now)
	}
}

func TestGetRunNotFound(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetRun(9999)
	if err != nil {
		t.Fatalf("GetRun(missing): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing run, got %+v", got)
	}
}

func TestUpdateRun(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	run := &SessionRun{Project: "p", Session: "s", Model: "haiku", StartedAt: now, Status: "working"}
	if err := s.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	finished := now.Add(5 * time.Second)
	run.FinishedAt = &finished
	run.Status = "completed"
	run.TasksDone = 3
	run.TotalCostUSD = 0.05
	run.InputTokens = 1000
	run.OutputTokens = 500
	run.NumTurns = 2
	run.DurationMs = 5000
	if err := s.UpdateRun(run); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}

	got, err := s.GetRun(run.ID)
	if err != nil {
		t.Fatalf("GetRun after update: %v", err)
	}
	if got.Status != "completed" {
		t.Errorf("status: got %q want completed", got.Status)
	}
	if got.TasksDone != 3 {
		t.Errorf("tasks_done: got %d want 3", got.TasksDone)
	}
	if got.TotalCostUSD != 0.05 {
		t.Errorf("total_cost_usd: got %f want 0.05", got.TotalCostUSD)
	}
	if got.FinishedAt == nil || !got.FinishedAt.Equal(finished) {
		t.Errorf("FinishedAt: got %v want %v", got.FinishedAt, finished)
	}
}

func TestUpdateRunExitCode(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	run := &SessionRun{Project: "p", Session: "s", Model: "m", StartedAt: now, Status: "error"}
	if err := s.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	run.ExitCode = ptr(1)
	run.ErrorMsg = "exit status 1"
	if err := s.UpdateRun(run); err != nil {
		t.Fatalf("UpdateRun: %v", err)
	}

	got, _ := s.GetRun(run.ID)
	if got.ExitCode == nil || *got.ExitCode != 1 {
		t.Errorf("ExitCode: got %v want 1", got.ExitCode)
	}
	if got.ErrorMsg != "exit status 1" {
		t.Errorf("ErrorMsg: got %q", got.ErrorMsg)
	}
}

func TestListRuns(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	// Insert 3 runs: two for "alpha", one for "beta".
	for i := 0; i < 2; i++ {
		r := &SessionRun{Project: "alpha", Session: "S1", Model: "sonnet",
			StartedAt: now.Add(time.Duration(i) * time.Second), Status: "completed"}
		if err := s.InsertRun(r); err != nil {
			t.Fatalf("InsertRun: %v", err)
		}
	}
	r := &SessionRun{Project: "beta", Session: "S1", Model: "haiku",
		StartedAt: now, Status: "completed"}
	if err := s.InsertRun(r); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	tests := []struct {
		project, session string
		limit            int
		wantCount        int
	}{
		{"alpha", "", 0, 2},
		{"alpha", "S1", 0, 2},
		{"alpha", "", 1, 1},
		{"beta", "", 0, 1},
		{"", "", 0, 3},
		{"notexist", "", 0, 0},
	}
	for _, tc := range tests {
		runs, err := s.ListRuns(tc.project, tc.session, tc.limit)
		if err != nil {
			t.Errorf("ListRuns(%q,%q,%d): %v", tc.project, tc.session, tc.limit, err)
			continue
		}
		if len(runs) != tc.wantCount {
			t.Errorf("ListRuns(%q,%q,%d): got %d runs, want %d",
				tc.project, tc.session, tc.limit, len(runs), tc.wantCount)
		}
	}
}

// --- SessionLogs ---

func TestInsertGetLogs(t *testing.T) {
	s := newTestStore(t)
	run := &SessionRun{Project: "p", Session: "s", Model: "m",
		StartedAt: time.Now().UTC(), Status: "working"}
	if err := s.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	entries := []LogEntry{
		{RunID: run.ID, Timestamp: time.Now().UTC(), Level: "text", Message: "hello"},
		{RunID: run.ID, Timestamp: time.Now().UTC(), Level: "tool", Message: "read file", ToolName: "Read", ToolInput: "main.go"},
		{RunID: run.ID, Timestamp: time.Now().UTC(), Level: "error", Message: "boom"},
	}
	if err := s.InsertLogs(run.ID, entries); err != nil {
		t.Fatalf("InsertLogs: %v", err)
	}

	// All three entries.
	logs, err := s.GetLogs(run.ID, 0, 10)
	if err != nil {
		t.Fatalf("GetLogs: %v", err)
	}
	if len(logs) != 3 {
		t.Fatalf("GetLogs: got %d, want 3", len(logs))
	}
	if logs[1].ToolName != "Read" || logs[1].ToolInput != "main.go" {
		t.Errorf("log[1] tool fields: %+v", logs[1])
	}

	// Pagination: skip first, limit 1.
	paged, err := s.GetLogs(run.ID, 1, 1)
	if err != nil {
		t.Fatalf("GetLogs paged: %v", err)
	}
	if len(paged) != 1 {
		t.Fatalf("GetLogs paged: got %d, want 1", len(paged))
	}
	if paged[0].Level != "tool" {
		t.Errorf("unexpected log level after offset: %q", paged[0].Level)
	}
}

func TestInsertLogsEmpty(t *testing.T) {
	s := newTestStore(t)
	// InsertLogs with empty slice must be a no-op (no error).
	if err := s.InsertLogs(42, nil); err != nil {
		t.Fatalf("InsertLogs(nil): %v", err)
	}
	if err := s.InsertLogs(42, []LogEntry{}); err != nil {
		t.Fatalf("InsertLogs(empty): %v", err)
	}
}

func TestDeleteOldLogs(t *testing.T) {
	s := newTestStore(t)
	old := time.Now().UTC().Add(-48 * time.Hour) // 2 days ago
	run := &SessionRun{Project: "p", Session: "s", Model: "m",
		StartedAt: old, Status: "completed"}
	if err := s.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	if err := s.InsertLogs(run.ID, []LogEntry{
		{RunID: run.ID, Timestamp: old, Level: "text", Message: "old"},
	}); err != nil {
		t.Fatalf("InsertLogs: %v", err)
	}

	// Deleting logs older than 1 day should remove the entry.
	if err := s.DeleteOldLogs(1); err != nil {
		t.Fatalf("DeleteOldLogs: %v", err)
	}

	logs, err := s.GetLogs(run.ID, 0, 100)
	if err != nil {
		t.Fatalf("GetLogs after delete: %v", err)
	}
	if len(logs) != 0 {
		t.Errorf("expected 0 logs after deletion, got %d", len(logs))
	}
}

func TestDeleteOldLogsRetainsRecent(t *testing.T) {
	s := newTestStore(t)
	run := &SessionRun{Project: "p", Session: "s", Model: "m",
		StartedAt: time.Now().UTC(), Status: "completed"}
	if err := s.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	if err := s.InsertLogs(run.ID, []LogEntry{
		{RunID: run.ID, Timestamp: time.Now().UTC(), Level: "text", Message: "fresh"},
	}); err != nil {
		t.Fatalf("InsertLogs: %v", err)
	}

	// Deleting logs older than 30 days should not remove today's entry.
	if err := s.DeleteOldLogs(30); err != nil {
		t.Fatalf("DeleteOldLogs: %v", err)
	}

	logs, _ := s.GetLogs(run.ID, 0, 100)
	if len(logs) != 1 {
		t.Errorf("expected 1 log retained, got %d", len(logs))
	}
}

func TestDeleteLogsForProject(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	runA := &SessionRun{Project: "proj-a", Session: "S1", Model: "m", StartedAt: now, Status: "completed"}
	if err := s.InsertRun(runA); err != nil {
		t.Fatalf("InsertRun A: %v", err)
	}
	if err := s.InsertLogs(runA.ID, []LogEntry{{Timestamp: now, Level: "text", Message: "a"}}); err != nil {
		t.Fatalf("InsertLogs A: %v", err)
	}

	runB := &SessionRun{Project: "proj-b", Session: "S1", Model: "m", StartedAt: now, Status: "completed"}
	if err := s.InsertRun(runB); err != nil {
		t.Fatalf("InsertRun B: %v", err)
	}
	if err := s.InsertLogs(runB.ID, []LogEntry{{Timestamp: now, Level: "text", Message: "b"}}); err != nil {
		t.Fatalf("InsertLogs B: %v", err)
	}

	if err := s.DeleteLogsForProject("proj-a"); err != nil {
		t.Fatalf("DeleteLogsForProject: %v", err)
	}

	logsA, _ := s.GetLogs(runA.ID, 0, 100)
	if len(logsA) != 0 {
		t.Errorf("expected proj-a logs deleted, got %d", len(logsA))
	}
	logsB, _ := s.GetLogs(runB.ID, 0, 100)
	if len(logsB) != 1 {
		t.Errorf("expected proj-b logs retained, got %d", len(logsB))
	}

	runAfter, err := s.GetRun(runA.ID)
	if err != nil || runAfter == nil {
		t.Fatalf("expected proj-a run row to survive (only logs cleared): %v", err)
	}
}

// --- DailyMetrics ---

func TestAddGetDailyMetrics(t *testing.T) {
	s := newTestStore(t)
	date := "2026-05-01"

	m := &DailyMetrics{
		Date:              date,
		Project:           "proj",
		TotalCost:         0.10,
		TotalInputTokens:  1000,
		TotalOutputTokens: 200,
		TotalRuns:         1,
		TotalTasks:        3,
	}
	if err := s.AddDailyMetrics(m); err != nil {
		t.Fatalf("AddDailyMetrics: %v", err)
	}

	got, err := s.GetDailyMetrics(date, "proj")
	if err != nil {
		t.Fatalf("GetDailyMetrics: %v", err)
	}
	if got == nil {
		t.Fatal("GetDailyMetrics returned nil")
	}
	if got.TotalCost != 0.10 || got.TotalRuns != 1 || got.TotalTasks != 3 {
		t.Errorf("unexpected metrics: %+v", got)
	}
}

func TestAddDailyMetricsUpsert(t *testing.T) {
	s := newTestStore(t)
	date := "2026-05-01"
	base := &DailyMetrics{Date: date, Project: "p", TotalCost: 1.0, TotalRuns: 1}
	if err := s.AddDailyMetrics(base); err != nil {
		t.Fatalf("AddDailyMetrics first: %v", err)
	}
	delta := &DailyMetrics{Date: date, Project: "p", TotalCost: 0.5, TotalRuns: 1}
	if err := s.AddDailyMetrics(delta); err != nil {
		t.Fatalf("AddDailyMetrics second: %v", err)
	}

	got, _ := s.GetDailyMetrics(date, "p")
	if got.TotalCost != 1.5 {
		t.Errorf("upsert: total_cost got %f want 1.5", got.TotalCost)
	}
	if got.TotalRuns != 2 {
		t.Errorf("upsert: total_runs got %d want 2", got.TotalRuns)
	}
}

func TestGetDailyMetricsNotFound(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetDailyMetrics("2000-01-01", "nothing")
	if err != nil {
		t.Fatalf("GetDailyMetrics(missing): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestListDailyMetrics(t *testing.T) {
	s := newTestStore(t)
	dates := []string{"2026-05-30", "2026-05-29", "2026-05-01"}
	for _, d := range dates {
		if err := s.AddDailyMetrics(&DailyMetrics{Date: d, Project: "p", TotalRuns: 1}); err != nil {
			t.Fatalf("AddDailyMetrics: %v", err)
		}
	}
	// ListDailyMetrics with 7 days: should return only the two recent entries.
	// (2026-05-01 is >7 days before 2026-05-30 in calendar terms — skip that
	// since the query uses 'now'; use a large window that includes all.)
	all, err := s.ListDailyMetrics("p", 3650) // 10 years
	if err != nil {
		t.Fatalf("ListDailyMetrics: %v", err)
	}
	if len(all) < 1 {
		t.Errorf("expected >=1 rows, got %d", len(all))
	}
	// Verify descending order.
	for i := 1; i < len(all); i++ {
		if all[i-1].Date < all[i].Date {
			t.Errorf("ListDailyMetrics: not descending at index %d", i)
		}
	}
}

// --- TaskPlan ---

func TestInsertGetPlan(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	plan := &TaskPlan{
		Project:      "proj",
		OriginalTask: "build feature X",
		AnalysisJSON: `{"feasible":true}`,
		Status:       "draft",
		CreatedAt:    now,
	}
	if err := s.InsertPlan(plan); err != nil {
		t.Fatalf("InsertPlan: %v", err)
	}
	if plan.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := s.GetPlan(plan.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if got == nil {
		t.Fatal("GetPlan returned nil")
	}
	if got.OriginalTask != "build feature X" || got.Status != "draft" {
		t.Errorf("unexpected plan: %+v", got)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt: got %v want %v", got.CreatedAt, now)
	}
}

func TestGetPlanNotFound(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetPlan(9999)
	if err != nil {
		t.Fatalf("GetPlan(missing): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestUpdatePlan(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	plan := &TaskPlan{Project: "p", OriginalTask: "t", AnalysisJSON: "{}", Status: "draft", CreatedAt: now}
	if err := s.InsertPlan(plan); err != nil {
		t.Fatalf("InsertPlan: %v", err)
	}

	finished := now.Add(time.Minute)
	plan.Status = "completed"
	plan.CompletedAt = &finished
	plan.TotalCostUSD = ptr(0.25)
	plan.TotalTokens = ptr(int64(5000))
	if err := s.UpdatePlan(plan); err != nil {
		t.Fatalf("UpdatePlan: %v", err)
	}

	got, _ := s.GetPlan(plan.ID)
	if got.Status != "completed" {
		t.Errorf("status: got %q want completed", got.Status)
	}
	if got.TotalCostUSD == nil || *got.TotalCostUSD != 0.25 {
		t.Errorf("TotalCostUSD: got %v", got.TotalCostUSD)
	}
	if got.TotalTokens == nil || *got.TotalTokens != 5000 {
		t.Errorf("TotalTokens: got %v", got.TotalTokens)
	}
}

func TestListPlans(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		p := &TaskPlan{Project: "p", OriginalTask: "t", AnalysisJSON: "{}",
			Status: "draft", CreatedAt: now.Add(time.Duration(i) * time.Second)}
		if err := s.InsertPlan(p); err != nil {
			t.Fatalf("InsertPlan: %v", err)
		}
	}
	// Extra plan for a different project.
	if err := s.InsertPlan(&TaskPlan{Project: "other", OriginalTask: "x",
		AnalysisJSON: "{}", Status: "draft", CreatedAt: now}); err != nil {
		t.Fatalf("InsertPlan other: %v", err)
	}

	all, err := s.ListPlans("p", 0)
	if err != nil {
		t.Fatalf("ListPlans: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListPlans: got %d, want 3", len(all))
	}

	limited, err := s.ListPlans("p", 2)
	if err != nil {
		t.Fatalf("ListPlans limited: %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("ListPlans limit 2: got %d", len(limited))
	}
}

// --- PlanSubtask ---

func TestInsertUpdateListSubtasks(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	plan := &TaskPlan{Project: "p", OriginalTask: "t", AnalysisJSON: "{}", Status: "draft", CreatedAt: now}
	if err := s.InsertPlan(plan); err != nil {
		t.Fatalf("InsertPlan: %v", err)
	}

	subs := []*PlanSubtask{
		{PlanID: plan.ID, SubtaskID: "s1", Name: "first", Prompt: "do A", Status: "pending"},
		{PlanID: plan.ID, SubtaskID: "s2", Name: "second", Prompt: "do B",
			DependsOn: `["s1"]`, Model: "haiku", Status: "pending"},
	}
	for _, sub := range subs {
		if err := s.InsertSubtask(sub); err != nil {
			t.Fatalf("InsertSubtask: %v", err)
		}
		if sub.ID == 0 {
			t.Fatal("expected non-zero subtask ID")
		}
	}

	list, err := s.ListSubtasks(plan.ID)
	if err != nil {
		t.Fatalf("ListSubtasks: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("ListSubtasks: got %d, want 2", len(list))
	}
	if list[1].DependsOn != `["s1"]` {
		t.Errorf("DependsOn: got %q", list[1].DependsOn)
	}

	// Update the first subtask, linking it to a real run (session_run_id has a
	// foreign key to session_runs, so the referenced run must exist).
	run := &SessionRun{Project: "p", Session: "s1", Model: "haiku", StartedAt: time.Now(), Status: "completed"}
	if err := s.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}
	runID := run.ID
	subs[0].Status = "completed"
	subs[0].SessionRunID = &runID
	subs[0].ResultSummary = "done"
	subs[0].FilesChanged = `["a.go"]`
	if err := s.UpdateSubtask(subs[0]); err != nil {
		t.Fatalf("UpdateSubtask: %v", err)
	}

	updated, err := s.ListSubtasks(plan.ID)
	if err != nil {
		t.Fatalf("ListSubtasks after update: %v", err)
	}
	if updated[0].Status != "completed" {
		t.Errorf("status after update: got %q want completed", updated[0].Status)
	}
	if updated[0].ResultSummary != "done" {
		t.Errorf("ResultSummary: got %q", updated[0].ResultSummary)
	}
	if updated[0].SessionRunID == nil || *updated[0].SessionRunID != runID {
		t.Errorf("SessionRunID: got %v, want %d", updated[0].SessionRunID, runID)
	}
}

func TestListSubtasksEmpty(t *testing.T) {
	s := newTestStore(t)
	list, err := s.ListSubtasks(9999)
	if err != nil {
		t.Fatalf("ListSubtasks(missing plan): %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

// --- MixedBrief ---

func TestInsertGetBriefByBriefID(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)
	b := &MixedBrief{
		BriefID:   "brief-1",
		Project:   "proj",
		Task:      "Replace the retry loop with backoff.",
		Files:     `["a.go","b.go"]`,
		CreatedAt: now,
	}
	if err := s.InsertBrief(b); err != nil {
		t.Fatalf("InsertBrief: %v", err)
	}
	if b.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := s.GetBriefByBriefID("brief-1")
	if err != nil {
		t.Fatalf("GetBriefByBriefID: %v", err)
	}
	if got == nil {
		t.Fatal("GetBriefByBriefID returned nil")
	}
	if got.Task != b.Task || got.Project != "proj" || got.Files != b.Files {
		t.Errorf("unexpected brief: %+v", got)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt: got %v want %v", got.CreatedAt, now)
	}
}

func TestGetBriefByBriefIDNotFound(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetBriefByBriefID("no-such-brief")
	if err != nil {
		t.Fatalf("GetBriefByBriefID(missing): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

func TestInsertBriefDuplicateBriefIDFails(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	b1 := &MixedBrief{BriefID: "dup", Project: "p", Task: "t1", CreatedAt: now}
	if err := s.InsertBrief(b1); err != nil {
		t.Fatalf("InsertBrief: %v", err)
	}
	b2 := &MixedBrief{BriefID: "dup", Project: "p", Task: "t2", CreatedAt: now}
	if err := s.InsertBrief(b2); err == nil {
		t.Error("expected error inserting a duplicate brief_id")
	}
}

func TestListBriefs(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		b := &MixedBrief{
			BriefID: strings.Repeat("x", i+1), Project: "p", Task: "t",
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		}
		if err := s.InsertBrief(b); err != nil {
			t.Fatalf("InsertBrief: %v", err)
		}
	}
	if err := s.InsertBrief(&MixedBrief{BriefID: "other-proj", Project: "other", Task: "t", CreatedAt: now}); err != nil {
		t.Fatalf("InsertBrief other: %v", err)
	}

	all, err := s.ListBriefs("p", 0)
	if err != nil {
		t.Fatalf("ListBriefs: %v", err)
	}
	if len(all) != 3 {
		t.Errorf("ListBriefs: got %d, want 3", len(all))
	}

	limited, err := s.ListBriefs("p", 2)
	if err != nil {
		t.Fatalf("ListBriefs limited: %v", err)
	}
	if len(limited) != 2 {
		t.Errorf("ListBriefs limit 2: got %d", len(limited))
	}
}

func TestListBriefsEmpty(t *testing.T) {
	s := newTestStore(t)
	list, err := s.ListBriefs("no-such-project", 0)
	if err != nil {
		t.Fatalf("ListBriefs: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("expected empty list, got %d", len(list))
	}
}

// --- Close ---

func TestClose(t *testing.T) {
	f, err := os.CreateTemp("", "store_test_*.db")
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	f.Close()
	path := f.Name()
	defer os.Remove(path)

	s, err := New(path)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// --- action_signatures / ingest_state (LEARN-TASKS.md LN-02) ---

func TestInsertActionsAndActionsForRun(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	run := &SessionRun{Project: "p", Session: "S1", Model: "sonnet", StartedAt: now, Status: "completed"}
	if err := s.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	rows := []ActionRow{
		{
			Project: "p", Session: "S1", RunID: &run.ID, CLISessionID: "cli-1",
			TaskPtr: "ROADMAP.md:5", StepIndex: 0, Tool: "Bash",
			Sig: "Bash:git status --short", Arg: "git status --short",
			OutTokens: 12, ResultChars: 40, Timestamp: now,
		},
		{
			Project: "p", Session: "S1", RunID: &run.ID, CLISessionID: "cli-1",
			TaskPtr: "ROADMAP.md:5", StepIndex: 1, Tool: "Read",
			Sig: "Read:internal/experience/*.go", Arg: "internal/experience/signature.go",
			IsError: true, OutTokens: 3, ResultChars: 0, Timestamp: now.Add(time.Second),
		},
	}
	if err := s.InsertActions(rows); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	got, err := s.ActionsForRun(run.ID)
	if err != nil {
		t.Fatalf("ActionsForRun: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ActionsForRun: got %d rows, want 2", len(got))
	}
	if got[0].Sig != rows[0].Sig || got[0].StepIndex != 0 || got[0].IsError {
		t.Errorf("row 0: unexpected fields: %+v", got[0])
	}
	if got[1].Sig != rows[1].Sig || got[1].StepIndex != 1 || !got[1].IsError {
		t.Errorf("row 1: unexpected fields: %+v", got[1])
	}
	if got[1].RunID == nil || *got[1].RunID != run.ID {
		t.Errorf("row 1: RunID = %v, want %d", got[1].RunID, run.ID)
	}
	if got[0].CLISessionID != "cli-1" || got[0].TaskPtr != "ROADMAP.md:5" {
		t.Errorf("row 0: unexpected CLISessionID/TaskPtr: %+v", got[0])
	}
}

func TestInsertActionsEmpty(t *testing.T) {
	s := newTestStore(t)
	if err := s.InsertActions(nil); err != nil {
		t.Fatalf("InsertActions(nil): %v", err)
	}
}

func TestTopSignatures(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	var runIDs []int64
	for i := 0; i < 3; i++ {
		run := &SessionRun{Project: "p", Session: "S1", Model: "sonnet", StartedAt: now, Status: "completed"}
		if err := s.InsertRun(run); err != nil {
			t.Fatalf("InsertRun: %v", err)
		}
		runIDs = append(runIDs, run.ID)
	}

	rows := []ActionRow{
		{Project: "p", Session: "S1", RunID: &runIDs[0], Tool: "Bash", Sig: "Bash:git status --short", Arg: "git status --short", StepIndex: 0, Timestamp: now},
		{Project: "p", Session: "S1", RunID: &runIDs[0], Tool: "Bash", Sig: "Bash:git status --short", Arg: "git status", StepIndex: 1, Timestamp: now},
		{Project: "p", Session: "S1", RunID: &runIDs[1], Tool: "Bash", Sig: "Bash:git status --short", Arg: "git status --short", StepIndex: 0, IsError: true, OutTokens: 5, Timestamp: now},
		{Project: "p", Session: "S1", RunID: &runIDs[1], Tool: "Read", Sig: "Read:internal/store/*.go", Arg: "internal/store/store.go", StepIndex: 1, OutTokens: 20, Timestamp: now},
		{Project: "other", Session: "S1", RunID: &runIDs[2], Tool: "Bash", Sig: "Bash:git status --short", Arg: "git status --short", StepIndex: 0, Timestamp: now},
	}
	if err := s.InsertActions(rows); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	stats, err := s.TopSignatures("p", 30, 0)
	if err != nil {
		t.Fatalf("TopSignatures: %v", err)
	}
	if len(stats) != 2 {
		t.Fatalf("TopSignatures: got %d groups, want 2", len(stats))
	}

	// Most frequent first: the git status signature appears 3 times for "p".
	top := stats[0]
	if top.Sig != "Bash:git status --short" || top.Count != 3 {
		t.Errorf("top signature: got %+v", top)
	}
	if top.DistinctRuns != 2 {
		t.Errorf("top signature DistinctRuns: got %d, want 2", top.DistinctRuns)
	}
	if top.ErrorRate < 0.33 || top.ErrorRate > 0.34 {
		t.Errorf("top signature ErrorRate: got %v, want ~1/3", top.ErrorRate)
	}
	if len(top.SampleArgs) == 0 {
		t.Error("top signature: expected at least one SampleArgs entry")
	}

	limited, err := s.TopSignatures("p", 30, 1)
	if err != nil {
		t.Fatalf("TopSignatures limited: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("TopSignatures limit 1: got %d", len(limited))
	}
}

func TestTopSignaturesSinceDaysExcludesOld(t *testing.T) {
	s := newTestStore(t)
	old := time.Now().UTC().AddDate(0, 0, -60)

	if err := s.InsertActions([]ActionRow{
		{Project: "p", Session: "S1", Tool: "Bash", Sig: "Bash:git status", StepIndex: 0, Timestamp: old},
	}); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	stats, err := s.TopSignatures("p", 30, 0)
	if err != nil {
		t.Fatalf("TopSignatures: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected old row excluded by sinceDays window, got %+v", stats)
	}
}

// TestTopSignatures_NullRunIDGroupsByCLISessionID: a bulk-imported row
// (LEARN-TASKS.md LN-17) has RunID == nil — DistinctRuns must still count it
// by falling back to cli_session_id, not silently drop it the way plain
// COUNT(DISTINCT run_id) would (LEARN-TASKS.md LN-03).
func TestTopSignatures_NullRunIDGroupsByCLISessionID(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	run := &SessionRun{Project: "p", Session: "S1", Model: "sonnet", StartedAt: now, Status: "completed"}
	if err := s.InsertRun(run); err != nil {
		t.Fatalf("InsertRun: %v", err)
	}

	rows := []ActionRow{
		// A live run — has a run_id.
		{Project: "p", Session: "S1", RunID: &run.ID, Tool: "Bash", Sig: "Bash:git status", StepIndex: 0, Timestamp: now},
		// Two bulk-imported rows from different files — RunID nil, distinguished
		// only by cli_session_id (the file name, per IngestDir).
		{Project: "p", Session: "S1", CLISessionID: "log-a.md", Tool: "Bash", Sig: "Bash:git status", StepIndex: 0, Timestamp: now},
		{Project: "p", Session: "S1", CLISessionID: "log-b.md", Tool: "Bash", Sig: "Bash:git status", StepIndex: 0, Timestamp: now},
	}
	if err := s.InsertActions(rows); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	stats, err := s.TopSignatures("p", 30, 0)
	if err != nil {
		t.Fatalf("TopSignatures: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("TopSignatures: got %d groups, want 1", len(stats))
	}
	if stats[0].Count != 3 {
		t.Errorf("Count = %d, want 3 (all rows visible, none dropped for a NULL run_id)", stats[0].Count)
	}
	if stats[0].DistinctRuns != 3 {
		t.Errorf("DistinctRuns = %d, want 3 (1 real run + 2 distinct cli_session_ids)", stats[0].DistinctRuns)
	}
}

// TestActionSamples returns full rows for one (project, sig) pair, most
// recent first, respecting limit — the "Actions" tab's click-through.
func TestActionSamples(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	rows := []ActionRow{
		{Project: "p", Session: "S1", Tool: "Bash", Sig: "Bash:git status", Arg: "git status --short", StepIndex: 0, Timestamp: now},
		{Project: "p", Session: "S1", Tool: "Bash", Sig: "Bash:git status", Arg: "git status", StepIndex: 1, Timestamp: now.Add(time.Second)},
		{Project: "p", Session: "S1", Tool: "Read", Sig: "Read:internal/*.go", Arg: "internal/app.go", StepIndex: 2, Timestamp: now},
	}
	if err := s.InsertActions(rows); err != nil {
		t.Fatalf("InsertActions: %v", err)
	}

	samples, err := s.ActionSamples("p", "Bash:git status", 0)
	if err != nil {
		t.Fatalf("ActionSamples: %v", err)
	}
	if len(samples) != 2 {
		t.Fatalf("ActionSamples: got %d rows, want 2", len(samples))
	}
	// Most recent (highest id) first.
	if samples[0].Arg != "git status" {
		t.Errorf("ActionSamples[0].Arg = %q, want most recent row first", samples[0].Arg)
	}

	limited, err := s.ActionSamples("p", "Bash:git status", 1)
	if err != nil {
		t.Fatalf("ActionSamples limited: %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("ActionSamples limit 1: got %d rows", len(limited))
	}
}

// TestTopPermissionEvents_AggregatesAndCountsDecisions (LEARN-TASKS.md LN-04):
// two allows and a deny for one (tool, pattern), one auto-decided row that
// must not count towards Count (it's already covered by a rule, not a
// candidate), and a different project's row that must not leak in.
func TestTopPermissionEvents_AggregatesAndCountsDecisions(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	events := []PermissionEvent{
		{Project: "p", Session: "S1", Tool: "Bash", Pattern: "go test ./...", Decision: "allow", Timestamp: now},
		{Project: "p", Session: "S1", Tool: "Bash", Pattern: "go test ./...", Decision: "allow_session", Timestamp: now},
		{Project: "p", Session: "S1", Tool: "Bash", Pattern: "go test ./...", Decision: "deny", Timestamp: now},
		// Already auto-decided (a rule covers this) — must not inflate Count.
		{Project: "p", Session: "S1", Tool: "Bash", Pattern: "go test ./...", Decision: "allow", Auto: true, Timestamp: now},
		// A different project must not leak into "p"'s stats.
		{Project: "other", Session: "S1", Tool: "Bash", Pattern: "go test ./...", Decision: "allow", Timestamp: now},
	}
	for _, ev := range events {
		if err := s.InsertPermissionEvent(ev); err != nil {
			t.Fatalf("InsertPermissionEvent: %v", err)
		}
	}

	stats, err := s.TopPermissionEvents("p", 30, 0)
	if err != nil {
		t.Fatalf("TopPermissionEvents: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("TopPermissionEvents: got %d groups, want 1", len(stats))
	}
	got := stats[0]
	if got.Tool != "Bash" || got.Pattern != "go test ./..." {
		t.Errorf("got tool/pattern = %q/%q", got.Tool, got.Pattern)
	}
	if got.Count != 3 {
		t.Errorf("Count = %d, want 3 (auto=1 row excluded)", got.Count)
	}
	if got.AllowCount != 2 {
		t.Errorf("AllowCount = %d, want 2", got.AllowCount)
	}
	if got.DenyCount != 1 {
		t.Errorf("DenyCount = %d, want 1", got.DenyCount)
	}
}

func TestPermissionEventsForProject(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()

	events := []PermissionEvent{
		{Project: "p", Session: "S1", Tool: "Bash", Pattern: "ls", Decision: "allow", Auto: true, Timestamp: now},
		{Project: "p", Session: "S1", Tool: "Bash", Pattern: "rm -rf /", Decision: "deny", Auto: false, Timestamp: now.Add(time.Second)},
		{Project: "other", Session: "S1", Tool: "Bash", Pattern: "ls", Decision: "allow", Auto: true, Timestamp: now},
	}
	for _, ev := range events {
		if err := s.InsertPermissionEvent(ev); err != nil {
			t.Fatalf("InsertPermissionEvent: %v", err)
		}
	}

	rows, err := s.PermissionEventsForProject("p")
	if err != nil {
		t.Fatalf("PermissionEventsForProject: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (other project excluded)", len(rows))
	}
	// Most recent (highest id) first.
	if rows[0].Pattern != "rm -rf /" || rows[0].Auto {
		t.Errorf("rows[0] = %+v, want the human-decided deny first", rows[0])
	}
	if rows[1].Pattern != "ls" || !rows[1].Auto {
		t.Errorf("rows[1] = %+v, want the auto-decided allow", rows[1])
	}
}

func TestTopPermissionEvents_SinceDaysExcludesOld(t *testing.T) {
	s := newTestStore(t)
	old := time.Now().UTC().AddDate(0, 0, -60)

	if err := s.InsertPermissionEvent(PermissionEvent{
		Project: "p", Session: "S1", Tool: "Bash", Pattern: "git status", Decision: "allow", Timestamp: old,
	}); err != nil {
		t.Fatalf("InsertPermissionEvent: %v", err)
	}

	stats, err := s.TopPermissionEvents("p", 30, 0)
	if err != nil {
		t.Fatalf("TopPermissionEvents: %v", err)
	}
	if len(stats) != 0 {
		t.Errorf("expected old row excluded by sinceDays window, got %+v", stats)
	}
}

func TestGetSetIngestOffset(t *testing.T) {
	s := newTestStore(t)

	_, _, ok, err := s.GetIngestOffset("cli-none")
	if err != nil {
		t.Fatalf("GetIngestOffset(missing): %v", err)
	}
	if ok {
		t.Error("expected ok=false for a never-indexed session")
	}

	if err := s.SetIngestOffset("cli-1", "/path/to/cli-1.jsonl", 1024); err != nil {
		t.Fatalf("SetIngestOffset: %v", err)
	}
	path, offset, ok, err := s.GetIngestOffset("cli-1")
	if err != nil {
		t.Fatalf("GetIngestOffset: %v", err)
	}
	if !ok || path != "/path/to/cli-1.jsonl" || offset != 1024 {
		t.Errorf("GetIngestOffset: got (%q, %d, %v)", path, offset, ok)
	}

	// Re-setting the same cli_session_id upserts rather than erroring.
	if err := s.SetIngestOffset("cli-1", "/path/to/cli-1.jsonl", 2048); err != nil {
		t.Fatalf("SetIngestOffset (update): %v", err)
	}
	_, offset, _, err = s.GetIngestOffset("cli-1")
	if err != nil {
		t.Fatalf("GetIngestOffset (after update): %v", err)
	}
	if offset != 2048 {
		t.Errorf("offset after update: got %d, want 2048", offset)
	}
}

// TestMigrateIsIdempotent opens the same on-disk database twice, exercising
// migrate()'s CREATE TABLE IF NOT EXISTS / ALTER TABLE-with-tolerated-error
// path a second time against tables that already exist and already hold
// data — the template every future additive column follows (see
// migrations.go) must not break on a database from a previous app version.
func TestMigrateIsIdempotent(t *testing.T) {
	f, err := os.CreateTemp("", "store_migrate_*.db")
	if err != nil {
		t.Fatalf("TempFile: %v", err)
	}
	f.Close()
	path := f.Name()
	defer os.Remove(path)

	s1, err := New(path)
	if err != nil {
		t.Fatalf("New (first open): %v", err)
	}
	if err := s1.SetIngestOffset("cli-1", "/a.jsonl", 10); err != nil {
		t.Fatalf("SetIngestOffset: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := New(path)
	if err != nil {
		t.Fatalf("New (second open, re-runs migrate): %v", err)
	}
	defer s2.Close()

	_, offset, ok, err := s2.GetIngestOffset("cli-1")
	if err != nil {
		t.Fatalf("GetIngestOffset after re-migration: %v", err)
	}
	if !ok || offset != 10 {
		t.Errorf("data lost across re-migration: got (%d, %v)", offset, ok)
	}
}

// --- imported_logfiles (LEARN-TASKS.md LN-17) ---

func TestIsMarkLogFileImported(t *testing.T) {
	s := newTestStore(t)
	mtime := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)

	imported, err := s.IsLogFileImported("proj", "S1-20260908.md", 1234, mtime)
	if err != nil {
		t.Fatalf("IsLogFileImported: %v", err)
	}
	if imported {
		t.Error("expected imported=false before MarkLogFileImported")
	}

	if err := s.MarkLogFileImported("proj", "S1-20260908.md", 1234, mtime); err != nil {
		t.Fatalf("MarkLogFileImported: %v", err)
	}

	imported, err = s.IsLogFileImported("proj", "S1-20260908.md", 1234, mtime)
	if err != nil {
		t.Fatalf("IsLogFileImported (after mark): %v", err)
	}
	if !imported {
		t.Error("expected imported=true after MarkLogFileImported")
	}

	// A different size or mtime for the same name is a different file — not
	// considered already imported (the file was rewritten/appended).
	imported, err = s.IsLogFileImported("proj", "S1-20260908.md", 9999, mtime)
	if err != nil {
		t.Fatalf("IsLogFileImported (different size): %v", err)
	}
	if imported {
		t.Error("expected imported=false for a changed file size")
	}

	// Re-marking the same triple is a no-op, not an error (idempotent).
	if err := s.MarkLogFileImported("proj", "S1-20260908.md", 1234, mtime); err != nil {
		t.Fatalf("MarkLogFileImported (re-mark): %v", err)
	}
}

// --- skills (LEARN-TASKS.md LN-09) ---

func TestInsertGetSkill(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	sk := &Skill{
		Project:    "proj",
		Name:       "git-session-preamble",
		Status:     "draft",
		DraftJSON:  `{"name":"git-session-preamble"}`,
		MD:         "---\nname: git-session-preamble\n---\n",
		SourceJSON: `["Bash:git status","Bash:git branch -a"]`,
		CreatedAt:  now,
	}
	if err := s.InsertSkill(sk); err != nil {
		t.Fatalf("InsertSkill: %v", err)
	}
	if sk.ID == 0 {
		t.Fatal("expected non-zero ID")
	}

	got, err := s.GetSkill(sk.ID)
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got == nil {
		t.Fatal("GetSkill returned nil")
	}
	if got.Project != sk.Project || got.Name != sk.Name || got.Status != sk.Status {
		t.Errorf("unexpected skill: %+v", got)
	}
	if got.DraftJSON != sk.DraftJSON || got.MD != sk.MD || got.SourceJSON != sk.SourceJSON {
		t.Errorf("unexpected skill content: %+v", got)
	}
	if !got.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt: got %v want %v", got.CreatedAt, now)
	}
	if got.ApprovedAt != nil || got.ArchivedAt != nil {
		t.Errorf("expected nil ApprovedAt/ArchivedAt on a fresh draft, got %+v / %+v", got.ApprovedAt, got.ArchivedAt)
	}
}

func TestGetSkillNotFound(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetSkill(999)
	if err != nil {
		t.Fatalf("GetSkill(missing): %v", err)
	}
	if got != nil {
		t.Errorf("expected nil, got %+v", got)
	}
}

// --- skills (LEARN-TASKS.md LN-10) ---

func TestListSkills(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC().Truncate(time.Second)

	mk := func(project, name string, at time.Time) *Skill {
		sk := &Skill{
			Project: project, Name: name, Status: "draft",
			DraftJSON: "{}", MD: "body", SourceJSON: "[]", CreatedAt: at,
		}
		if err := s.InsertSkill(sk); err != nil {
			t.Fatalf("InsertSkill: %v", err)
		}
		return sk
	}
	older := mk("proj", "older-skill", now.Add(-time.Hour))
	newer := mk("proj", "newer-skill", now)
	mk("other-proj", "other-skill", now)

	got, err := s.ListSkills("proj")
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ListSkills(proj) = %d rows, want 2: %+v", len(got), got)
	}
	// Newest first.
	if got[0].ID != newer.ID || got[1].ID != older.ID {
		t.Errorf("ListSkills order = [%d,%d], want [%d,%d]", got[0].ID, got[1].ID, newer.ID, older.ID)
	}

	empty, err := s.ListSkills("no-such-project")
	if err != nil {
		t.Fatalf("ListSkills(missing project): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("ListSkills(missing project) = %+v, want empty", empty)
	}
}

func TestUpdateSkillApproved(t *testing.T) {
	s := newTestStore(t)
	sk := &Skill{
		Project: "proj", Name: "my-skill", Status: "draft",
		DraftJSON: "{}", MD: "draft body", SourceJSON: "[]", CreatedAt: time.Now().UTC(),
	}
	if err := s.InsertSkill(sk); err != nil {
		t.Fatalf("InsertSkill: %v", err)
	}

	approvedAt := time.Now().UTC().Truncate(time.Second)
	if err := s.UpdateSkillApproved(sk.ID, "edited body", approvedAt); err != nil {
		t.Fatalf("UpdateSkillApproved: %v", err)
	}

	got, err := s.GetSkill(sk.ID)
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got.Status != "approved" {
		t.Errorf("Status = %q, want approved", got.Status)
	}
	if got.MD != "edited body" {
		t.Errorf("MD = %q, want %q", got.MD, "edited body")
	}
	if got.ApprovedAt == nil || !got.ApprovedAt.Equal(approvedAt) {
		t.Errorf("ApprovedAt = %v, want %v", got.ApprovedAt, approvedAt)
	}
	if got.ArchivedAt != nil {
		t.Errorf("ArchivedAt = %v, want nil", got.ArchivedAt)
	}
	// DraftJSON is untouched by approval — it stays the original distillation.
	if got.DraftJSON != "{}" {
		t.Errorf("DraftJSON was modified: %q", got.DraftJSON)
	}
}

func TestUpdateSkillArchived(t *testing.T) {
	s := newTestStore(t)
	sk := &Skill{
		Project: "proj", Name: "my-skill", Status: "draft",
		DraftJSON: "{}", MD: "body", SourceJSON: "[]", CreatedAt: time.Now().UTC(),
	}
	if err := s.InsertSkill(sk); err != nil {
		t.Fatalf("InsertSkill: %v", err)
	}

	archivedAt := time.Now().UTC().Truncate(time.Second)
	if err := s.UpdateSkillArchived(sk.ID, archivedAt); err != nil {
		t.Fatalf("UpdateSkillArchived: %v", err)
	}

	got, err := s.GetSkill(sk.ID)
	if err != nil {
		t.Fatalf("GetSkill: %v", err)
	}
	if got.Status != "archived" {
		t.Errorf("Status = %q, want archived", got.Status)
	}
	if got.ArchivedAt == nil || !got.ArchivedAt.Equal(archivedAt) {
		t.Errorf("ArchivedAt = %v, want %v", got.ArchivedAt, archivedAt)
	}
	// MD is left as whatever it was (archiving a draft never approved).
	if got.MD != "body" {
		t.Errorf("MD was modified: %q", got.MD)
	}
}
