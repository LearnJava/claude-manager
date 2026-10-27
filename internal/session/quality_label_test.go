package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/worker"
)

// runLabelTask drives a fakeclaude run on a one-task queue in a temp git
// repository. With closeTask the "agent" commits a file, leaves a stray one
// and takes the task off the queue as soon as it starts; without it the task
// stays first in the queue, so every run ends unfinished. Returns the end
// events of the runs and the task_done events.
func runLabelTask(t *testing.T, opt *config.OptimizationSettings, closeTask bool, gateRounds int) (runEnds, done []SessionEvent) {
	t.Helper()
	claudePath := buildFakeclaudeForSession(t)
	scenarioDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKECLAUDE_SCENARIO", scenarioDir)

	origGates := runGatesFn
	runGatesFn = func(context.Context, string, []string) worker.GateResult {
		return worker.GateResult{Commands: []worker.GateCommandResult{{Command: "make test", Output: "TestSum: got 3, want 4\n", ExitCode: 1}}}
	}
	t.Cleanup(func() { runGatesFn = origGates })

	dir := t.TempDir()
	git := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	write("TASKS.md", refTaskFile)
	write("STATUS-P1.md", "# queue\nTASKS.md:3\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")

	closed := false
	s := New(Params{
		ID: "it/QL", ProjectName: "it", ProjectPath: dir, ClaudePath: claudePath,
		Gates:        []string{"make test"},
		Optimization: opt,
		Config: config.SessionConfig{Name: "QL", Model: "sonnet", PermissionMode: "bypassPermissions",
			AutoRestart: true, TaskSource: "STATUS-P1.md", Prompt: "hello", GateRounds: gateRounds},
		OnEvent: func(_ string, ev SessionEvent) {
			switch ev.Type {
			case EvtInit:
				if closeTask && !closed {
					closed = true
					write("feature.txt", "done\n")
					git("add", "-A")
					git("commit", "-q", "-m", "the task")
					write("debug.log", "leftover\n")
					write("STATUS-P1.md", "# queue\n")
				}
			case EvtRunEnd:
				runEnds = append(runEnds, ev)
			case EvtTaskDone:
				done = append(done, ev)
			}
		},
	})
	s.retryDelay = 0
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	s.Run(ctx)
	return runEnds, done
}

func labelsOf(evs []SessionEvent) (n int, last *LabelRecord) {
	for _, ev := range evs {
		if ev.Label != nil {
			n++
			last = ev.Label
		}
	}
	return n, last
}

func TestQualityLabel_GatesRedAtTheLimitCarriesEvidence(t *testing.T) {
	ends, done := runLabelTask(t, &config.OptimizationSettings{QualityLabels: true}, true, 1)
	if len(done) != 0 {
		t.Fatalf("a task with red gates must not close: %d task_done", len(done))
	}
	n, l := labelsOf(ends)
	if n != 1 {
		t.Fatalf("run ends carry %d labels, want exactly one: %+v", n, ends)
	}
	if l.Outcome != LabelOutcomeGatesRed || l.TaskPtr != "TASKS.md:3" || !strings.HasPrefix(l.TaskText, "## T-1: first") {
		t.Errorf("label = %+v", l)
	}
	if !strings.Contains(l.Feedback, "TestSum: got 3, want 4") {
		t.Errorf("feedback must be the gates' output, got %q", l.Feedback)
	}
	if !strings.Contains(l.Diff, "feature.txt") || !strings.Contains(l.Diff, "debug.log") {
		t.Errorf("evidence must show the committed change and the stray file: %q", l.Diff)
	}
}

func TestQualityLabel_UnfinishedLoopLabelsOnlyTheLastRun(t *testing.T) {
	ends, _ := runLabelTask(t, &config.OptimizationSettings{QualityLabels: true}, false, 0)
	if len(ends) != maxUnfinishedStreak {
		t.Fatalf("run ends = %d, want %d", len(ends), maxUnfinishedStreak)
	}
	n, l := labelsOf(ends)
	if n != 1 || ends[len(ends)-1].Label == nil {
		t.Fatalf("only the run that stops the loop is labelled, got %d labels", n)
	}
	if l.Outcome != LabelOutcomeUnfinishedLoop || l.TaskPtr != "TASKS.md:3" || l.TaskText == "" {
		t.Errorf("label = %+v", l)
	}
}

func TestQualityLabel_GoodEndingAndOffProduceNothing(t *testing.T) {
	// A task that closes well, gates off.
	ends, done := runLabelTask(t, &config.OptimizationSettings{QualityLabels: true}, true, 0)
	if len(done) == 0 {
		t.Fatal("the task should close")
	}
	if n, _ := labelsOf(append(ends, done...)); n != 0 {
		t.Errorf("a task that ended well produced %d labels", n)
	}
	// Flag off on a task that ends badly.
	ends, _ = runLabelTask(t, &config.OptimizationSettings{}, true, 1)
	if n, _ := labelsOf(ends); n != 0 {
		t.Errorf("flag off but %d labels", n)
	}
}

func TestQualityLabel_OffMakesNoGitCalls(t *testing.T) {
	s := New(Params{ID: "it/O", ProjectName: "it", ProjectPath: t.TempDir(), Config: config.SessionConfig{Name: "O"}})
	ctx := context.Background()
	s.beginLabelCapture(ctx, "TASKS.md:3")
	s.captureLabelBase(ctx, s.ProjectPath)
	if s.labelCap != (labelCapture{}) || s.badEndingLabel(ctx, LabelOutcomeGatesRed, "x") != nil {
		t.Errorf("the flag is off but the session captured %+v", s.labelCap)
	}
}

func TestQualityLabelInfo_SnapshotAndSwitches(t *testing.T) {
	rec := &LabelRecord{TaskPtr: "T.md:1", TaskText: "x", Outcome: LabelOutcomeUnfinishedLoop}
	ms := &managedSession{project: "p", runID: 7, runModel: "opus", lastResultText: "I got stuck",
		session: &Session{ProjectPath: "/p"}}
	m := &SessionManager{cfg: &config.AppConfig{}}
	var got []QualityLabelInfo
	var model string
	done := make(chan struct{}, 1)
	m.SetQualityLabels(func(_ context.Context, i QualityLabelInfo, judgeModel string) error {
		got, model = append(got, i), judgeModel
		done <- struct{}{}
		return nil
	})
	if m.qualityLabelInfo(ms, rec) != nil {
		t.Error("flag off: the manager must not take a snapshot")
	}
	m.cfg.Optimization.QualityLabels = true
	m.cfg.Optimization.QualityLabelModel = "haiku-x"
	if m.qualityLabelInfo(ms, nil) != nil {
		t.Error("no record from the session: nothing to judge")
	}
	info := m.qualityLabelInfo(ms, rec)
	if info == nil || info.RunID != 7 || info.Project != "p" || info.ProjectPath != "/p" || info.Feedback != "I got stuck" {
		t.Fatalf("info = %+v (the agent's last words stand in for missing gate output)", info)
	}
	m.judgeQualityLabel(info)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the judge was not called")
	}
	if len(got) != 1 || got[0].TaskPtr != "T.md:1" || model != "haiku-x" {
		t.Errorf("judged = %+v model %q", got, model)
	}
	m.SetQualityLabels(nil)
	if m.qualityLabelInfo(ms, rec) != nil {
		t.Error("labeller not wired: no snapshot")
	}
}
