package main

import (
	"context"
	"fmt"
	"sync"

	"claude-manager/internal/analysis"
	"claude-manager/internal/experience"
	"claude-manager/internal/logger"
	"claude-manager/internal/store"
)

// benchMu keeps one benchmark running at a time: each trial starts a model and
// checks out a worktree of the same repository.
var benchMu sync.Mutex

// RunBenchmark starts a benchmark of a project (WASTE-TASKS.md WS-12): a suite
// (the given tasks, else the given reference cases, else every usable one) x
// configurations x repeats, each trial a model run in a throwaway worktree at
// the task's starting commit with the task's gates. The suite and
// configurations are validated and frozen first - a bad request fails here,
// before any token is spent - then the trials run in the background; poll
// GetBenchmarkReport. Returns the report of the just-started run (RunID,
// planned trials). One benchmark at a time; refused unless [optimization]
// benchmark is on.
func (a *App) RunBenchmark(project string, spec experience.BenchmarkSpec) (experience.BenchmarkReport, error) {
	if a.store == nil || a.cfg == nil {
		return experience.BenchmarkReport{}, fmt.Errorf("no store")
	}
	if !a.cfg.Optimization.Benchmark {
		return experience.BenchmarkReport{}, fmt.Errorf("benchmark is off: set [optimization] benchmark = true")
	}
	path, err := a.projectPath(project)
	if err != nil {
		return experience.BenchmarkReport{}, err
	}
	if !benchMu.TryLock() {
		return experience.BenchmarkReport{}, fmt.Errorf("a benchmark is already running: wait for it to finish")
	}
	started := false
	defer func() {
		if !started {
			benchMu.Unlock()
		}
	}()

	suite := spec.Tasks
	if len(suite) == 0 {
		if suite, err = experience.SuiteFromReferenceCases(a.store, project, spec.CaseIDs); err != nil {
			return experience.BenchmarkReport{}, err
		}
	}
	var rules []store.HookRule
	if all, err := a.store.ListHookRules(project); err == nil {
		rules = experience.ActiveHookRules(all)
	}
	opts := experience.BenchmarkOptions{
		Project: project, ProjectPath: path, Name: spec.Name, Suite: suite, Configs: spec.Configs,
		Repeats: spec.Repeats, Rounds: spec.Rounds, MaxTrials: a.cfg.Optimization.BenchmarkMaxTrials,
		HookRules: rules, Exec: &analysis.CLIExecutor{ClaudePath: a.cfg.Settings.ClaudePath},
	}
	ctx := a.ctx // cancelled when the app shuts down: the run ends as interrupted
	if ctx == nil {
		ctx = context.Background()
	}
	run, err := experience.PrepareBenchmark(ctx, a.store, opts)
	if err != nil {
		return experience.BenchmarkReport{}, err
	}
	started = true
	go func() {
		defer benchMu.Unlock()
		if err := experience.ExecuteBenchmark(ctx, a.store, run, opts); err != nil {
			logger.L.Error("experience.benchmark_failed", "run", run.ID, "error", err)
		}
	}()
	return experience.BuildBenchmarkReport(a.store, project, run.ID)
}

// GetBenchmarkReport reports a benchmark run of a project metric by metric:
// per configuration the share passed, rounds to green, tokens and seconds with
// their spread over repeats, and each configuration against the first as
// differs / within_noise / indistinguishable (a single repeat has no spread).
// There is no overall score. runID <= 0 is the project's latest run; a project
// with none gets an empty report. Read only; works with the feature off.
func (a *App) GetBenchmarkReport(project string, runID int64) (experience.BenchmarkReport, error) {
	if a.store == nil {
		return experience.BenchmarkReport{}, fmt.Errorf("no store")
	}
	return experience.BuildBenchmarkReport(a.store, project, runID)
}
