package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"claude-manager/internal/logger"
	"claude-manager/internal/store"
)

// ---- Status enums (PLAN.md section 17.9) ----

// PlanStatus tracks the lifecycle of a TaskPlan.
type PlanStatus string

const (
	PlanStatusDraft     PlanStatus = "draft"
	PlanStatusApproved  PlanStatus = "approved"
	PlanStatusExecuting PlanStatus = "executing"
	PlanStatusCompleted PlanStatus = "completed"
	PlanStatusFailed    PlanStatus = "failed"
)

// SubtaskStatus tracks the lifecycle of a single subtask in a plan.
type SubtaskStatus string

const (
	SubtaskStatusPending   SubtaskStatus = "pending"
	SubtaskStatusRunning   SubtaskStatus = "running"
	SubtaskStatusCompleted SubtaskStatus = "completed"
	SubtaskStatusFailed    SubtaskStatus = "failed"
)

// PlanKind distinguishes an ad-hoc, immediately-executed TaskPlan from one
// meant to be materialized into a project's ROADMAP.md/STATUS-P1.md instead
// (see WriteRoadmapFiles). ExecutePlan refuses to run a PlanKindRoadmap plan.
type PlanKind string

const (
	PlanKindAdhoc   PlanKind = "adhoc"
	PlanKindRoadmap PlanKind = "roadmap"
)

// ---- Data model (PLAN.md section 17.9) ----

// PlannedSubtask is a single executable unit produced by the analyst and
// updated as the plan executes. Fields up to FilesToTouch come from the
// analyst JSON; SessionID/Status/ResultSummary/FilesChanged are runtime.
type PlannedSubtask struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Prompt          string   `json:"prompt"`
	DependsOn       []string `json:"depends_on,omitempty"`
	Model           string   `json:"model,omitempty"`
	Effort          string   `json:"effort,omitempty"`
	UseWorktree     bool     `json:"use_worktree,omitempty"`
	EstimatedTokens int      `json:"estimated_tokens,omitempty"`
	FilesToTouch    []string `json:"files_to_touch,omitempty"`

	// Runtime fields, populated during ExecutePlan.
	SessionID     string        `json:"session_id,omitempty"`
	Status        SubtaskStatus `json:"status,omitempty"`
	ResultSummary string        `json:"result_summary,omitempty"`
	FilesChanged  []string      `json:"files_changed,omitempty"`
	CostUSD       float64       `json:"cost_usd,omitempty"`
}

// TaskPlan is the top-level plan record persisted to SQLite and rendered in
// the Plan Review UI. PLAN.md section 17.9.
type TaskPlan struct {
	ID             int64            `json:"id"`
	Project        string           `json:"project"`
	OriginalTask   string           `json:"original_task"`
	Analysis       AnalysisResult   `json:"analysis"`
	Subtasks       []PlannedSubtask `json:"subtasks"`
	ExecutionOrder [][]string       `json:"execution_order"`
	Status         PlanStatus       `json:"status"`
	Kind           PlanKind         `json:"kind,omitempty"`
	SharedContext  string           `json:"shared_context"`
	CreatedAt      time.Time        `json:"created_at"`
	CompletedAt    *time.Time       `json:"completed_at,omitempty"`
	TotalCostUSD   float64          `json:"total_cost_usd"`
	TotalTokens    int64            `json:"total_tokens"`
}

// NewPlanFromAnalysis builds a TaskPlan in `draft` status from an analyst
// result. Subtasks are copied so later runtime mutations don't bleed back
// into the analysis blob saved to the store.
func NewPlanFromAnalysis(project, originalTask string, ar *AnalysisResult) *TaskPlan {
	subtasks := make([]PlannedSubtask, len(ar.Subtasks))
	for i, s := range ar.Subtasks {
		s.Status = SubtaskStatusPending
		subtasks[i] = s
	}
	return &TaskPlan{
		Project:        project,
		OriginalTask:   originalTask,
		Analysis:       *ar,
		Subtasks:       subtasks,
		ExecutionOrder: ar.ExecutionOrder,
		Status:         PlanStatusDraft,
		Kind:           PlanKindAdhoc,
		SharedContext:  ar.SharedContext,
		CreatedAt:      time.Now(),
		TotalCostUSD:   ar.CostUSD,
	}
}

// ---- Subtask execution interface ----

// SubtaskResult is what a SubtaskExecutor reports back to ExecutePlan.
type SubtaskResult struct {
	SessionID    string
	Summary      string
	FilesChanged []string
	CostUSD      float64
	InputTokens  int64
	OutputTokens int64
}

// SubtaskExecutor abstracts the act of running one subtask. The session
// package implements this against a real Claude CLI; tests can pass a stub.
// The contextAppend string holds the rolling summary of prior groups; the
// executor MUST forward it via --append-system-prompt for context handoff.
type SubtaskExecutor interface {
	Execute(ctx context.Context, projectPath string, subtask PlannedSubtask, contextAppend string) (*SubtaskResult, error)
}

// SubtaskExecutorFunc adapts a plain function to the SubtaskExecutor interface.
type SubtaskExecutorFunc func(ctx context.Context, projectPath string, subtask PlannedSubtask, contextAppend string) (*SubtaskResult, error)

// Execute satisfies SubtaskExecutor.
func (f SubtaskExecutorFunc) Execute(ctx context.Context, projectPath string, subtask PlannedSubtask, contextAppend string) (*SubtaskResult, error) {
	return f(ctx, projectPath, subtask, contextAppend)
}

// ---- Execution ----

// ExecutePlan walks the plan's ExecutionOrder groups, runs each group's
// subtasks in parallel via the executor, then waits for the group to finish
// before launching the next group. Between groups it builds a context summary
// from completed subtask results and passes it to the next group's executor
// calls (PLAN.md section 17.8).
//
// Plan progress is flushed to the store after every state change so the UI
// can poll for updates and a crash mid-run leaves a coherent record.
func ExecutePlan(ctx context.Context, plan *TaskPlan, projectPath string, executor SubtaskExecutor, st *store.Store) error {
	if executor == nil {
		return errors.New("plan: executor is required")
	}
	if plan == nil {
		return errors.New("plan: nil plan")
	}

	plan.Status = PlanStatusExecuting
	if err := SavePlan(st, plan); err != nil {
		return fmt.Errorf("plan: save before execute: %w", err)
	}

	index := indexSubtasks(plan.Subtasks)
	var completed []PlannedSubtask
	var firstErr error

	for _, group := range plan.ExecutionOrder {
		if ctx.Err() != nil {
			firstErr = ctx.Err()
			break
		}

		contextAppend := buildContextSummary(plan.SharedContext, completed)

		var wg sync.WaitGroup
		results := make([]groupOutcome, len(group))

		for i, subID := range group {
			sub, ok := index[subID]
			if !ok {
				results[i] = groupOutcome{
					id:  subID,
					err: fmt.Errorf("plan: subtask %q not found in plan", subID),
				}
				continue
			}

			sub.task.Status = SubtaskStatusRunning
			plan.Subtasks[sub.idx] = sub.task
			_ = SavePlan(st, plan)

			wg.Add(1)
			go func(idx int, target indexedSubtask) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						logger.L.Error("analysis.plan.subtask.panic",
							"subtask_id", target.task.ID, "panic", r, "stack", string(debug.Stack()))
						results[idx] = groupOutcome{id: target.task.ID, idx: target.idx, err: fmt.Errorf("subtask panic: %v", r)}
					}
				}()
				res, err := executor.Execute(ctx, projectPath, target.task, contextAppend)
				results[idx] = groupOutcome{id: target.task.ID, idx: target.idx, res: res, err: err}
			}(i, sub)
		}
		wg.Wait()

		for _, out := range results {
			if out.idx >= len(plan.Subtasks) {
				continue
			}
			task := &plan.Subtasks[out.idx]
			if out.err != nil {
				task.Status = SubtaskStatusFailed
				task.ResultSummary = out.err.Error()
				if firstErr == nil {
					firstErr = fmt.Errorf("subtask %s: %w", out.id, out.err)
				}
				continue
			}
			task.Status = SubtaskStatusCompleted
			if out.res != nil {
				task.SessionID = out.res.SessionID
				task.ResultSummary = out.res.Summary
				task.FilesChanged = out.res.FilesChanged
				task.CostUSD = out.res.CostUSD
				plan.TotalCostUSD += out.res.CostUSD
				plan.TotalTokens += out.res.InputTokens + out.res.OutputTokens
			}
			completed = append(completed, *task)
		}

		if err := SavePlan(st, plan); err != nil && firstErr == nil {
			firstErr = err
		}
		if firstErr != nil {
			break
		}
	}

	now := time.Now()
	plan.CompletedAt = &now
	if firstErr != nil {
		plan.Status = PlanStatusFailed
	} else {
		plan.Status = PlanStatusCompleted
	}
	if err := SavePlan(st, plan); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// groupOutcome is the per-subtask result collected inside one parallel group.
type groupOutcome struct {
	id  string
	idx int
	res *SubtaskResult
	err error
}

// indexedSubtask carries both the task value and its position in plan.Subtasks
// so workers can write back without re-searching.
type indexedSubtask struct {
	task PlannedSubtask
	idx  int
}

func indexSubtasks(subs []PlannedSubtask) map[string]indexedSubtask {
	out := make(map[string]indexedSubtask, len(subs))
	for i, s := range subs {
		out[s.ID] = indexedSubtask{task: s, idx: i}
	}
	return out
}

// buildContextSummary renders the rolling --append-system-prompt payload that
// later groups receive. Format mirrors the example in PLAN.md section 17.8.
func buildContextSummary(shared string, completed []PlannedSubtask) string {
	if len(completed) == 0 && strings.TrimSpace(shared) == "" {
		return ""
	}

	var b strings.Builder
	if shared = strings.TrimSpace(shared); shared != "" {
		b.WriteString("Shared context for this plan:\n")
		b.WriteString(shared)
		b.WriteString("\n\n")
	}
	if len(completed) > 0 {
		b.WriteString("Previous subtasks completed:\n")
		for _, s := range completed {
			fmt.Fprintf(&b, "- %s (id %s):\n", s.Name, s.ID)
			if s.ResultSummary != "" {
				fmt.Fprintf(&b, "  Summary: %s\n", s.ResultSummary)
			}
			if len(s.FilesChanged) > 0 {
				fmt.Fprintf(&b, "  Files changed: %s\n", strings.Join(s.FilesChanged, ", "))
			}
		}
		b.WriteString("Continue with this state.")
	}
	return b.String()
}

// ---- Store integration ----

// SavePlan upserts a TaskPlan and its subtasks into the SQLite store. On the
// first call it inserts; subsequent calls update existing rows. A nil store
// is treated as a no-op so unit tests can omit persistence.
func SavePlan(st *store.Store, plan *TaskPlan) error {
	if st == nil || plan == nil {
		return nil
	}

	analysisJSON, err := json.Marshal(plan.Analysis)
	if err != nil {
		return fmt.Errorf("plan: marshal analysis: %w", err)
	}

	kind := plan.Kind
	if kind == "" {
		kind = PlanKindAdhoc
	}
	row := &store.TaskPlan{
		ID:           plan.ID,
		Project:      plan.Project,
		OriginalTask: plan.OriginalTask,
		AnalysisJSON: string(analysisJSON),
		Status:       string(plan.Status),
		Kind:         string(kind),
		CreatedAt:    plan.CreatedAt,
		CompletedAt:  plan.CompletedAt,
	}
	if plan.TotalCostUSD != 0 {
		v := plan.TotalCostUSD
		row.TotalCostUSD = &v
	}
	if plan.TotalTokens != 0 {
		v := plan.TotalTokens
		row.TotalTokens = &v
	}

	if plan.ID == 0 {
		if err := st.InsertPlan(row); err != nil {
			return fmt.Errorf("plan: insert: %w", err)
		}
		plan.ID = row.ID
		for i := range plan.Subtasks {
			if err := insertSubtaskRow(st, plan.ID, &plan.Subtasks[i]); err != nil {
				return err
			}
		}
		return nil
	}

	if err := st.UpdatePlan(row); err != nil {
		return fmt.Errorf("plan: update: %w", err)
	}
	existing, err := st.ListSubtasks(plan.ID)
	if err != nil {
		return fmt.Errorf("plan: list subtasks: %w", err)
	}
	byID := make(map[string]*store.PlanSubtask, len(existing))
	for _, sub := range existing {
		byID[sub.SubtaskID] = sub
	}
	for i := range plan.Subtasks {
		s := &plan.Subtasks[i]
		row, ok := byID[s.ID]
		if !ok {
			if err := insertSubtaskRow(st, plan.ID, s); err != nil {
				return err
			}
			continue
		}
		filesJSON, _ := json.Marshal(s.FilesChanged)
		row.Status = string(s.Status)
		row.ResultSummary = s.ResultSummary
		row.FilesChanged = string(filesJSON)
		row.Model = s.Model
		if err := st.UpdateSubtask(row); err != nil {
			return fmt.Errorf("plan: update subtask %s: %w", s.ID, err)
		}
	}
	return nil
}

func insertSubtaskRow(st *store.Store, planID int64, s *PlannedSubtask) error {
	depsJSON, _ := json.Marshal(s.DependsOn)
	filesJSON, _ := json.Marshal(s.FilesChanged)
	status := s.Status
	if status == "" {
		status = SubtaskStatusPending
	}
	row := &store.PlanSubtask{
		PlanID:        planID,
		SubtaskID:     s.ID,
		Name:          s.Name,
		Prompt:        s.Prompt,
		DependsOn:     string(depsJSON),
		Model:         s.Model,
		Status:        string(status),
		ResultSummary: s.ResultSummary,
		FilesChanged:  string(filesJSON),
	}
	if err := st.InsertSubtask(row); err != nil {
		return fmt.Errorf("plan: insert subtask %s: %w", s.ID, err)
	}
	return nil
}

// LoadPlan reconstructs a TaskPlan (with subtasks) from the store by ID.
// Returns (nil, nil) if no plan with that ID exists.
func LoadPlan(st *store.Store, id int64) (*TaskPlan, error) {
	if st == nil {
		return nil, errors.New("plan: nil store")
	}
	row, err := st.GetPlan(id)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, nil
	}

	var analysis AnalysisResult
	if row.AnalysisJSON != "" {
		if err := json.Unmarshal([]byte(row.AnalysisJSON), &analysis); err != nil {
			return nil, fmt.Errorf("plan: decode analysis: %w", err)
		}
	}

	subRows, err := st.ListSubtasks(row.ID)
	if err != nil {
		return nil, err
	}
	subs := make([]PlannedSubtask, 0, len(subRows))
	for _, sr := range subRows {
		s := PlannedSubtask{
			ID:            sr.SubtaskID,
			Name:          sr.Name,
			Prompt:        sr.Prompt,
			Model:         sr.Model,
			Status:        SubtaskStatus(sr.Status),
			ResultSummary: sr.ResultSummary,
		}
		if sr.DependsOn != "" {
			_ = json.Unmarshal([]byte(sr.DependsOn), &s.DependsOn)
		}
		if sr.FilesChanged != "" {
			_ = json.Unmarshal([]byte(sr.FilesChanged), &s.FilesChanged)
		}
		subs = append(subs, s)
	}

	kind := PlanKind(row.Kind)
	if kind == "" {
		kind = PlanKindAdhoc
	}
	plan := &TaskPlan{
		ID:             row.ID,
		Project:        row.Project,
		OriginalTask:   row.OriginalTask,
		Analysis:       analysis,
		Subtasks:       subs,
		ExecutionOrder: analysis.ExecutionOrder,
		Status:         PlanStatus(row.Status),
		Kind:           kind,
		SharedContext:  analysis.SharedContext,
		CreatedAt:      row.CreatedAt,
		CompletedAt:    row.CompletedAt,
	}
	if row.TotalCostUSD != nil {
		plan.TotalCostUSD = *row.TotalCostUSD
	}
	if row.TotalTokens != nil {
		plan.TotalTokens = *row.TotalTokens
	}
	return plan, nil
}
