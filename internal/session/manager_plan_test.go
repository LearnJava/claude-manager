package session

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/analysis"
)

// stubAnalyzer returns a fixed AnalysisResult, recording the received task.
func stubAnalyzer(result *analysis.AnalysisResult) (analyzeFn, *[]string) {
	var tasks []string
	fn := func(_ context.Context, _ string, task string, _ analysis.AnalysisConfig) (*analysis.AnalysisResult, error) {
		tasks = append(tasks, task)
		return result, nil
	}
	return fn, &tasks
}

func twoStepAnalysis() *analysis.AnalysisResult {
	return &analysis.AnalysisResult{
		RecommendedModel: "sonnet",
		Subtasks: []analysis.PlannedSubtask{
			{ID: "s1", Name: "first", Prompt: "do first"},
			{ID: "s2", Name: "second", Prompt: "do second", DependsOn: []string{"s1"}},
		},
		ExecutionOrder: [][]string{{"s1"}, {"s2"}},
		SharedContext:  "shared notes",
	}
}

func TestRunPreflightSavesDraftPlan(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	analyze, tasks := stubAnalyzer(twoStepAnalysis())

	plan, err := m.runPreflight(context.Background(), "lumen", "build a parser", analyze)
	if err != nil {
		t.Fatalf("runPreflight: %v", err)
	}
	if plan.ID == 0 {
		t.Error("plan must receive a store ID on save")
	}
	if plan.Status != analysis.PlanStatusDraft {
		t.Errorf("Status = %q, want draft", plan.Status)
	}
	if len(*tasks) != 1 || (*tasks)[0] != "build a parser" {
		t.Errorf("analyzer received tasks %v", *tasks)
	}

	// The plan must be reloadable with its subtasks.
	loaded, err := m.GetPlan(plan.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if loaded == nil || len(loaded.Subtasks) != 2 {
		t.Fatalf("reloaded plan wrong: %+v", loaded)
	}
}

func TestRunPreflightUnknownProject(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	analyze, _ := stubAnalyzer(twoStepAnalysis())
	if _, err := m.runPreflight(context.Background(), "no-such", "task", analyze); err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestApprovePlanAssignsIDAndStatus(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	plan := analysis.NewPlanFromAnalysis("lumen", "task", twoStepAnalysis())

	approved, err := m.ApprovePlan(plan)
	if err != nil {
		t.Fatalf("ApprovePlan: %v", err)
	}
	if approved.ID == 0 {
		t.Error("ApprovePlan must persist the plan and assign an ID")
	}
	if approved.Status != analysis.PlanStatusApproved {
		t.Errorf("Status = %q, want approved", approved.Status)
	}

	loaded, err := m.GetPlan(approved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != analysis.PlanStatusApproved {
		t.Errorf("persisted status = %q, want approved", loaded.Status)
	}
}

func TestApprovePlanNil(t *testing.T) {
	m := newTestManager(t)
	if _, err := m.ApprovePlan(nil); err == nil {
		t.Fatal("expected error for nil plan")
	}
}

// recordingExecutor completes every subtask and records the contextAppend each
// call received, to verify inter-group context handoff.
type recordingExecutor struct {
	contexts map[string]string
	failID   string
}

func (r *recordingExecutor) Execute(_ context.Context, _ string, sub analysis.PlannedSubtask, contextAppend string) (*analysis.SubtaskResult, error) {
	if r.contexts == nil {
		r.contexts = map[string]string{}
	}
	r.contexts[sub.ID] = contextAppend
	if sub.ID == r.failID {
		return nil, errors.New("boom")
	}
	return &analysis.SubtaskResult{
		SessionID: "sess-" + sub.ID,
		Summary:   "done " + sub.ID,
		CostUSD:   0.1,
	}, nil
}

func TestExecutePlanCompletesAndHandsOffContext(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	plan := analysis.NewPlanFromAnalysis("lumen", "task", twoStepAnalysis())
	if _, err := m.ApprovePlan(plan); err != nil {
		t.Fatal(err)
	}

	ex := &recordingExecutor{}
	if err := m.executePlan(context.Background(), plan.ID, ex); err != nil {
		t.Fatalf("executePlan: %v", err)
	}

	// Group 2 must receive the summary of group 1 via contextAppend.
	if !strings.Contains(ex.contexts["s2"], "done s1") {
		t.Errorf("s2 contextAppend missing s1 summary: %q", ex.contexts["s2"])
	}

	loaded, err := m.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != analysis.PlanStatusCompleted {
		t.Errorf("plan status = %q, want completed", loaded.Status)
	}
	for _, s := range loaded.Subtasks {
		if s.Status != analysis.SubtaskStatusCompleted {
			t.Errorf("subtask %s status = %q, want completed", s.ID, s.Status)
		}
	}
}

func TestExecutePlanFailureMarksPlanFailed(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	plan := analysis.NewPlanFromAnalysis("lumen", "task", twoStepAnalysis())
	if _, err := m.ApprovePlan(plan); err != nil {
		t.Fatal(err)
	}

	ex := &recordingExecutor{failID: "s1"}
	if err := m.executePlan(context.Background(), plan.ID, ex); err == nil {
		t.Fatal("expected error when a subtask fails")
	}

	loaded, err := m.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != analysis.PlanStatusFailed {
		t.Errorf("plan status = %q, want failed", loaded.Status)
	}
}

func TestExecutePlanMissingPlan(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	if err := m.executePlan(context.Background(), 9999, &recordingExecutor{}); err == nil {
		t.Fatal("expected error for missing plan")
	}
}

// ---- Roadmap generation / approval ----

func TestGenerateRoadmapSavesRoadmapKindPlan(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	analyze, tasks := stubAnalyzer(twoStepAnalysis())

	plan, err := m.generateRoadmap(context.Background(), "lumen", "build a chat app", "", analyze)
	if err != nil {
		t.Fatalf("generateRoadmap: %v", err)
	}
	if plan.ID == 0 {
		t.Error("plan must receive a store ID on save")
	}
	if plan.Kind != analysis.PlanKindRoadmap {
		t.Errorf("Kind = %q, want roadmap", plan.Kind)
	}
	if len(*tasks) != 1 || (*tasks)[0] != "build a chat app" {
		t.Errorf("analyzer received tasks %v", *tasks)
	}

	loaded, err := m.GetPlan(plan.ID)
	if err != nil {
		t.Fatalf("GetPlan: %v", err)
	}
	if loaded == nil || loaded.Kind != analysis.PlanKindRoadmap {
		t.Fatalf("reloaded plan lost its Kind: %+v", loaded)
	}
}

func TestGenerateRoadmapUnknownProject(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	analyze, _ := stubAnalyzer(twoStepAnalysis())
	if _, err := m.generateRoadmap(context.Background(), "no-such", "idea", "", analyze); err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestExecutePlanRejectsRoadmapKind(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	analyze, _ := stubAnalyzer(twoStepAnalysis())
	plan, err := m.generateRoadmap(context.Background(), "lumen", "idea", "", analyze)
	if err != nil {
		t.Fatal(err)
	}

	err = m.executePlan(context.Background(), plan.ID, &recordingExecutor{})
	if err == nil {
		t.Fatal("expected executePlan to reject a roadmap-kind plan")
	}
	if !strings.Contains(err.Error(), "roadmap") {
		t.Errorf("error should mention it is a roadmap plan, got: %v", err)
	}
}

func TestApproveRoadmapFilesWritesAndCompletesPlan(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	analyze, _ := stubAnalyzer(twoStepAnalysis())
	plan, err := m.generateRoadmap(context.Background(), "lumen", "idea", "", analyze)
	if err != nil {
		t.Fatal(err)
	}

	projectPath, err := m.projectPath("lumen")
	if err != nil {
		t.Fatal(err)
	}

	approved, roadmapPath, statusPath, err := m.ApproveRoadmapFiles(plan.ID, false)
	if err != nil {
		t.Fatalf("ApproveRoadmapFiles: %v", err)
	}
	if approved.Status != analysis.PlanStatusCompleted {
		t.Errorf("Status = %q, want completed", approved.Status)
	}
	if roadmapPath != filepath.Join(projectPath, "ROADMAP.md") {
		t.Errorf("unexpected roadmap path: %s", roadmapPath)
	}
	if _, err := os.Stat(roadmapPath); err != nil {
		t.Errorf("ROADMAP.md was not written: %v", err)
	}
	if _, err := os.Stat(statusPath); err != nil {
		t.Errorf("STATUS-P1.md was not written: %v", err)
	}

	loaded, err := m.GetPlan(plan.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != analysis.PlanStatusCompleted {
		t.Errorf("persisted status = %q, want completed", loaded.Status)
	}
}

func TestApproveRoadmapFilesRejectsAdhocKind(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	plan := analysis.NewPlanFromAnalysis("lumen", "task", twoStepAnalysis())
	if _, err := m.ApprovePlan(plan); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := m.ApproveRoadmapFiles(plan.ID, false); err == nil {
		t.Fatal("expected error approving an ad-hoc-kind plan as a roadmap")
	}
}

func TestApproveRoadmapFilesMissingPlan(t *testing.T) {
	m, _ := newTestManagerWithStore(t)
	if _, _, _, err := m.ApproveRoadmapFiles(9999, false); err == nil {
		t.Fatal("expected error for missing plan")
	}
}
