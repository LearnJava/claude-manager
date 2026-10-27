package experience

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/logger"
	"claude-manager/internal/store"
	"claude-manager/internal/worker"
)

// Benchmark runs (WASTE-TASKS.md WS-12). What surrounds the model — settings,
// bans, how strict the check is — moves a result more than swapping the model,
// yet only models get measured. A run is a suite of tasks (reference cases of
// WS-06, or an external set) × configurations × repeats, every cell replayed in
// a throwaway worktree exactly like ReplayReferenceCase. The run keeps a frozen
// copy of its inputs and the report keeps every metric apart: there is no
// single score, because the weights are the reader's choice.

const (
	// DefaultBenchRepeats: repeats are what separates a real difference from
	// noise, so a run does not default to one. Three is the least that gives a
	// range.
	DefaultBenchRepeats = 3
	// DefaultBenchMaxTrials caps tasks × configs × repeats: every trial is a
	// paid model run, so a typo in a list must not start a thousand.
	DefaultBenchMaxTrials = 60
)

// Verdicts of one metric of a configuration against the baseline (the first).
const (
	// BenchIndistinguishable: fewer than two repeats on a side — the spread is
	// unknown, so no difference can be claimed.
	BenchIndistinguishable = "indistinguishable"
	// BenchWithinNoise: the per-repeat ranges overlap.
	BenchWithinNoise = "within_noise"
	// BenchDiffers: the per-repeat ranges do not touch.
	BenchDiffers = "differs"
)

// BenchmarkOptions describes one run.
type BenchmarkOptions struct {
	Project     string
	ProjectPath string
	Name        string
	Suite       []store.BenchTask
	Configs     []store.BenchConfig
	Repeats     int // 0 = DefaultBenchRepeats
	// Rounds is the rework budget of a configuration with gates on;
	// 0 = DefaultReplayRounds.
	Rounds    int
	MaxTrials int // 0 = DefaultBenchMaxTrials
	// HookRules are the project's active hook rules, installed into the replay
	// folder of every configuration with bans on.
	HookRules []store.HookRule
	Exec      analysis.SubtaskExecutor
	// RunGates defaults to worker.RunGates.
	RunGates func(ctx context.Context, root string, gates []string) worker.GateResult
}

// BenchmarkSpec is what a caller (the GUI, an MCP tool) asks for. The suite is
// Tasks when given, else the reference cases CaseIDs, else every usable
// reference case of the project.
type BenchmarkSpec struct {
	Name    string              `json:"name"`
	CaseIDs []int64             `json:"case_ids"`
	Tasks   []store.BenchTask   `json:"tasks"`
	Configs []store.BenchConfig `json:"configs"`
	Repeats int                 `json:"repeats"`
	Rounds  int                 `json:"rounds"`
}

// SuiteFromReferenceCases freezes reference cases into suite tasks. With no ids
// it takes every usable case of the project; an explicit id that is missing,
// from another project or no longer replayable is an error.
func SuiteFromReferenceCases(st *store.Store, project string, caseIDs []int64) ([]store.BenchTask, error) {
	if st == nil {
		return nil, errors.New("no store")
	}
	var cases []*store.ReferenceCase
	if len(caseIDs) == 0 {
		all, err := st.ListReferenceCases(project)
		if err != nil {
			return nil, err
		}
		for _, c := range all {
			if c.InvalidReason == "" {
				cases = append(cases, c)
			}
		}
	}
	for _, id := range caseIDs {
		c, err := st.GetReferenceCase(id)
		if err != nil {
			return nil, err
		}
		if c == nil || c.Project != project {
			return nil, fmt.Errorf("reference case %d not found in project %q", id, project)
		}
		if c.InvalidReason != "" {
			return nil, fmt.Errorf("reference case %d is not usable: %s", id, c.InvalidReason)
		}
		cases = append(cases, c)
	}
	out := make([]store.BenchTask, 0, len(cases))
	for _, c := range cases {
		out = append(out, store.BenchTask{
			ID: fmt.Sprintf("case-%d", c.ID), CaseID: c.ID, Text: c.TaskText,
			BaseCommit: c.BaseCommit, Gates: append([]string(nil), c.GateCmds...),
		})
	}
	return out, nil
}

// BenchConfigName is the name a configuration gets when it has none: what
// differs from the project as it is.
func BenchConfigName(c store.BenchConfig) string {
	parts := []string{}
	model := c.Model
	if model == "" {
		model = "default"
	}
	parts = append(parts, model)
	if c.Effort != "" {
		parts[0] += "/" + c.Effort
	}
	if c.NoSkills {
		parts = append(parts, "no-skills")
	}
	if c.NoBans {
		parts = append(parts, "no-bans")
	}
	if c.NoGates {
		parts = append(parts, "no-gates")
	}
	return strings.Join(parts, " ")
}

// PrepareBenchmark validates a run, freezes its inputs and stores it as
// running. Nothing is spent before this returns: a bad suite, an unknown
// commit or too many trials is an error here, not halfway through.
func PrepareBenchmark(ctx context.Context, st *store.Store, o BenchmarkOptions) (*store.BenchmarkRun, error) {
	if st == nil || o.Exec == nil {
		return nil, errors.New("a benchmark needs a store and an executor")
	}
	if len(o.Suite) == 0 {
		return nil, errors.New("the suite is empty: record reference cases or pass tasks")
	}
	if len(o.Configs) == 0 {
		return nil, errors.New("no configurations to compare")
	}
	repeats := o.Repeats
	if repeats <= 0 {
		repeats = DefaultBenchRepeats
	}
	rounds := o.Rounds
	if rounds <= 0 {
		rounds = DefaultReplayRounds
	}
	maxTrials := o.MaxTrials
	if maxTrials <= 0 {
		maxTrials = DefaultBenchMaxTrials
	}
	if n := len(o.Suite) * len(o.Configs) * repeats; n > maxTrials {
		return nil, fmt.Errorf("%d tasks x %d configurations x %d repeats = %d trials, more than the limit of %d",
			len(o.Suite), len(o.Configs), repeats, n, maxTrials)
	}

	// Freeze: the run owns copies, so the caller's slices (or the reference
	// cases they came from) can change afterwards without touching it.
	suite := make([]store.BenchTask, len(o.Suite))
	seenTask := map[string]bool{}
	for i, t := range o.Suite {
		t.Gates = append([]string(nil), t.Gates...)
		if t.ID == "" {
			t.ID = fmt.Sprintf("task-%d", i+1)
		}
		switch {
		case seenTask[t.ID]:
			return nil, fmt.Errorf("task id %q appears twice", t.ID)
		case strings.TrimSpace(t.Text) == "":
			return nil, fmt.Errorf("task %s has no text", t.ID)
		case t.BaseCommit == "":
			return nil, fmt.Errorf("task %s has no base commit", t.ID)
		case len(t.Gates) == 0:
			return nil, fmt.Errorf("task %s has no gates to check a result with", t.ID)
		}
		seenTask[t.ID] = true
		suite[i] = t
	}
	configs := make([]store.BenchConfig, len(o.Configs))
	seenCfg := map[string]bool{}
	for i, c := range o.Configs {
		if c.Name == "" {
			c.Name = BenchConfigName(c)
		}
		if seenCfg[c.Name] {
			return nil, fmt.Errorf("configuration name %q appears twice", c.Name)
		}
		seenCfg[c.Name] = true
		configs[i] = c
	}

	var gone []string
	for _, t := range suite {
		exists, err := commitExists(ctx, o.ProjectPath, t.BaseCommit)
		if err != nil {
			return nil, fmt.Errorf("check base commit of %s: %w", t.ID, err)
		}
		if !exists {
			gone = append(gone, t.ID)
		}
	}
	if len(gone) > 0 {
		return nil, fmt.Errorf("base commit is gone from the repository for: %s", strings.Join(gone, ", "))
	}

	run := &store.BenchmarkRun{
		Project: o.Project, Name: o.Name, Suite: suite, Configs: configs,
		Repeats: repeats, Rounds: rounds, Status: store.BenchmarkRunning,
	}
	if err := st.InsertBenchmarkRun(run); err != nil {
		return nil, err
	}
	return run, nil
}

// ExecuteBenchmark runs every trial of a prepared run — repeat by repeat, so an
// interruption leaves whole repeats rather than one configuration done and
// another not started — storing each as it finishes. It reads the run's frozen
// copy, never the options' suite. A cancelled context ends the run as
// interrupted with the trials so far kept.
func ExecuteBenchmark(ctx context.Context, st *store.Store, run *store.BenchmarkRun, o BenchmarkOptions) error {
	if st == nil || run == nil || o.Exec == nil {
		return errors.New("a benchmark needs a store, a run and an executor")
	}
	finish := func(status, note string) {
		if err := st.FinishBenchmarkRun(run.ID, status, note); err != nil {
			logger.L.Error("experience.benchmark_finish_failed", "run", run.ID, "error", err)
		}
	}
	errored := 0
	for rep := 1; rep <= run.Repeats; rep++ {
		for ti, task := range run.Suite {
			for ci, cfg := range run.Configs {
				if ctx.Err() != nil {
					finish(store.BenchmarkInterrupted, "cancelled")
					return ctx.Err()
				}
				tr := runBenchTrial(ctx, run, task, cfg, o)
				tr.RunID, tr.Task, tr.Config, tr.Repeat = run.ID, ti, ci, rep
				if ctx.Err() != nil {
					// A trial cut short by cancellation says nothing; drop it.
					finish(store.BenchmarkInterrupted, "cancelled")
					return ctx.Err()
				}
				if tr.Errored {
					errored++
				}
				if err := st.InsertBenchmarkTrial(tr); err != nil {
					finish(store.BenchmarkInterrupted, "could not store a trial: "+err.Error())
					return err
				}
			}
		}
	}
	note := ""
	if errored > 0 {
		note = fmt.Sprintf("%d trial(s) could not be run and are left out of the figures", errored)
	}
	finish(store.BenchmarkDone, note)
	return nil
}

// RunBenchmark prepares and executes a run, blocking until it ends.
func RunBenchmark(ctx context.Context, st *store.Store, o BenchmarkOptions) (*store.BenchmarkRun, error) {
	run, err := PrepareBenchmark(ctx, st, o)
	if err != nil {
		return nil, err
	}
	return run, ExecuteBenchmark(ctx, st, run, o)
}

func runBenchTrial(ctx context.Context, run *store.BenchmarkRun, task store.BenchTask, cfg store.BenchConfig, o BenchmarkOptions) *store.BenchmarkTrial {
	tr := &store.BenchmarkTrial{}
	maxRounds := run.Rounds
	if cfg.NoGates {
		maxRounds = 0
	}
	err := withDetachedWorktree(ctx, o.ProjectPath, task.BaseCommit, func(dir string) error {
		if err := prepareBenchFolder(ctx, dir, cfg, o.HookRules); err != nil {
			return fmt.Errorf("prepare the folder: %w", err)
		}
		out, err := runInFolder(ctx, dir, folderRun{
			Name:   fmt.Sprintf("bench-%d-%s", run.ID, task.ID),
			PinTag: fmt.Sprintf("bench-%d", run.ID), TaskText: task.Text, Gates: task.Gates,
			Model: cfg.Model, Effort: cfg.Effort, MaxRounds: maxRounds, Exec: o.Exec, RunGates: o.RunGates,
		})
		if err != nil {
			return err
		}
		tr.Passed, tr.Rounds, tr.Tokens, tr.DurationSec = out.Passed, out.Rounds, out.Tokens, out.DurationSec
		tr.ResultCommit, tr.Note = out.ResultCommit, out.Note
		return nil
	})
	if err != nil {
		tr.Errored, tr.Note = true, err.Error()
	}
	return tr
}

// prepareBenchFolder applies the configuration's setup axes to a fresh checkout
// and commits the change into the throwaway worktree's own HEAD, so the pinned
// result later shows only what the agent did.
func prepareBenchFolder(ctx context.Context, dir string, cfg store.BenchConfig, rules []store.HookRule) error {
	if cfg.NoSkills {
		if err := os.RemoveAll(filepath.Join(dir, ".claude", "skills")); err != nil {
			return err
		}
	}
	var plan HookPlan
	var err error
	switch {
	case cfg.NoBans:
		plan, err = PlanHook(dir, nil)
	case len(rules) > 0:
		plan, err = PlanHook(dir, rules)
	}
	if err != nil {
		return err
	}
	if err := ApplyHookPlan(dir, plan); err != nil {
		return err
	}
	return commitPreparation(ctx, dir)
}

// commitPreparation moves the detached HEAD of dir onto a commit holding the
// prepared tree (plumbing only: no hooks, no config, no branch). Nothing
// prepared = nothing done.
func commitPreparation(ctx context.Context, dir string) error {
	if _, err := gitRun(ctx, dir, nil, "add", "-A"); err != nil {
		return err
	}
	tree, err := gitRun(ctx, dir, nil, "write-tree")
	if err != nil {
		return err
	}
	headTree, err := gitRun(ctx, dir, nil, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}
	if tree == headTree {
		return nil
	}
	head, err := gitRun(ctx, dir, nil, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	sha, err := gitRun(ctx, dir, []string{
		"GIT_AUTHOR_NAME=cm-replay", "GIT_AUTHOR_EMAIL=cm-replay@localhost",
		"GIT_COMMITTER_NAME=cm-replay", "GIT_COMMITTER_EMAIL=cm-replay@localhost",
	}, "commit-tree", tree, "-p", head, "-m", "benchmark setup")
	if err != nil {
		return err
	}
	_, err = gitRun(ctx, dir, nil, "update-ref", "HEAD", sha)
	return err
}

// ---- report ----

// BenchStat describes one metric over a set of values. N is how many values
// there were; the rest is zero when N is 0.
type BenchStat struct {
	N      int
	Mean   float64
	Min    float64
	Max    float64
	StdDev float64 // sample standard deviation, 0 for N < 2
}

func newBenchStat(v []float64) BenchStat {
	s := BenchStat{N: len(v)}
	if len(v) == 0 {
		return s
	}
	s.Min, s.Max = v[0], v[0]
	sum := 0.0
	for _, x := range v {
		sum += x
		s.Min = math.Min(s.Min, x)
		s.Max = math.Max(s.Max, x)
	}
	s.Mean = sum / float64(len(v))
	if len(v) > 1 {
		ss := 0.0
		for _, x := range v {
			ss += (x - s.Mean) * (x - s.Mean)
		}
		s.StdDev = math.Sqrt(ss / float64(len(v)-1))
	}
	return s
}

// BenchRepeat is one repeat of a configuration over the whole suite — the unit
// the spread is measured on. The means are 0 when the repeat had no value (for
// rounds: no trial went green).
type BenchRepeat struct {
	Repeat          int
	Trials          int
	Passed          int
	PassRate        float64
	MeanRounds      float64
	MeanTokens      float64
	MeanDurationSec float64
}

// BenchConfigReport is one configuration's figures over every completed trial.
// Rounds is over the trials that went green (rounds to green); Tokens and
// DurationSec over all completed ones. Errors are trials the model could not be
// run for: counted here and in no figure.
type BenchConfigReport struct {
	Config      store.BenchConfig
	Trials      int
	Errors      int
	Passed      int
	PassRate    float64
	Rounds      BenchStat
	Tokens      BenchStat
	DurationSec BenchStat
	Repeats     []BenchRepeat
}

// BenchComparison is one metric of a configuration against the baseline (the
// first configuration). Verdict is one of the Bench* constants; Direction says
// "higher" or "lower" than the baseline when they differ.
type BenchComparison struct {
	Config    string
	Baseline  string
	Metric    string
	Verdict   string
	Direction string
	Note      string
}

// BenchTaskView is a suite task as the report shows it (without the text).
type BenchTaskView struct {
	ID         string
	CaseID     int64
	BaseCommit string
}

// BenchTrialView is one stored cell.
type BenchTrialView struct {
	Task         string
	Config       string
	Repeat       int
	Passed       bool
	Errored      bool
	Rounds       int
	Tokens       int64
	DurationSec  int
	ResultCommit string
	Note         string
}

// BenchmarkReport compares a run's configurations metric by metric. It carries
// no overall score on purpose: how much a round, a token or a failed gate
// weighs depends on the task and is the reader's call.
type BenchmarkReport struct {
	RunID         int64
	Project       string
	Name          string
	Status        string
	Note          string
	CreatedAt     time.Time
	FinishedAt    time.Time
	Repeats       int
	Rounds        int
	PlannedTrials int
	StoredTrials  int
	Tasks         []BenchTaskView
	Configs       []BenchConfigReport
	Baseline      string
	Comparisons   []BenchComparison
	Trials        []BenchTrialView
	Warnings      []string
}

// Benchmark metrics compared between configurations.
const (
	MetricPassRate    = "pass_rate"
	MetricRounds      = "rounds"
	MetricTokens      = "tokens"
	MetricDurationSec = "duration_sec"
)

// BuildBenchmarkReport reports one run of a project from its stored, frozen
// inputs and trials only — never from the live reference cases. runID <= 0 is
// the project's latest run; a project with none gets an empty report.
func BuildBenchmarkReport(st *store.Store, project string, runID int64) (BenchmarkReport, error) {
	empty := BenchmarkReport{Project: project, Tasks: []BenchTaskView{}, Configs: []BenchConfigReport{},
		Comparisons: []BenchComparison{}, Trials: []BenchTrialView{}, Warnings: []string{}}
	if st == nil {
		return empty, errors.New("no store")
	}
	var run *store.BenchmarkRun
	if runID > 0 {
		r, err := st.GetBenchmarkRun(runID)
		if err != nil {
			return empty, err
		}
		if r == nil || r.Project != project {
			return empty, fmt.Errorf("benchmark run %d not found in project %q", runID, project)
		}
		run = r
	} else {
		runs, err := st.ListBenchmarkRuns(project)
		if err != nil || len(runs) == 0 {
			return empty, err
		}
		run = runs[0]
	}
	trials, err := st.ListBenchmarkTrials(run.ID)
	if err != nil {
		return empty, err
	}

	rep := empty
	rep.RunID, rep.Name, rep.Status, rep.Note = run.ID, run.Name, run.Status, run.Note
	rep.CreatedAt, rep.FinishedAt = run.CreatedAt, run.FinishedAt
	rep.Repeats, rep.Rounds = run.Repeats, run.Rounds
	rep.PlannedTrials = len(run.Suite) * len(run.Configs) * run.Repeats
	rep.StoredTrials = len(trials)
	for _, t := range run.Suite {
		rep.Tasks = append(rep.Tasks, BenchTaskView{ID: t.ID, CaseID: t.CaseID, BaseCommit: t.BaseCommit})
	}
	for _, t := range trials {
		if t.Task < 0 || t.Task >= len(run.Suite) || t.Config < 0 || t.Config >= len(run.Configs) {
			continue
		}
		rep.Trials = append(rep.Trials, BenchTrialView{
			Task: run.Suite[t.Task].ID, Config: run.Configs[t.Config].Name, Repeat: t.Repeat,
			Passed: t.Passed, Errored: t.Errored, Rounds: t.Rounds, Tokens: t.Tokens,
			DurationSec: t.DurationSec, ResultCommit: t.ResultCommit, Note: t.Note,
		})
	}
	for ci, cfg := range run.Configs {
		rep.Configs = append(rep.Configs, buildConfigReport(cfg, ci, run.Repeats, trials))
	}
	if len(rep.Configs) > 0 {
		rep.Baseline = rep.Configs[0].Config.Name
		for _, c := range rep.Configs[1:] {
			rep.Comparisons = append(rep.Comparisons, compareConfigs(rep.Configs[0], c)...)
		}
	}
	if run.Repeats < 2 {
		rep.Warnings = append(rep.Warnings, "one repeat per cell: there is no spread, so no difference between configurations can be told from noise")
	}
	if run.Status != store.BenchmarkDone {
		rep.Warnings = append(rep.Warnings, fmt.Sprintf("the run is %s with %d of %d trials stored", run.Status, len(trials), rep.PlannedTrials))
	}
	return rep, nil
}

// repeatAcc collects one repeat's values of one configuration.
type repeatAcc struct {
	trials, passed      int
	rounds, tokens, sec []float64
}

func buildConfigReport(cfg store.BenchConfig, idx, repeats int, trials []*store.BenchmarkTrial) BenchConfigReport {
	out := BenchConfigReport{Config: cfg, Repeats: []BenchRepeat{}}
	var rounds, tokens, secs []float64
	per := make([]repeatAcc, repeats+1)
	for _, t := range trials {
		if t.Config != idx {
			continue
		}
		if t.Errored {
			out.Errors++
			continue
		}
		out.Trials++
		tokens = append(tokens, float64(t.Tokens))
		secs = append(secs, float64(t.DurationSec))
		var p *repeatAcc
		if t.Repeat >= 1 && t.Repeat <= repeats {
			p = &per[t.Repeat]
			p.trials++
			p.tokens = append(p.tokens, float64(t.Tokens))
			p.sec = append(p.sec, float64(t.DurationSec))
		}
		if t.Passed {
			out.Passed++
			rounds = append(rounds, float64(t.Rounds))
			if p != nil {
				p.passed++
				p.rounds = append(p.rounds, float64(t.Rounds))
			}
		}
	}
	if out.Trials > 0 {
		out.PassRate = float64(out.Passed) / float64(out.Trials)
	}
	out.Rounds, out.Tokens, out.DurationSec = newBenchStat(rounds), newBenchStat(tokens), newBenchStat(secs)
	for r := 1; r <= repeats; r++ {
		p := per[r]
		if p.trials == 0 {
			continue
		}
		out.Repeats = append(out.Repeats, BenchRepeat{
			Repeat: r, Trials: p.trials, Passed: p.passed, PassRate: float64(p.passed) / float64(p.trials),
			MeanRounds: newBenchStat(p.rounds).Mean, MeanTokens: newBenchStat(p.tokens).Mean,
			MeanDurationSec: newBenchStat(p.sec).Mean,
		})
	}
	return out
}

// compareConfigs sets each metric of c against the baseline b. The spread is
// the range of the per-repeat values: two ranges that do not touch differ, ones
// that overlap are within noise, and with fewer than two repeats on either side
// there is no range at all.
func compareConfigs(b, c BenchConfigReport) []BenchComparison {
	type metric struct {
		name string
		get  func(BenchRepeat) float64
		ok   func(BenchRepeat) bool
	}
	all := func(BenchRepeat) bool { return true }
	metrics := []metric{
		{MetricPassRate, func(r BenchRepeat) float64 { return r.PassRate }, all},
		{MetricRounds, func(r BenchRepeat) float64 { return r.MeanRounds }, func(r BenchRepeat) bool { return r.Passed > 0 }},
		{MetricTokens, func(r BenchRepeat) float64 { return r.MeanTokens }, all},
		{MetricDurationSec, func(r BenchRepeat) float64 { return r.MeanDurationSec }, all},
	}
	var out []BenchComparison
	for _, m := range metrics {
		cmp := BenchComparison{Config: c.Config.Name, Baseline: b.Config.Name, Metric: m.name}
		bv, cv := repeatValues(b.Repeats, m.get, m.ok), repeatValues(c.Repeats, m.get, m.ok)
		switch {
		case len(bv) < 2 || len(cv) < 2:
			cmp.Verdict = BenchIndistinguishable
			cmp.Note = fmt.Sprintf("fewer than two repeats with a value (%d vs %d): no spread to compare against", len(bv), len(cv))
		default:
			bs, cs := newBenchStat(bv), newBenchStat(cv)
			switch {
			case cs.Min > bs.Max:
				cmp.Verdict, cmp.Direction = BenchDiffers, "higher"
			case cs.Max < bs.Min:
				cmp.Verdict, cmp.Direction = BenchDiffers, "lower"
			default:
				cmp.Verdict = BenchWithinNoise
			}
			cmp.Note = fmt.Sprintf("per-repeat range %.4g..%.4g vs baseline %.4g..%.4g", cs.Min, cs.Max, bs.Min, bs.Max)
		}
		out = append(out, cmp)
	}
	return out
}

func repeatValues(rs []BenchRepeat, get func(BenchRepeat) float64, ok func(BenchRepeat) bool) []float64 {
	var v []float64
	for _, r := range rs {
		if ok(r) {
			v = append(v, get(r))
		}
	}
	return v
}
