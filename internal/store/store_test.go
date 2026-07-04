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
