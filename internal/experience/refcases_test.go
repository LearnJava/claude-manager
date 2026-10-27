package experience

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/analysis"
	"claude-manager/internal/store"
	"claude-manager/internal/worker"
)

func newRefStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func goodInput() RefCaseInput {
	return RefCaseInput{Project: "p", TaskPtr: "T.md:5", TaskText: "## T-1: x", BaseCommit: "aaa", ResultCommit: "bbb",
		Gates: []string{"go test ./..."}, Rounds: 1, Tokens: 500, DurationSec: 30, Model: "opus", Effort: "high", Success: true}
}

func TestRecordReferenceCase_OnlyGoodTasks(t *testing.T) {
	st := newRefStore(t)
	for name, mut := range map[string]func(*RefCaseInput){
		"failed task":     func(in *RefCaseInput) { in.Success = false },
		"no gates":        func(in *RefCaseInput) { in.Gates = nil },
		"no task text":    func(in *RefCaseInput) { in.TaskText = " " },
		"no base":         func(in *RefCaseInput) { in.BaseCommit = "" },
		"nothing changed": func(in *RefCaseInput) { in.ResultCommit = in.BaseCommit },
	} {
		in := goodInput()
		mut(&in)
		if RefCaseSkipReason(in) == "" {
			t.Errorf("%s: want a skip reason", name)
		}
		if c, err := RecordReferenceCase(st, in); c != nil || err != nil {
			t.Errorf("%s: recorded %+v %v", name, c, err)
		}
	}
	if list, _ := st.ListReferenceCases("p"); len(list) != 0 {
		t.Fatalf("an unsuccessful task became a case: %+v", list)
	}

	c, err := RecordReferenceCase(st, goodInput())
	if err != nil || c == nil || c.ID == 0 || c.Rounds != 1 || c.Tokens != 500 || c.Model != "opus" {
		t.Fatalf("good task = %+v %v", c, err)
	}
	if dup, err := RecordReferenceCase(st, goodInput()); dup != nil || err != nil {
		t.Errorf("the same task from the same commit is recorded twice: %+v %v", dup, err)
	}
	if c, err := RecordReferenceCase(nil, goodInput()); c != nil || err != nil {
		t.Errorf("nil store: %+v %v", c, err)
	}
}

// ---- replay against a real repository ----

func testGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// refRepo makes a repository with a base commit and a later "result" commit.
func refRepo(t *testing.T) (dir, base, result string) {
	t.Helper()
	dir = t.TempDir()
	testGit(t, dir, "init", "-q")
	testGit(t, dir, "checkout", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "-A")
	testGit(t, dir, "commit", "-q", "-m", "base")
	base = testGit(t, dir, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(dir, "result.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testGit(t, dir, "add", "-A")
	testGit(t, dir, "commit", "-q", "-m", "result")
	result = testGit(t, dir, "rev-parse", "HEAD")
	return dir, base, result
}

func storeCase(t *testing.T, st *store.Store, base, result string) *store.ReferenceCase {
	t.Helper()
	in := goodInput()
	in.BaseCommit, in.ResultCommit = base, result
	c, err := RecordReferenceCase(st, in)
	if err != nil || c == nil {
		t.Fatalf("record: %+v %v", c, err)
	}
	return c
}

// execStub is an attempt runner: write[i] says whether attempt i leaves the
// "ok.txt" file the stub gates look for.
type execStub struct {
	write   []bool
	prompts []string
	dirs    []string
	fail    bool
	sawBase bool
}

func (e *execStub) Execute(_ context.Context, dir string, sub analysis.PlannedSubtask, _ string) (*analysis.SubtaskResult, error) {
	if e.fail {
		return nil, errors.New("claude not found")
	}
	i := len(e.prompts)
	e.prompts = append(e.prompts, sub.Prompt)
	e.dirs = append(e.dirs, dir)
	_, baseErr := os.Stat(filepath.Join(dir, "base.txt"))
	_, resErr := os.Stat(filepath.Join(dir, "result.txt"))
	e.sawBase = baseErr == nil && resErr != nil
	if i < len(e.write) && e.write[i] {
		_ = os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("x"), 0o644)
	}
	return &analysis.SubtaskResult{InputTokens: 100, OutputTokens: 50}, nil
}

func stubGates(_ context.Context, root string, gates []string) worker.GateResult {
	if _, err := os.Stat(filepath.Join(root, "ok.txt")); err == nil {
		return worker.GateResult{Passed: true, Commands: []worker.GateCommandResult{{Command: gates[0]}}}
	}
	return worker.GateResult{Commands: []worker.GateCommandResult{{Command: gates[0], Output: "FAIL: ok.txt missing", ExitCode: 1}}}
}

func TestReplayReferenceCase_LeavesBranchesAlone(t *testing.T) {
	dir, base, result := refRepo(t)
	st := newRefStore(t)
	c := storeCase(t, st, base, result)

	headsBefore := testGit(t, dir, "for-each-ref", "refs/heads")
	headBefore := testGit(t, dir, "rev-parse", "HEAD")
	statusBefore := testGit(t, dir, "status", "--porcelain")

	ex := &execStub{write: []bool{true}}
	rep, err := ReplayReferenceCase(context.Background(), st, ReplayOptions{
		Project: "p", ProjectPath: dir, CaseID: c.ID, Model: "sonnet", Effort: "medium", Exec: ex, RunGates: stubGates})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed || rep.Rounds != 0 || rep.Tokens != 150 || rep.Model != "sonnet" {
		t.Fatalf("replay = %+v", rep)
	}
	if !ex.sawBase {
		t.Error("the replay folder must be the base commit, without the original's result")
	}
	if !strings.Contains(ex.prompts[0], "## T-1: x") || !strings.Contains(ex.prompts[0], "uncommitted") {
		t.Errorf("prompt = %q", ex.prompts[0])
	}

	if got := testGit(t, dir, "for-each-ref", "refs/heads"); got != headsBefore {
		t.Errorf("branches changed:\n%s\n->\n%s", headsBefore, got)
	}
	if got := testGit(t, dir, "rev-parse", "HEAD"); got != headBefore {
		t.Errorf("HEAD moved: %s -> %s", headBefore, got)
	}
	if got := testGit(t, dir, "status", "--porcelain"); got != statusBefore {
		t.Errorf("working tree changed: %q", got)
	}
	if wt := testGit(t, dir, "worktree", "list", "--porcelain"); strings.Count(wt, "worktree ") != 1 {
		t.Errorf("replay worktree left behind:\n%s", wt)
	}
	if _, err := os.Stat(ex.dirs[0]); !os.IsNotExist(err) {
		t.Errorf("replay folder %s still exists", ex.dirs[0])
	}
	if rep.ResultCommit == "" {
		t.Fatal("a changed tree must be pinned")
	}
	if refs := testGit(t, dir, "for-each-ref", "refs/cm-replay"); !strings.Contains(refs, rep.ResultCommit) {
		t.Errorf("result %s not pinned under refs/cm-replay: %q", rep.ResultCommit, refs)
	}
	if files := testGit(t, dir, "ls-tree", "--name-only", rep.ResultCommit); !strings.Contains(files, "ok.txt") {
		t.Errorf("pinned tree lacks the agent's file: %q", files)
	}
}

func TestReplayReferenceCase_ReworkRounds(t *testing.T) {
	dir, base, result := refRepo(t)
	st := newRefStore(t)
	c := storeCase(t, st, base, result)

	ex := &execStub{write: []bool{false, true}}
	rep, err := ReplayReferenceCase(context.Background(), st, ReplayOptions{
		Project: "p", ProjectPath: dir, CaseID: c.ID, Model: "m", Exec: ex, RunGates: stubGates})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed || rep.Rounds != 1 || rep.Tokens != 300 {
		t.Fatalf("replay = %+v", rep)
	}
	if len(ex.prompts) != 2 || !strings.Contains(ex.prompts[1], "FAIL: ok.txt missing") {
		t.Errorf("the red gates' output must go back in the next prompt: %q", ex.prompts)
	}

	// Never green: stops exactly at the round limit and says so.
	ex2 := &execStub{}
	rep, err = ReplayReferenceCase(context.Background(), st, ReplayOptions{
		Project: "p", ProjectPath: dir, CaseID: c.ID, Model: "weak", Rounds: 1, Exec: ex2, RunGates: stubGates})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Passed || rep.Rounds != 1 || len(ex2.prompts) != 2 || !strings.Contains(rep.Note, "still red") {
		t.Errorf("never-green replay = %+v after %d attempts", rep, len(ex2.prompts))
	}
	if rep.ResultCommit != "" || !strings.Contains(rep.Note, "changed nothing") {
		t.Errorf("an untouched tree must not be pinned: %+v", rep)
	}
}

func TestReplayReferenceCase_ExecutorFailsToRun(t *testing.T) {
	dir, base, result := refRepo(t)
	st := newRefStore(t)
	c := storeCase(t, st, base, result)
	_, err := ReplayReferenceCase(context.Background(), st, ReplayOptions{
		Project: "p", ProjectPath: dir, CaseID: c.ID, Model: "m", Exec: &execStub{fail: true}, RunGates: stubGates})
	if err == nil {
		t.Fatal("want an error")
	}
	if reps, _ := st.ListReferenceReplays(c.ID); len(reps) != 0 {
		t.Errorf("a replay that never ran was recorded: %+v", reps)
	}
}

func TestReferenceCase_VanishedBaseIsInvalidNotFatal(t *testing.T) {
	dir, base, result := refRepo(t)
	st := newRefStore(t)
	good := storeCase(t, st, base, result)
	in := goodInput()
	in.TaskPtr = "T.md:99"
	in.BaseCommit = strings.Repeat("d", 40)
	in.ResultCommit = strings.Repeat("e", 40)
	gone, err := RecordReferenceCase(st, in)
	if err != nil || gone == nil {
		t.Fatal(err)
	}

	rep, err := BuildReferenceReport(context.Background(), st, "p", dir)
	if err != nil || len(rep.Cases) != 2 {
		t.Fatalf("report = %+v %v", rep, err)
	}
	for _, p := range rep.Cases {
		switch p.CaseID {
		case good.ID:
			if p.InvalidReason != "" {
				t.Errorf("good case marked invalid: %+v", p)
			}
		case gone.ID:
			if p.InvalidReason != InvalidBaseReason {
				t.Errorf("vanished base not marked: %+v", p)
			}
		}
	}
	if stored, _ := st.GetReferenceCase(gone.ID); stored.InvalidReason == "" {
		t.Error("the mark must be stored")
	}

	ex := &execStub{write: []bool{true}}
	if _, err := ReplayReferenceCase(context.Background(), st, ReplayOptions{
		Project: "p", ProjectPath: dir, CaseID: gone.ID, Model: "m", Exec: ex, RunGates: stubGates}); err == nil {
		t.Error("an invalid case must not be replayed")
	}
	if len(ex.prompts) != 0 {
		t.Error("the executor ran for an invalid case")
	}

	// A path that is not a repository cannot prove a commit gone.
	st2 := newRefStore(t)
	c2 := storeCase(t, st2, base, result)
	if _, err := BuildReferenceReport(context.Background(), st2, "p", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if stored, _ := st2.GetReferenceCase(c2.ID); stored.InvalidReason != "" {
		t.Errorf("a non-repository path invalidated a case: %q", stored.InvalidReason)
	}
}

func TestBuildReferenceReport_PairsOriginalWithReplays(t *testing.T) {
	dir, base, result := refRepo(t)
	st := newRefStore(t)
	c := storeCase(t, st, base, result)
	for _, model := range []string{"sonnet", "haiku"} {
		if _, err := ReplayReferenceCase(context.Background(), st, ReplayOptions{
			Project: "p", ProjectPath: dir, CaseID: c.ID, Model: model, Exec: &execStub{write: []bool{true}}, RunGates: stubGates}); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := BuildReferenceReport(context.Background(), st, "p", dir)
	if err != nil || len(rep.Cases) != 1 {
		t.Fatalf("report = %+v %v", rep, err)
	}
	runs := rep.Cases[0].Runs
	if len(runs) != 3 || !runs[0].Original || runs[0].Model != "opus" || runs[1].Model != "sonnet" || runs[2].Model != "haiku" {
		t.Fatalf("runs = %+v", runs)
	}
	if empty, err := BuildReferenceReport(context.Background(), st, "other", dir); err != nil || empty.Cases == nil || len(empty.Cases) != 0 {
		t.Errorf("a project without cases = %+v %v, want an empty list", empty, err)
	}
}
