package experience

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"claude-manager/internal/analysis"
	"claude-manager/internal/store"
)

// benchExec is an attempt runner for benchmarks. Per attempt it writes the
// "ok.txt" the stub gates look for when pass(model) says so, reports
// tokens(call number) and records what the folder looked like.
type benchExec struct {
	mu     sync.Mutex
	pass   func(model string) bool
	tokens func(call int) int64
	fail   bool

	calls  int
	models []string
	seen   []folderView
}

type folderView struct {
	skills, hook bool
}

func (e *benchExec) Execute(_ context.Context, dir string, sub analysis.PlannedSubtask, _ string) (*analysis.SubtaskResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fail {
		return nil, errors.New("claude not found")
	}
	n := e.calls
	e.calls++
	e.models = append(e.models, sub.Model)
	_, skillsErr := os.Stat(filepath.Join(dir, ".claude", "skills"))
	_, hookErr := os.Stat(filepath.Join(dir, filepath.FromSlash(HookScriptRel)))
	e.seen = append(e.seen, folderView{skills: skillsErr == nil, hook: hookErr == nil})
	if e.pass != nil && e.pass(sub.Model) {
		_ = os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("x"), 0o644)
	}
	tok := int64(100)
	if e.tokens != nil {
		tok = e.tokens(n)
	}
	return &analysis.SubtaskResult{InputTokens: tok}, nil
}

func benchTask(base string) store.BenchTask {
	return store.BenchTask{ID: "t1", Text: "## T-1: do it", BaseCommit: base, Gates: []string{"go test ./..."}}
}

func benchOpts(dir string, ex *benchExec, base string, cfgs ...store.BenchConfig) BenchmarkOptions {
	return BenchmarkOptions{Project: "p", ProjectPath: dir, Name: "b", Suite: []store.BenchTask{benchTask(base)},
		Configs: cfgs, Exec: ex, RunGates: stubGates}
}

func cfgByName(rep BenchmarkReport, name string) BenchConfigReport {
	for _, c := range rep.Configs {
		if c.Config.Name == name {
			return c
		}
	}
	return BenchConfigReport{}
}

func verdicts(rep BenchmarkReport) map[string]string {
	m := map[string]string{}
	for _, c := range rep.Comparisons {
		m[c.Config+"/"+c.Metric] = c.Verdict
	}
	return m
}

func TestBenchmark_TwoConfigsGiveComparableReportWithSpread(t *testing.T) {
	dir, base, _ := refRepo(t)
	st := newRefStore(t)
	headsBefore := testGit(t, dir, "for-each-ref", "refs/heads")

	ex := &benchExec{pass: func(model string) bool { return model == "good" }}
	run, err := RunBenchmark(context.Background(), st, func() BenchmarkOptions {
		o := benchOpts(dir, ex, base, store.BenchConfig{Name: "bad", Model: "bad"}, store.BenchConfig{Name: "good", Model: "good"})
		o.Repeats = 3
		return o
	}())
	if err != nil {
		t.Fatal(err)
	}
	rep, err := BuildBenchmarkReport(st, "p", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != store.BenchmarkDone || rep.StoredTrials != 6 || rep.PlannedTrials != 6 || len(rep.Warnings) != 0 {
		t.Fatalf("report = %+v", rep)
	}
	bad, good := cfgByName(rep, "bad"), cfgByName(rep, "good")
	if bad.Trials != 3 || bad.Passed != 0 || bad.PassRate != 0 || bad.Rounds.N != 0 {
		t.Errorf("bad = %+v", bad)
	}
	// Never green: the first attempt plus DefaultReplayRounds reworks, 100 each.
	if bad.Tokens.Mean != 300 || bad.Tokens.StdDev != 0 {
		t.Errorf("bad tokens = %+v", bad.Tokens)
	}
	if good.Passed != 3 || good.PassRate != 1 || good.Rounds.N != 3 || good.Rounds.Mean != 0 || good.Tokens.Mean != 100 {
		t.Errorf("good = %+v", good)
	}
	if len(good.Repeats) != 3 || good.Repeats[2].Repeat != 3 || good.Repeats[0].Trials != 1 {
		t.Errorf("the spread needs one entry per repeat: %+v", good.Repeats)
	}
	if rep.Baseline != "bad" {
		t.Errorf("baseline = %q, want the first configuration", rep.Baseline)
	}
	v := verdicts(rep)
	if v["good/pass_rate"] != BenchDiffers || v["good/tokens"] != BenchDiffers {
		t.Errorf("verdicts = %v", v)
	}
	for _, c := range rep.Comparisons {
		if c.Metric == MetricPassRate && c.Direction != "higher" {
			t.Errorf("good passes more often than bad, got %+v", c)
		}
	}
	// Four metrics kept apart; no overall score anywhere in the report.
	if len(rep.Comparisons) != 4 {
		t.Errorf("comparisons = %d, want one per metric", len(rep.Comparisons))
	}
	if _, ok := reflect.TypeOf(rep).FieldByName("Score"); ok {
		t.Error("the report must not carry a single score")
	}
	if got := testGit(t, dir, "for-each-ref", "refs/heads"); got != headsBefore {
		t.Errorf("project branches changed:\n%s\n->\n%s", headsBefore, got)
	}
	if wt := testGit(t, dir, "worktree", "list", "--porcelain"); strings.Count(wt, "worktree ") != 1 {
		t.Errorf("a benchmark worktree was left behind:\n%s", wt)
	}
}

func TestBenchmark_OneRepeatIsIndistinguishable(t *testing.T) {
	dir, base, _ := refRepo(t)
	st := newRefStore(t)
	ex := &benchExec{pass: func(model string) bool { return model == "good" }}
	o := benchOpts(dir, ex, base, store.BenchConfig{Name: "bad", Model: "bad"}, store.BenchConfig{Name: "good", Model: "good"})
	o.Repeats = 1
	run, err := RunBenchmark(context.Background(), st, o)
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := BuildBenchmarkReport(st, "p", run.ID)
	if len(rep.Comparisons) != 4 {
		t.Fatalf("comparisons = %+v", rep.Comparisons)
	}
	for _, c := range rep.Comparisons {
		if c.Verdict != BenchIndistinguishable {
			t.Errorf("with one repeat %s must be marked indistinguishable, got %q", c.Metric, c.Verdict)
		}
	}
	if len(rep.Warnings) == 0 || !strings.Contains(rep.Warnings[0], "one repeat") {
		t.Errorf("warnings = %v", rep.Warnings)
	}
	// The figures themselves are still reported.
	if cfgByName(rep, "good").PassRate != 1 || cfgByName(rep, "bad").PassRate != 0 {
		t.Errorf("figures missing: %+v", rep.Configs)
	}
}

func TestBenchmark_DefaultRepeatsAreMoreThanOne(t *testing.T) {
	dir, base, _ := refRepo(t)
	st := newRefStore(t)
	run, err := PrepareBenchmark(context.Background(), st, benchOpts(dir, &benchExec{}, base, store.BenchConfig{Name: "a"}))
	if err != nil {
		t.Fatal(err)
	}
	if run.Repeats != DefaultBenchRepeats || run.Repeats < 2 {
		t.Errorf("repeats default = %d", run.Repeats)
	}
}

func TestBenchmark_EditingTheSuiteDoesNotChangeAPastRun(t *testing.T) {
	dir, base, result := refRepo(t)
	st := newRefStore(t)
	c := storeCase(t, st, base, result)

	suite, err := SuiteFromReferenceCases(st, "p", nil)
	if err != nil || len(suite) != 1 || suite[0].CaseID != c.ID {
		t.Fatalf("suite = %+v %v", suite, err)
	}
	ex := &benchExec{pass: func(string) bool { return true }}
	o := BenchmarkOptions{Project: "p", ProjectPath: dir, Suite: suite, Repeats: 2, Exec: ex, RunGates: stubGates,
		Configs: []store.BenchConfig{{Name: "a", Model: "x"}, {Name: "b", Model: "y"}}}
	run, err := RunBenchmark(context.Background(), st, o)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := BuildBenchmarkReport(st, "p", run.ID)

	// Edit everything the run came from: the caller's slice, the case, the table.
	suite[0].Text, suite[0].BaseCommit, suite[0].Gates[0] = "changed", "deadbeef", "false"
	if err := st.SetReferenceCaseInvalid(c.ID, "edited"); err != nil {
		t.Fatal(err)
	}
	after, _ := BuildBenchmarkReport(st, "p", run.ID)
	if !reflect.DeepEqual(before, after) {
		t.Errorf("a past run changed after its suite was edited:\n%+v\n%+v", before, after)
	}
	got, _ := st.GetBenchmarkRun(run.ID)
	if got.Suite[0].Text != "## T-1: x" || got.Suite[0].BaseCommit != base || got.Suite[0].Gates[0] != "go test ./..." {
		t.Errorf("stored suite is not the frozen one: %+v", got.Suite[0])
	}
	if latest, _ := BuildBenchmarkReport(st, "p", 0); latest.RunID != run.ID {
		t.Errorf("run 0 must mean the latest run, got %d", latest.RunID)
	}
}

func TestBenchmark_AnIdenticalConfigurationStaysWithinNoise(t *testing.T) {
	dir, base, _ := refRepo(t)
	st := newRefStore(t)
	// Tokens wander from call to call; the two configurations are the same.
	ex := &benchExec{pass: func(string) bool { return true },
		tokens: func(n int) int64 { return int64(100 + (n%3)*10) }}
	o := benchOpts(dir, ex, base, store.BenchConfig{Name: "one", Model: "m", Effort: "high"},
		store.BenchConfig{Name: "same", Model: "m", Effort: "high"})
	o.Repeats = 3
	run, err := RunBenchmark(context.Background(), st, o)
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := BuildBenchmarkReport(st, "p", run.ID)
	for _, c := range rep.Comparisons {
		if c.Verdict != BenchWithinNoise {
			t.Errorf("a configuration identical to the baseline must be within noise: %+v", c)
		}
	}
	a, b := cfgByName(rep, "one"), cfgByName(rep, "same")
	if a.Tokens.StdDev == 0 || b.Tokens.StdDev == 0 {
		t.Errorf("the noise must be visible in the spread: %+v %+v", a.Tokens, b.Tokens)
	}
	if a.PassRate != b.PassRate || a.Rounds.Mean != b.Rounds.Mean {
		t.Errorf("same configuration, different outcome: %+v %+v", a, b)
	}
}

func TestBenchmark_ConfigurationAxesShapeTheFolder(t *testing.T) {
	dir := t.TempDir()
	testGit(t, dir, "init", "-q")
	testGit(t, dir, "checkout", "-q", "-b", "main")
	rules := []store.HookRule{{ID: 1, Kind: HookKindRepeatRead, Status: store.HookRuleKept}}
	skill := filepath.Join(dir, ".claude", "skills", "s", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skill), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skill, []byte("---\nname: s\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := PlanHook(dir, rules)
	if err != nil {
		t.Fatal(err)
	}
	if err := ApplyHookPlan(dir, plan); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "-A")
	testGit(t, dir, "commit", "-q", "-m", "base")
	base := testGit(t, dir, "rev-parse", "HEAD")

	st := newRefStore(t)
	ex := &benchExec{} // never writes ok.txt: the gates stay red
	o := benchOpts(dir, ex, base,
		store.BenchConfig{Name: "all"},
		store.BenchConfig{Name: "noskills", NoSkills: true},
		store.BenchConfig{Name: "nobans", NoBans: true},
		store.BenchConfig{Name: "nogates", NoGates: true},
	)
	o.Repeats, o.HookRules = 1, rules
	run, err := RunBenchmark(context.Background(), st, o)
	if err != nil {
		t.Fatal(err)
	}
	// "all" and "noskills" and "nobans" each take 3 attempts (red gates, two
	// reworks), "nogates" exactly 1. The order is: all x3, noskills x3, nobans x3, nogates.
	if ex.calls != 10 {
		t.Fatalf("attempts = %d, want 3+3+3+1", ex.calls)
	}
	want := []folderView{
		{true, true}, {true, true}, {true, true},
		{false, true}, {false, true}, {false, true},
		{true, false}, {true, false}, {true, false},
		{true, true},
	}
	if !reflect.DeepEqual(ex.seen, want) {
		t.Errorf("folders seen = %+v\nwant %+v", ex.seen, want)
	}
	rep, _ := BuildBenchmarkReport(st, "p", run.ID)
	if c := cfgByName(rep, "nogates"); c.Tokens.Mean != 100 || c.Rounds.N != 0 {
		t.Errorf("without gates there is no rework: %+v", c)
	}
	for _, tr := range rep.Trials {
		// The setup change is committed into the throwaway HEAD, so a run in
		// which the agent did nothing says so instead of pinning the setup.
		if tr.ResultCommit != "" || !strings.Contains(tr.Note, "changed nothing") {
			t.Errorf("trial %+v: the preparation must not count as the agent's work", tr)
		}
	}
}

func TestPrepareBenchmark_RefusesBeforeSpendingAnything(t *testing.T) {
	dir, base, _ := refRepo(t)
	cfg := store.BenchConfig{Name: "a"}
	cases := map[string]func(*BenchmarkOptions){
		"empty suite":   func(o *BenchmarkOptions) { o.Suite = nil },
		"no configs":    func(o *BenchmarkOptions) { o.Configs = nil },
		"no text":       func(o *BenchmarkOptions) { o.Suite[0].Text = " " },
		"no gates":      func(o *BenchmarkOptions) { o.Suite[0].Gates = nil },
		"gone commit":   func(o *BenchmarkOptions) { o.Suite[0].BaseCommit = strings.Repeat("0", 40) },
		"too many":      func(o *BenchmarkOptions) { o.Repeats, o.MaxTrials = 5, 4 },
		"same name":     func(o *BenchmarkOptions) { o.Configs = []store.BenchConfig{cfg, cfg} },
		"duplicate ids": func(o *BenchmarkOptions) { o.Suite = append(o.Suite, o.Suite[0]) },
	}
	for name, mut := range cases {
		st := newRefStore(t)
		ex := &benchExec{}
		o := benchOpts(dir, ex, base, cfg)
		mut(&o)
		if run, err := PrepareBenchmark(context.Background(), st, o); err == nil || run != nil {
			t.Errorf("%s: want an error, got %+v", name, run)
		}
		if runs, _ := st.ListBenchmarkRuns("p"); len(runs) != 0 || ex.calls != 0 {
			t.Errorf("%s: a refused run stored %d run(s) and made %d model call(s)", name, len(runs), ex.calls)
		}
	}
}

func TestBenchmark_UnrunnableTrialsAreCountedApart(t *testing.T) {
	dir, base, _ := refRepo(t)
	st := newRefStore(t)
	o := benchOpts(dir, &benchExec{fail: true}, base, store.BenchConfig{Name: "a"})
	o.Repeats = 2
	run, err := RunBenchmark(context.Background(), st, o)
	if err != nil {
		t.Fatal(err)
	}
	rep, _ := BuildBenchmarkReport(st, "p", run.ID)
	c := rep.Configs[0]
	if rep.Status != store.BenchmarkDone || c.Errors != 2 || c.Trials != 0 || c.PassRate != 0 || c.Tokens.N != 0 {
		t.Errorf("a trial that never ran must not be a failure of the configuration: %+v", c)
	}
	if !strings.Contains(rep.Note, "2 trial") {
		t.Errorf("note = %q", rep.Note)
	}
}

func TestBenchmark_CancelledRunIsInterruptedAndKeepsItsTrials(t *testing.T) {
	dir, base, _ := refRepo(t)
	st := newRefStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	ex := &cancelAfter{benchExec: benchExec{pass: func(string) bool { return true }}, cancel: cancel, after: 2}
	o := benchOpts(dir, &ex.benchExec, base, store.BenchConfig{Name: "a"})
	o.Exec = ex
	o.Repeats = 4
	run, err := RunBenchmark(ctx, st, o)
	if err == nil {
		t.Fatal("a cancelled run must report it")
	}
	rep, _ := BuildBenchmarkReport(st, "p", run.ID)
	if rep.Status != store.BenchmarkInterrupted || rep.StoredTrials != 1 || rep.PlannedTrials != 4 {
		t.Errorf("report = status %s, %d/%d trials", rep.Status, rep.StoredTrials, rep.PlannedTrials)
	}
	if len(rep.Warnings) == 0 {
		t.Error("an unfinished run must say so")
	}
	// A run still marked running when the manager restarts is closed.
	live := &store.BenchmarkRun{Project: "p", Suite: []store.BenchTask{benchTask(base)}, Configs: []store.BenchConfig{{Name: "a"}}, Repeats: 1}
	if err := st.InsertBenchmarkRun(live); err != nil {
		t.Fatal(err)
	}
	if n, err := st.InterruptRunningBenchmarks(); err != nil || n != 1 {
		t.Fatalf("interrupt = %d %v", n, err)
	}
	if got, _ := st.GetBenchmarkRun(live.ID); got.Status != store.BenchmarkInterrupted || got.FinishedAt.IsZero() {
		t.Errorf("run = %+v", got)
	}
}

type cancelAfter struct {
	benchExec
	cancel func()
	after  int
}

func (c *cancelAfter) Execute(ctx context.Context, dir string, sub analysis.PlannedSubtask, ap string) (*analysis.SubtaskResult, error) {
	res, err := c.benchExec.Execute(ctx, dir, sub, ap)
	if c.calls >= c.after {
		c.cancel()
	}
	return res, err
}

func TestBuildBenchmarkReport_EmptyAndForeign(t *testing.T) {
	st := newRefStore(t)
	rep, err := BuildBenchmarkReport(st, "p", 0)
	if err != nil || rep.RunID != 0 || rep.Configs == nil || rep.Comparisons == nil || rep.Trials == nil {
		t.Fatalf("a project without runs wants an empty report, got %+v %v", rep, err)
	}
	run := &store.BenchmarkRun{Project: "p", Repeats: 1}
	if err := st.InsertBenchmarkRun(run); err != nil {
		t.Fatal(err)
	}
	if _, err := BuildBenchmarkReport(st, "other", run.ID); err == nil {
		t.Error("another project's run must not be readable")
	}
}

func TestBenchStat(t *testing.T) {
	s := newBenchStat([]float64{2, 4, 6})
	if s.N != 3 || s.Mean != 4 || s.Min != 2 || s.Max != 6 || s.StdDev != 2 {
		t.Errorf("stat = %+v", s)
	}
	if z := newBenchStat(nil); z.N != 0 || z.Mean != 0 {
		t.Errorf("empty stat = %+v", z)
	}
	if one := newBenchStat([]float64{5}); one.StdDev != 0 {
		t.Errorf("one value has no spread: %+v", one)
	}
}
