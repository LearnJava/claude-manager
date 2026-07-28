package analysis

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"claude-manager/internal/store"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "test.db"))
	if err != nil {
		// Store requires CGO + sqlite3. Skip on builds without it.
		t.Skipf("store unavailable: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
		_ = os.RemoveAll(dir)
	})
	return st
}

func samplePlan() *TaskPlan {
	return NewPlanFromAnalysis("acme", "Refactor auth", &AnalysisResult{
		Feasibility:         FeasibilityInfo{SingleSession: false, EstimatedComplexity: "large"},
		RecommendedApproach: "sequential_sessions",
		SharedContext:       "Project shares one auth module",
		Subtasks: []PlannedSubtask{
			{ID: "a", Name: "core", Prompt: "Implement OAuth2 core"},
			{ID: "b", Name: "handlers", Prompt: "Update HTTP handlers"},
			{ID: "c", Name: "tests", Prompt: "Write tests"},
		},
		ExecutionOrder: [][]string{{"a"}, {"b"}, {"c"}},
		CostUSD:        0.05,
	})
}

func TestNewPlanFromAnalysisSetsPending(t *testing.T) {
	plan := samplePlan()

	if plan.Status != PlanStatusDraft {
		t.Errorf("status: want draft, got %s", plan.Status)
	}
	if plan.TotalCostUSD != 0.05 {
		t.Errorf("cost: want 0.05 (carried over), got %v", plan.TotalCostUSD)
	}
	for _, s := range plan.Subtasks {
		if s.Status != SubtaskStatusPending {
			t.Errorf("subtask %s: want pending, got %s", s.ID, s.Status)
		}
	}
}

func TestIndexSubtasks(t *testing.T) {
	subs := []PlannedSubtask{
		{ID: "a"}, {ID: "b"}, {ID: "c"},
	}
	idx := indexSubtasks(subs)
	if len(idx) != 3 {
		t.Fatalf("want 3 entries, got %d", len(idx))
	}
	if idx["b"].idx != 1 {
		t.Errorf("b should be at index 1, got %d", idx["b"].idx)
	}
}

func TestBuildContextSummaryEmpty(t *testing.T) {
	if got := buildContextSummary("", nil); got != "" {
		t.Errorf("empty inputs should yield empty string, got %q", got)
	}
}

func TestBuildContextSummaryWithShared(t *testing.T) {
	got := buildContextSummary("  use Postgres  ", nil)
	if !strings.Contains(got, "Shared context") {
		t.Errorf("missing shared context header: %q", got)
	}
	if !strings.Contains(got, "use Postgres") {
		t.Errorf("missing shared context body: %q", got)
	}
}

func TestBuildContextSummaryWithCompleted(t *testing.T) {
	done := []PlannedSubtask{
		{ID: "a", Name: "auth-core", ResultSummary: "OAuth2 done", FilesChanged: []string{"auth/provider.go", "auth/middleware.go"}},
		{ID: "b", Name: "handlers", ResultSummary: "Handlers updated"},
	}
	got := buildContextSummary("", done)
	for _, want := range []string{
		"Previous subtasks completed",
		"auth-core (id a)",
		"OAuth2 done",
		"auth/provider.go, auth/middleware.go",
		"handlers (id b)",
		"Continue with this state",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary missing %q. full output:\n%s", want, got)
		}
	}
}

func TestExecutePlanSequentialPassesContext(t *testing.T) {
	plan := samplePlan()
	st := newTestStore(t)

	var (
		mu       sync.Mutex
		contexts = make(map[string]string)
		callOrder []string
	)
	exec := SubtaskExecutorFunc(func(ctx context.Context, projectPath string, sub PlannedSubtask, contextAppend string) (*SubtaskResult, error) {
		mu.Lock()
		contexts[sub.ID] = contextAppend
		callOrder = append(callOrder, sub.ID)
		mu.Unlock()
		return &SubtaskResult{
			SessionID:    "sess-" + sub.ID,
			Summary:      "did " + sub.Name,
			FilesChanged: []string{sub.ID + ".go"},
			CostUSD:      0.10,
			InputTokens:  1000,
			OutputTokens: 200,
		}, nil
	})

	if err := ExecutePlan(context.Background(), plan, "/tmp/proj", exec, st); err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}

	if got := callOrder; len(got) != 3 || got[0] != "a" || got[1] != "b" || got[2] != "c" {
		t.Errorf("call order: want [a b c], got %v", got)
	}

	// Subtask a should receive only the shared context (no completed work yet).
	if ca := contexts["a"]; strings.Contains(ca, "Previous subtasks") {
		t.Errorf("first subtask should not see prior summaries, got: %q", ca)
	}
	if !strings.Contains(contexts["a"], "Shared context") {
		t.Errorf("first subtask should see shared context, got: %q", contexts["a"])
	}

	// Subtask b should see a's summary and files.
	if cb := contexts["b"]; !strings.Contains(cb, "did core") || !strings.Contains(cb, "a.go") {
		t.Errorf("b context missing a's results: %q", cb)
	}

	// Subtask c should see both a and b.
	if cc := contexts["c"]; !strings.Contains(cc, "did core") || !strings.Contains(cc, "did handlers") {
		t.Errorf("c context missing prior summaries: %q", cc)
	}

	if plan.Status != PlanStatusCompleted {
		t.Errorf("plan status: want completed, got %s", plan.Status)
	}
	if plan.TotalCostUSD <= 0.05 { // started at 0.05 (analyst), +0.30 from 3 subtasks
		t.Errorf("expected accumulated cost > 0.05, got %v", plan.TotalCostUSD)
	}
	for _, s := range plan.Subtasks {
		if s.Status != SubtaskStatusCompleted {
			t.Errorf("subtask %s: want completed, got %s", s.ID, s.Status)
		}
		if s.SessionID == "" {
			t.Errorf("subtask %s: SessionID not set", s.ID)
		}
	}
}

func TestExecutePlanParallelGroup(t *testing.T) {
	plan := NewPlanFromAnalysis("acme", "Mechanical refactor", &AnalysisResult{
		Subtasks: []PlannedSubtask{
			{ID: "a", Name: "rename1", Prompt: "rename in module 1"},
			{ID: "b", Name: "rename2", Prompt: "rename in module 2"},
			{ID: "c", Name: "rename3", Prompt: "rename in module 3"},
		},
		ExecutionOrder: [][]string{{"a", "b", "c"}},
	})

	var running atomic.Int32
	var maxRunning atomic.Int32
	exec := SubtaskExecutorFunc(func(ctx context.Context, projectPath string, sub PlannedSubtask, contextAppend string) (*SubtaskResult, error) {
		cur := running.Add(1)
		defer running.Add(-1)
		for {
			old := maxRunning.Load()
			if cur <= old || maxRunning.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		return &SubtaskResult{Summary: "done"}, nil
	})

	if err := ExecutePlan(context.Background(), plan, "", exec, nil); err != nil {
		t.Fatalf("ExecutePlan: %v", err)
	}
	if maxRunning.Load() < 2 {
		t.Errorf("expected at least 2 concurrent executions, got max=%d", maxRunning.Load())
	}
}

func TestExecutePlanSubtaskFailure(t *testing.T) {
	plan := NewPlanFromAnalysis("acme", "task", &AnalysisResult{
		Subtasks: []PlannedSubtask{
			{ID: "a", Name: "good", Prompt: "ok"},
			{ID: "b", Name: "bad", Prompt: "boom"},
			{ID: "c", Name: "never", Prompt: "should not run"},
		},
		ExecutionOrder: [][]string{{"a"}, {"b"}, {"c"}},
	})

	var ran []string
	var mu sync.Mutex
	exec := SubtaskExecutorFunc(func(ctx context.Context, projectPath string, sub PlannedSubtask, contextAppend string) (*SubtaskResult, error) {
		mu.Lock()
		ran = append(ran, sub.ID)
		mu.Unlock()
		if sub.ID == "b" {
			return nil, &fakeErr{"boom"}
		}
		return &SubtaskResult{Summary: "ok"}, nil
	})

	err := ExecutePlan(context.Background(), plan, "", exec, nil)
	if err == nil {
		t.Fatal("expected error from failed subtask")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should mention boom, got %v", err)
	}
	if plan.Status != PlanStatusFailed {
		t.Errorf("status: want failed, got %s", plan.Status)
	}
	if len(ran) != 2 || ran[0] != "a" || ran[1] != "b" {
		t.Errorf("c should not have run; ran=%v", ran)
	}
	if plan.Subtasks[1].Status != SubtaskStatusFailed {
		t.Errorf("subtask b should be failed, got %s", plan.Subtasks[1].Status)
	}
}

func TestExecutePlanNilExecutor(t *testing.T) {
	plan := samplePlan()
	if err := ExecutePlan(context.Background(), plan, "", nil, nil); err == nil {
		t.Error("expected error with nil executor")
	}
}

func TestExecutePlanNilPlan(t *testing.T) {
	exec := SubtaskExecutorFunc(func(ctx context.Context, p string, s PlannedSubtask, c string) (*SubtaskResult, error) {
		return nil, nil
	})
	if err := ExecutePlan(context.Background(), nil, "", exec, nil); err == nil {
		t.Error("expected error with nil plan")
	}
}

func TestExecutePlanUnknownSubtaskID(t *testing.T) {
	plan := NewPlanFromAnalysis("acme", "task", &AnalysisResult{
		Subtasks:       []PlannedSubtask{{ID: "a", Name: "a"}},
		ExecutionOrder: [][]string{{"a", "missing"}},
	})
	exec := SubtaskExecutorFunc(func(ctx context.Context, p string, s PlannedSubtask, c string) (*SubtaskResult, error) {
		return &SubtaskResult{Summary: "ok"}, nil
	})
	err := ExecutePlan(context.Background(), plan, "", exec, nil)
	if err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("expected error mentioning missing subtask, got %v", err)
	}
}

func TestSavePlanInsertThenUpdate(t *testing.T) {
	plan := samplePlan()
	st := newTestStore(t)

	if err := SavePlan(st, plan); err != nil {
		t.Fatalf("first save: %v", err)
	}
	if plan.ID == 0 {
		t.Fatal("plan ID should be set after insert")
	}

	loaded, err := LoadPlan(st, plan.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded plan is nil")
	}
	if loaded.OriginalTask != plan.OriginalTask {
		t.Errorf("original_task: want %q, got %q", plan.OriginalTask, loaded.OriginalTask)
	}
	if len(loaded.Subtasks) != len(plan.Subtasks) {
		t.Fatalf("subtask count: want %d, got %d", len(plan.Subtasks), len(loaded.Subtasks))
	}
	if loaded.Status != PlanStatusDraft {
		t.Errorf("status: want draft, got %s", loaded.Status)
	}

	plan.Status = PlanStatusCompleted
	plan.Subtasks[0].Status = SubtaskStatusCompleted
	plan.Subtasks[0].ResultSummary = "ok"
	plan.Subtasks[0].FilesChanged = []string{"x.go"}
	plan.TotalCostUSD = 1.23
	if err := SavePlan(st, plan); err != nil {
		t.Fatalf("update save: %v", err)
	}

	loaded2, err := LoadPlan(st, plan.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if loaded2.Status != PlanStatusCompleted {
		t.Errorf("reloaded status: want completed, got %s", loaded2.Status)
	}
	if loaded2.TotalCostUSD != 1.23 {
		t.Errorf("reloaded cost: want 1.23, got %v", loaded2.TotalCostUSD)
	}
	if loaded2.Subtasks[0].Status != SubtaskStatusCompleted {
		t.Errorf("reloaded subtask status: want completed, got %s", loaded2.Subtasks[0].Status)
	}
	if loaded2.Subtasks[0].ResultSummary != "ok" {
		t.Errorf("reloaded subtask summary: want ok, got %s", loaded2.Subtasks[0].ResultSummary)
	}
	if len(loaded2.Subtasks[0].FilesChanged) != 1 || loaded2.Subtasks[0].FilesChanged[0] != "x.go" {
		t.Errorf("reloaded files: want [x.go], got %v", loaded2.Subtasks[0].FilesChanged)
	}
}

func TestLoadPlanRestoresPlanningFieldsFromAnalysis(t *testing.T) {
	// plan_subtasks has no columns for summary/estimate/files_to_touch — they
	// only survive in the analysis blob. ApproveRoadmap renders task files from
	// a freshly loaded plan, so losing them here would silently strip the
	// metadata out of every tasks/NN-*.md file.
	plan := samplePlan()
	plan.Subtasks[0].Summary = "Short label"
	plan.Subtasks[0].EstimatedTokens = 80000
	plan.Subtasks[0].FilesToTouch = []string{"a.go"}
	plan.Subtasks[0].Effort = "high"
	plan.Subtasks[0].UseWorktree = true
	plan.Analysis.Subtasks = plan.Subtasks

	st := newTestStore(t)
	if err := SavePlan(st, plan); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadPlan(st, plan.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := loaded.Subtasks[0]
	if got.Summary != "Short label" {
		t.Errorf("summary: want %q, got %q", "Short label", got.Summary)
	}
	if got.EstimatedTokens != 80000 {
		t.Errorf("estimated_tokens: want 80000, got %d", got.EstimatedTokens)
	}
	if len(got.FilesToTouch) != 1 || got.FilesToTouch[0] != "a.go" {
		t.Errorf("files_to_touch: want [a.go], got %v", got.FilesToTouch)
	}
	if got.Effort != "high" {
		t.Errorf("effort: want high, got %q", got.Effort)
	}
	if !got.UseWorktree {
		t.Error("use_worktree: want true")
	}
}

func TestRestorePlanningFields_KeepsOperatorEdits(t *testing.T) {
	subs := []PlannedSubtask{{ID: "a", Summary: "edited in the UI", EstimatedTokens: 10}}
	fromAnalysis := []PlannedSubtask{{ID: "a", Summary: "from the analyst", EstimatedTokens: 999}}
	restorePlanningFields(subs, fromAnalysis)
	if subs[0].Summary != "edited in the UI" {
		t.Errorf("existing summary was overwritten: %q", subs[0].Summary)
	}
	if subs[0].EstimatedTokens != 10 {
		t.Errorf("existing estimate was overwritten: %d", subs[0].EstimatedTokens)
	}
}

func TestRestorePlanningFields_IgnoresUnknownAndEmpty(t *testing.T) {
	subs := []PlannedSubtask{{ID: "a"}}
	restorePlanningFields(subs, nil)
	restorePlanningFields(nil, []PlannedSubtask{{ID: "a", Summary: "x"}})
	restorePlanningFields(subs, []PlannedSubtask{{ID: "other", Summary: "x"}})
	if subs[0].Summary != "" {
		t.Errorf("unrelated subtask leaked in: %q", subs[0].Summary)
	}
}

func TestSavePlanNilStore(t *testing.T) {
	if err := SavePlan(nil, samplePlan()); err != nil {
		t.Errorf("nil store should be no-op, got %v", err)
	}
}

func TestLoadPlanMissing(t *testing.T) {
	st := newTestStore(t)
	p, err := LoadPlan(st, 9999)
	if err != nil {
		t.Fatalf("LoadPlan: %v", err)
	}
	if p != nil {
		t.Errorf("expected nil for missing plan, got %+v", p)
	}
}

func TestSubtaskExecutorFunc(t *testing.T) {
	called := false
	f := SubtaskExecutorFunc(func(ctx context.Context, p string, s PlannedSubtask, c string) (*SubtaskResult, error) {
		called = true
		return &SubtaskResult{Summary: "ok"}, nil
	})
	res, err := f.Execute(context.Background(), "/tmp", PlannedSubtask{ID: "x"}, "")
	if err != nil || res == nil || res.Summary != "ok" {
		t.Errorf("unexpected: res=%v err=%v", res, err)
	}
	if !called {
		t.Error("function not called")
	}
}

// --- helpers ---

type fakeErr struct{ msg string }

func (e *fakeErr) Error() string { return e.msg }

// Compile-time guard: SubtaskExecutorFunc implements SubtaskExecutor.
var _ SubtaskExecutor = SubtaskExecutorFunc(nil)
