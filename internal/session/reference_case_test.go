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
)

const refTaskFile = "# Tasks\n\n## T-1: first\nDo the first thing.\n\n```sh\n# a comment inside a fence\n```\n\nMore of T-1.\n\n## T-2: second\nOther.\n"

func TestTaskSectionFromPointer(t *testing.T) {
	// "## T-1" is on line 3.
	got := taskSectionFromPointer(refTaskFile, "TASKS.md:3")
	if !strings.HasPrefix(got, "## T-1: first") || !strings.Contains(got, "More of T-1.") {
		t.Errorf("section = %q", got)
	}
	if strings.Contains(got, "T-2") {
		t.Errorf("the section must stop at the next heading: %q", got)
	}
	if !strings.Contains(got, "# a comment inside a fence") {
		t.Errorf("a '#' line inside a code fence must not end the section: %q", got)
	}
	// A pointer a few lines below the heading finds the heading above it.
	if got2 := taskSectionFromPointer(refTaskFile, "TASKS.md:4"); got2 != got {
		t.Errorf("a pointer inside the section = %q, want the same section", got2)
	}
	for _, p := range []string{"TASKS.md", "TASKS.md:0", "TASKS.md:abc", "TASKS.md:999", ":3"} {
		if s := taskSectionFromPointer(refTaskFile, p); s != "" {
			t.Errorf("pointer %q = %q, want empty", p, s)
		}
	}
	if s := taskSectionFromPointer("no headings here\nat all\n", "TASKS.md:1"); s != "" {
		t.Errorf("no heading = %q, want empty", s)
	}
	if got := pointerFile("docs/TASKS.md:12"); got != "docs/TASKS.md" {
		t.Errorf("pointerFile = %q", got)
	}
}

func TestReferenceCase_SecondStartOnSamePointerDisqualifies(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "TASKS.md"), []byte(refTaskFile), 0o644); err != nil {
		t.Fatal(err)
	}
	s := New(Params{ID: "it/R", ProjectName: "it", ProjectPath: dir,
		Config:       config.SessionConfig{Name: "R"},
		Optimization: &config.OptimizationSettings{ReferenceCases: true}})
	ctx := context.Background()

	s.beginReferenceCase(ctx, "TASKS.md:3")
	s.captureRefBase(ctx, dir)
	if s.refCap.ptr != "TASKS.md:3" || !strings.Contains(s.refCap.text, "first thing") {
		t.Fatalf("capture = %+v", s.refCap)
	}
	// The same task starts again: a slice / retry — tokens and base are partial.
	s.beginReferenceCase(ctx, "TASKS.md:3")
	if s.refCap.ptr != "" {
		t.Errorf("a task spanning runs is still a candidate: %+v", s.refCap)
	}
	s.beginReferenceCase(ctx, "TASKS.md:3")
	if s.refCap.ptr != "" {
		t.Errorf("a third start re-armed the capture: %+v", s.refCap)
	}
	if rec := s.finishReferenceCase(ctx, 0); rec != nil {
		t.Errorf("record = %+v, want nil", rec)
	}
	// After the task ends, the next one starts clean.
	s.beginReferenceCase(ctx, "TASKS.md:3")
	if s.refCap.ptr != "TASKS.md:3" {
		t.Errorf("a new task after the spanned one = %+v", s.refCap)
	}
}

func TestReferenceCase_OffMakesNoGitCalls(t *testing.T) {
	s := New(Params{ID: "it/O", ProjectName: "it", ProjectPath: t.TempDir(), Config: config.SessionConfig{Name: "O"}})
	s.beginReferenceCase(context.Background(), "TASKS.md:3")
	s.captureRefBase(context.Background(), s.ProjectPath)
	if s.refCap != (refCapture{}) || s.finishReferenceCase(context.Background(), 0) != nil {
		t.Errorf("the flag is off but the session captured %+v", s.refCap)
	}
}

// runClosingTask drives a fakeclaude run whose "agent" commits a file and
// takes the task off the queue as soon as it starts (EvtInit); returns the
// EvtTaskDone events.
func runClosingTask(t *testing.T, opt *config.OptimizationSettings) []SessionEvent {
	t.Helper()
	claudePath := buildFakeclaudeForSession(t)
	scenarioDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKECLAUDE_SCENARIO", scenarioDir)

	dir := t.TempDir()
	git := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
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

	var done []SessionEvent
	closed := false
	s := New(Params{
		ID: "it/RC", ProjectName: "it", ProjectPath: dir, ClaudePath: claudePath,
		Gates:        []string{"go test ./..."},
		Optimization: opt,
		Config: config.SessionConfig{Name: "RC", Model: "sonnet", PermissionMode: "bypassPermissions",
			AutoRestart: true, TaskSource: "STATUS-P1.md", Prompt: "hello"},
		OnEvent: func(_ string, ev SessionEvent) {
			switch ev.Type {
			case EvtInit:
				if !closed {
					closed = true
					write("feature.txt", "done\n")
					write("STATUS-P1.md", "# queue\n")
					git("add", "-A")
					git("commit", "-q", "-m", "the task")
				}
			case EvtTaskDone:
				done = append(done, ev)
			}
		},
	})
	s.retryDelay = 0
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	s.Run(ctx)
	return done
}

func TestReferenceCase_ClosedTaskIsReported(t *testing.T) {
	done := runClosingTask(t, &config.OptimizationSettings{ReferenceCases: true})
	if len(done) == 0 || done[0].RefCase == nil {
		t.Fatalf("task_done events = %+v, want a reference case on the first", done)
	}
	rc := done[0].RefCase
	if rc.TaskPtr != "TASKS.md:3" || !strings.HasPrefix(rc.TaskText, "## T-1: first") || strings.Contains(rc.TaskText, "T-2") {
		t.Errorf("task = %q / %q", rc.TaskPtr, rc.TaskText)
	}
	if rc.BaseCommit == "" || rc.ResultCommit == "" || rc.BaseCommit == rc.ResultCommit {
		t.Errorf("commits base=%q result=%q, want two different ones", rc.BaseCommit, rc.ResultCommit)
	}
	if len(rc.Gates) != 1 || rc.Rounds != 0 {
		t.Errorf("gates %v rounds %d", rc.Gates, rc.Rounds)
	}
}

func TestReferenceCase_FlagOffReportsNothing(t *testing.T) {
	done := runClosingTask(t, &config.OptimizationSettings{})
	if len(done) == 0 {
		t.Fatal("the task should still close")
	}
	for _, ev := range done {
		if ev.RefCase != nil {
			t.Errorf("flag off but task_done carries %+v", ev.RefCase)
		}
	}
}

func TestReferenceCaseInfo_MeasurementsAndSwitches(t *testing.T) {
	rec := &RefCaseRecord{TaskPtr: "T.md:1", TaskText: "x", BaseCommit: "a", ResultCommit: "b"}
	ms := &managedSession{project: "p", inputTokens: 100, outputTokens: 40, cacheReadTokens: 99999,
		runStartedAt: time.Now().Add(-90 * time.Second),
		session:      &Session{Config: config.SessionConfig{Model: "opus", Effort: "high"}}}

	m := &SessionManager{cfg: &config.AppConfig{}}
	var got []ReferenceCaseInfo
	m.SetReferenceCases(func(i ReferenceCaseInfo) error { got = append(got, i); return nil })
	if m.referenceCaseInfo(ms, rec) != nil {
		t.Error("flag off: the manager must not take a snapshot")
	}
	m.cfg.Optimization.ReferenceCases = true
	if m.referenceCaseInfo(ms, nil) != nil {
		t.Error("no record from the session: nothing to store")
	}
	info := m.referenceCaseInfo(ms, rec)
	if info == nil || info.Tokens != 140 || info.Model != "opus" || info.Effort != "high" || info.DurationSec < 89 || info.Project != "p" {
		t.Fatalf("info = %+v", info)
	}
	m.recordReferenceCase(info)
	if len(got) != 1 || got[0].TaskPtr != "T.md:1" {
		t.Errorf("recorded = %+v", got)
	}
	m.SetReferenceCases(nil)
	if m.referenceCaseInfo(ms, rec) != nil {
		t.Error("recorder not wired: no snapshot")
	}
}
