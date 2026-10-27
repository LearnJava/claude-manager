package main

import (
	"context"
	"fmt"
	"sync"

	"claude-manager/internal/analysis"
	"claude-manager/internal/experience"
	"claude-manager/internal/store"
)

// replayMu keeps one replay running at a time: each one starts a model and
// checks out a worktree of the same repository.
var replayMu sync.Mutex

// GetReferenceReport lists a project's reference cases (finished tasks kept
// with their gates and cost) with every configuration that has replayed them
// — one task, several models/settings — and marks cases whose starting commit
// is gone from the repository (WASTE-TASKS.md WS-06). Read only apart from
// that mark.
func (a *App) GetReferenceReport(project string) (experience.ReferenceReport, error) {
	if a.store == nil {
		return experience.ReferenceReport{}, fmt.Errorf("no store")
	}
	path, _ := a.projectPath(project) // no folder: the base-commit check is skipped
	return experience.BuildReferenceReport(context.Background(), a.store, project, path)
}

// ReplayReferenceCase runs a reference case again with another model or
// effort: a detached worktree at the case's base commit, the same gates, the
// outcome stored beside the original. It blocks for the whole run and spends
// tokens. No project branch is touched. Empty model/effort = the one-shot
// defaults of the analysis runner.
func (a *App) ReplayReferenceCase(project string, caseID int64, model, effort string) (*store.ReferenceReplay, error) {
	if a.store == nil || a.cfg == nil {
		return nil, fmt.Errorf("no store")
	}
	path, err := a.projectPath(project)
	if err != nil {
		return nil, err
	}
	replayMu.Lock()
	defer replayMu.Unlock()
	return experience.ReplayReferenceCase(context.Background(), a.store, experience.ReplayOptions{
		Project: project, ProjectPath: path, CaseID: caseID, Model: model, Effort: effort,
		Exec: &analysis.CLIExecutor{ClaudePath: a.cfg.Settings.ClaudePath},
	})
}
