package experience

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/logger"
	"claude-manager/internal/proc"
	"claude-manager/internal/store"
	"claude-manager/internal/worker"
)

// Reference cases (WASTE-TASKS.md WS-06). Comparing models and settings fairly
// needs the same task run twice; every task that finished well already is such
// a case — text, starting commit, gates, a known right answer. A case is stored
// as a by-product of the work, replayed in a throwaway detached worktree, and
// reported next to the original. Project branches are never touched.

// RefCaseInput is what a finished task hands over to be considered a case.
// Success means the task closed and, when strict gates are on, they went green.
type RefCaseInput struct {
	Project      string
	TaskPtr      string
	TaskText     string
	BaseCommit   string
	ResultCommit string
	Gates        []string
	Rounds       int
	Tokens       int64 // input+output, cache excluded
	DurationSec  int
	Model        string
	Effort       string
	Success      bool
}

// RefCaseSkipReason says why a finished task is not a case, "" when it is. A
// task that ended badly is not a reference: a case is one with a known right
// answer. Without gates there is nothing to check a replay against, and
// without a text or a changed commit range there is nothing to replay.
func RefCaseSkipReason(in RefCaseInput) string {
	switch {
	case !in.Success:
		return "the task did not end well"
	case len(in.Gates) == 0:
		return "the project has no gates to check a replay with"
	case strings.TrimSpace(in.TaskText) == "":
		return "the task text could not be read"
	case in.BaseCommit == "" || in.ResultCommit == "":
		return "the commits could not be read"
	case in.BaseCommit == in.ResultCommit:
		return "the task changed nothing in the repository"
	}
	return ""
}

// RecordReferenceCase stores a finished task as a case. Returns (nil, nil)
// when it is not one (see RefCaseSkipReason) or is already stored.
func RecordReferenceCase(st *store.Store, in RefCaseInput) (*store.ReferenceCase, error) {
	if st == nil || RefCaseSkipReason(in) != "" {
		return nil, nil
	}
	dup, err := st.HasReferenceCase(in.Project, in.TaskPtr, in.BaseCommit)
	if err != nil || dup {
		return nil, err
	}
	c := &store.ReferenceCase{
		Project: in.Project, TaskPtr: in.TaskPtr, TaskText: in.TaskText,
		BaseCommit: in.BaseCommit, GateCmds: in.Gates, ResultCommit: in.ResultCommit,
		Rounds: in.Rounds, Tokens: in.Tokens, DurationSec: in.DurationSec,
		Model: in.Model, Effort: in.Effort,
	}
	if err := st.InsertReferenceCase(c); err != nil {
		return nil, err
	}
	return c, nil
}

// ---- report ----

// ReferenceRun is one configuration's outcome on a case: the original run
// (Original=true, always green) or a replay.
type ReferenceRun struct {
	Original     bool
	Model        string
	Effort       string
	Passed       bool
	Rounds       int
	Tokens       int64
	DurationSec  int
	ResultCommit string
	Note         string
	At           time.Time
}

// ReferencePair is one task with every configuration that has run it.
// InvalidReason is set when the case can no longer be replayed.
type ReferencePair struct {
	CaseID        int64
	TaskPtr       string
	BaseCommit    string
	Gates         []string
	InvalidReason string
	Runs          []ReferenceRun // the original first, then replays oldest first
}

// ReferenceReport is a project's reference cases, one pair per task.
type ReferenceReport struct {
	Project string
	Cases   []ReferencePair
}

// InvalidBaseReason is the reason recorded on a case whose commit is gone.
const InvalidBaseReason = "base commit is gone from the repository"

// BuildReferenceReport lists a project's cases with their replays. With
// projectPath set it first checks that each case's base_commit still exists
// and marks the ones that vanished invalid (the report still lists them).
func BuildReferenceReport(ctx context.Context, st *store.Store, project, projectPath string) (ReferenceReport, error) {
	rep := ReferenceReport{Project: project, Cases: []ReferencePair{}}
	if st == nil {
		return rep, errors.New("no store")
	}
	cases, err := st.ListReferenceCases(project)
	if err != nil {
		return rep, err
	}
	for _, c := range cases {
		if c.InvalidReason == "" && projectPath != "" {
			c.InvalidReason = checkBaseCommit(ctx, st, c, projectPath)
		}
		pair := ReferencePair{
			CaseID: c.ID, TaskPtr: c.TaskPtr, BaseCommit: c.BaseCommit, Gates: c.GateCmds,
			InvalidReason: c.InvalidReason,
			Runs: []ReferenceRun{{
				Original: true, Model: c.Model, Effort: c.Effort, Passed: true, Rounds: c.Rounds,
				Tokens: c.Tokens, DurationSec: c.DurationSec, ResultCommit: c.ResultCommit, At: c.CreatedAt,
			}},
		}
		replays, err := st.ListReferenceReplays(c.ID)
		if err != nil {
			return rep, err
		}
		for _, r := range replays {
			pair.Runs = append(pair.Runs, ReferenceRun{
				Model: r.Model, Effort: r.Effort, Passed: r.Passed, Rounds: r.Rounds, Tokens: r.Tokens,
				DurationSec: r.DurationSec, ResultCommit: r.ResultCommit, Note: r.Note, At: r.CreatedAt,
			})
		}
		rep.Cases = append(rep.Cases, pair)
	}
	return rep, nil
}

// checkBaseCommit returns InvalidBaseReason (and stores it) when the case's
// base commit is provably missing; "" when it exists or git cannot tell.
func checkBaseCommit(ctx context.Context, st *store.Store, c *store.ReferenceCase, projectPath string) string {
	exists, err := commitExists(ctx, projectPath, c.BaseCommit)
	if err != nil || exists {
		return ""
	}
	if err := st.SetReferenceCaseInvalid(c.ID, InvalidBaseReason); err != nil {
		logger.L.Error("experience.refcase_invalidate_failed", "case", c.ID, "error", err)
	}
	return InvalidBaseReason
}

// ---- replay ----

// DefaultReplayRounds is how many rework rounds a replay gets after its first
// attempt when ReplayOptions.Rounds is 0.
const DefaultReplayRounds = 2

// ReplayOptions describes one replay of a case.
type ReplayOptions struct {
	Project     string
	ProjectPath string
	CaseID      int64
	Model       string
	Effort      string
	// Rounds is the number of rework rounds after the first attempt;
	// 0 = DefaultReplayRounds.
	Rounds int
	// Exec runs one attempt as a one-shot model call in the replay folder.
	Exec analysis.SubtaskExecutor
	// RunGates defaults to worker.RunGates.
	RunGates func(ctx context.Context, root string, gates []string) worker.GateResult
}

// replayPrompt frames the task text for a throwaway checkout: the original
// session ran under the project's git protocol (branch, merge, push), which a
// replay must not repeat.
func replayPrompt(taskText string) string {
	return "This is a measured replay of an already finished task, in a throwaway checkout. " +
		"Implement the task below by editing files in the working folder only. " +
		"Do not commit, create or switch branches, merge, push, or edit the task queue or status files — leave your changes uncommitted. " +
		"The project's gates run on the folder afterwards.\n\n" + taskText
}

func replayReworkPrompt(taskText string, round, max int, res worker.GateResult) string {
	return replayPrompt(taskText) + fmt.Sprintf("\n\nThe gates failed after your previous attempt (rework round %d of %d); "+
		"the working folder keeps your changes. Fix the cause. The gates' output:\n\n%s", round, max, res.Feedback())
}

// ReplayReferenceCase runs a stored case again with another model/effort: a
// detached worktree at base_commit, the task text as the prompt, the same gates,
// up to Rounds rework rounds. The outcome is stored beside the original. No
// project branch is touched; the resulting tree is pinned under
// refs/cm-replay/ for inspection and the worktree is removed.
func ReplayReferenceCase(ctx context.Context, st *store.Store, o ReplayOptions) (*store.ReferenceReplay, error) {
	if st == nil || o.Exec == nil {
		return nil, errors.New("replay needs a store and an executor")
	}
	c, err := st.GetReferenceCase(o.CaseID)
	if err != nil {
		return nil, err
	}
	if c == nil || c.Project != o.Project {
		return nil, fmt.Errorf("reference case %d not found in project %q", o.CaseID, o.Project)
	}
	if c.InvalidReason != "" {
		return nil, fmt.Errorf("reference case %d is not usable: %s", c.ID, c.InvalidReason)
	}
	if len(c.GateCmds) == 0 {
		return nil, fmt.Errorf("reference case %d has no gates", c.ID)
	}
	if exists, err := commitExists(ctx, o.ProjectPath, c.BaseCommit); err != nil {
		return nil, fmt.Errorf("check base commit: %w", err)
	} else if !exists {
		_ = st.SetReferenceCaseInvalid(c.ID, InvalidBaseReason)
		return nil, fmt.Errorf("reference case %d is not usable: %s", c.ID, InvalidBaseReason)
	}

	maxRounds := o.Rounds
	if maxRounds <= 0 {
		maxRounds = DefaultReplayRounds
	}
	rep := &store.ReferenceReplay{CaseID: c.ID, Model: o.Model, Effort: o.Effort}
	err = withDetachedWorktree(ctx, o.ProjectPath, c.BaseCommit, func(dir string) error {
		out, err := runInFolder(ctx, dir, folderRun{
			Name: fmt.Sprintf("replay-%d", c.ID), PinTag: fmt.Sprintf("case-%d", c.ID),
			TaskText: c.TaskText, Gates: c.GateCmds, Model: o.Model, Effort: o.Effort,
			MaxRounds: maxRounds, Exec: o.Exec, RunGates: o.RunGates,
		})
		if err != nil {
			return err
		}
		rep.Passed, rep.Rounds, rep.Tokens, rep.DurationSec = out.Passed, out.Rounds, out.Tokens, out.DurationSec
		rep.ResultCommit, rep.Note = out.ResultCommit, out.Note
		return nil
	})
	if err != nil {
		return nil, err
	}

	if err := st.InsertReferenceReplay(rep); err != nil {
		return nil, err
	}
	return rep, nil
}

// withDetachedWorktree checks commit out into a throwaway detached worktree of
// the repository at projectPath, calls fn with its folder and removes it again.
// No project branch is created, moved or checked out.
func withDetachedWorktree(ctx context.Context, projectPath, commit string, fn func(dir string) error) error {
	dir, err := os.MkdirTemp("", "cm-replay-*")
	if err != nil {
		return err
	}
	if _, err := gitRun(ctx, projectPath, nil, "worktree", "add", "--detach", dir, commit); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("create replay worktree: %w", err)
	}
	defer func() {
		_, _ = gitRun(context.Background(), projectPath, nil, "worktree", "remove", "--force", dir)
		_ = os.RemoveAll(dir)
		_, _ = gitRun(context.Background(), projectPath, nil, "worktree", "prune")
	}()
	return fn(dir)
}

// folderRun is one task attempted in a prepared folder: the first attempt, then
// up to MaxRounds rework rounds while the gates stay red (0 = a single attempt).
// PinTag names the result under refs/cm-replay/.
type folderRun struct {
	Name      string
	PinTag    string
	TaskText  string
	Gates     []string
	Model     string
	Effort    string
	MaxRounds int
	// ContextAppend travels to the model as --append-system-prompt.
	ContextAppend string
	Exec          analysis.SubtaskExecutor
	// RunGates defaults to worker.RunGates.
	RunGates func(ctx context.Context, root string, gates []string) worker.GateResult
}

type folderOutcome struct {
	Passed       bool
	Rounds       int
	Tokens       int64
	DurationSec  int
	ResultCommit string
	Note         string
}

// runInFolder runs the attempt/rework loop of a replay in dir and pins the
// result. It fails only when the very first attempt could not run at all.
func runInFolder(ctx context.Context, dir string, r folderRun) (*folderOutcome, error) {
	runGates := r.RunGates
	if runGates == nil {
		runGates = worker.RunGates
	}
	sub := analysis.PlannedSubtask{ID: r.Name, Name: "replay", Model: r.Model, Effort: r.Effort}
	out := &folderOutcome{}
	prompt := replayPrompt(r.TaskText)
	var spent time.Duration
	for attempt := 0; ; attempt++ {
		sub.Prompt = prompt
		began := time.Now()
		res, execErr := r.Exec.Execute(ctx, dir, sub, r.ContextAppend)
		spent += time.Since(began)
		if execErr != nil {
			if attempt == 0 {
				return nil, fmt.Errorf("replay attempt failed to run: %w", execErr)
			}
			out.Note = "attempt " + fmt.Sprint(attempt+1) + " failed to run: " + execErr.Error()
			break
		}
		out.Tokens += res.InputTokens + res.OutputTokens
		gr := runGates(ctx, dir, r.Gates)
		if gr.Passed {
			out.Passed = true
			break
		}
		if attempt >= r.MaxRounds {
			if r.MaxRounds == 0 {
				out.Note = "gates red; rework is off for this run"
			} else {
				out.Note = fmt.Sprintf("gates still red after %d rework rounds", r.MaxRounds)
			}
			break
		}
		out.Rounds++
		prompt = replayReworkPrompt(r.TaskText, out.Rounds, r.MaxRounds, gr)
	}
	out.DurationSec = int(spent.Seconds())
	out.ResultCommit = pinReplayResult(ctx, dir, r.PinTag, &out.Note)
	return out, nil
}

// pinReplayResult snapshots the replay folder's tree as a commit on top of
// HEAD and keeps it alive under refs/cm-replay/ — plumbing only (no hooks, no
// config, no branch). Returns "" when the agent changed nothing.
func pinReplayResult(ctx context.Context, dir, tag string, note *string) string {
	fail := func(step string, err error) string {
		appendNote(note, "result not pinned: "+step+": "+err.Error())
		return ""
	}
	if _, err := gitRun(ctx, dir, nil, "add", "-A"); err != nil {
		return fail("add", err)
	}
	tree, err := gitRun(ctx, dir, nil, "write-tree")
	if err != nil {
		return fail("write-tree", err)
	}
	head, err := gitRun(ctx, dir, nil, "rev-parse", "HEAD")
	if err != nil {
		return fail("rev-parse", err)
	}
	headTree, err := gitRun(ctx, dir, nil, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return fail("rev-parse tree", err)
	}
	if tree == headTree {
		appendNote(note, "the agent changed nothing")
		return ""
	}
	env := []string{
		"GIT_AUTHOR_NAME=cm-replay", "GIT_AUTHOR_EMAIL=cm-replay@localhost",
		"GIT_COMMITTER_NAME=cm-replay", "GIT_COMMITTER_EMAIL=cm-replay@localhost",
	}
	sha, err := gitRun(ctx, dir, env, "commit-tree", tree, "-p", head, "-m", "replay "+tag)
	if err != nil {
		return fail("commit-tree", err)
	}
	ref := fmt.Sprintf("refs/cm-replay/%s-%d", tag, time.Now().UnixNano())
	if _, err := gitRun(ctx, dir, nil, "update-ref", ref, sha); err != nil {
		return fail("update-ref", err)
	}
	return sha
}

func appendNote(note *string, s string) {
	if *note != "" {
		*note += "; "
	}
	*note += s
}

// ---- git ----

// commitExists reports whether sha is a commit of the repository at dir. An
// error means git could not tell (not a repository, git missing) — the caller
// must not conclude the commit is gone.
func commitExists(ctx context.Context, dir, sha string) (bool, error) {
	_, err := gitRun(ctx, dir, nil, "rev-parse", "--verify", "--quiet", sha+"^{commit}")
	if err == nil {
		return true, nil
	}
	var ee *gitExitError
	if errors.As(err, &ee) && ee.Code == 1 {
		return false, nil
	}
	return false, err
}

type gitExitError struct {
	Code   int
	Stderr string
}

func (e *gitExitError) Error() string {
	return fmt.Sprintf("git exited with %d: %s", e.Code, strings.TrimSpace(e.Stderr))
}

// gitRun runs git in dir with extra environment variables and returns its
// trimmed stdout.
func gitRun(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	proc.HideConsole(cmd)
	cmd.Dir = filepath.Clean(dir)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", &gitExitError{Code: ee.ExitCode(), Stderr: string(ee.Stderr)}
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
