package worker

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"claude-manager/internal/hooks"
)

// FeedbackByteLimit caps the size of GateResult.Feedback, mirroring the
// lumen-browser bench (~14KB keeps a round's feedback within a model's
// effective context without truncating the part that matters: build/test
// errors, which surface near the end of the output, are kept by truncating
// the head rather than the tail.
const FeedbackByteLimit = 14 * 1024

// GateCommandResult is the outcome of one gate command.
type GateCommandResult struct {
	Command  string
	Output   string // combined stdout+stderr
	ExitCode int
	Duration time.Duration
	// Err is set only when the command could not be run at all (e.g. binary
	// not found) — as opposed to running and exiting non-zero, which is
	// reported via ExitCode alone.
	Err error
}

// Passed reports whether this command ran and exited 0.
func (r GateCommandResult) Passed() bool {
	return r.Err == nil && r.ExitCode == 0
}

// GateResult is the outcome of running a project's full gate sequence.
type GateResult struct {
	Commands []GateCommandResult // commands actually run — stops at the first failure
	Passed   bool
}

// FailedCommand returns the command that failed, or nil if Passed.
func (r GateResult) FailedCommand() *GateCommandResult {
	if r.Passed || len(r.Commands) == 0 {
		return nil
	}
	return &r.Commands[len(r.Commands)-1]
}

// Feedback formats the executed gate commands as a model-facing rejection
// message. Gates are the ground truth of mixed programming — model-written
// tests are never trusted (lumen bench: 6/7 models write self-contradictory
// tests) — so this exact output, not a paraphrase, is what goes back to the
// worker for the next round. Truncated to FeedbackByteLimit from the head:
// compiler/test failures appear near the end of the output.
func (r GateResult) Feedback() string {
	var b strings.Builder
	for _, c := range r.Commands {
		status := "ok"
		switch {
		case c.Err != nil:
			status = fmt.Sprintf("FAILED (%v)", c.Err)
		case c.ExitCode != 0:
			status = fmt.Sprintf("FAILED (exit %d)", c.ExitCode)
		}
		fmt.Fprintf(&b, "$ %s  [%s]\n", c.Command, status)
		b.WriteString(c.Output)
		if c.Output != "" && !strings.HasSuffix(c.Output, "\n") {
			b.WriteByte('\n')
		}
	}
	return truncateHead(b.String(), FeedbackByteLimit)
}

// truncateHead keeps the last limit bytes of s, marking that a prefix was cut.
func truncateHead(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := len(s) - limit
	return fmt.Sprintf("...[truncated %d bytes]...\n%s", cut, s[cut:])
}

// RunGates runs gates sequentially in root (a task worktree), stopping at the
// first non-zero exit. Unlike post_task_hook, gates are blocking: a red gate
// rejects the round instead of merely being logged.
func RunGates(ctx context.Context, root string, gates []string) GateResult {
	var res GateResult
	for _, g := range gates {
		h := hooks.RunHookContext(ctx, g, root, 0)
		cr := GateCommandResult{
			Command:  g,
			Output:   h.Stdout + h.Stderr,
			ExitCode: h.ExitCode,
			Duration: h.Duration,
		}
		if h.Err != nil {
			var exitErr *exec.ExitError
			if !errors.As(h.Err, &exitErr) {
				cr.Err = h.Err
			}
		}
		res.Commands = append(res.Commands, cr)
		if !cr.Passed() {
			return res
		}
	}
	res.Passed = true
	return res
}

var trailerReplacer = strings.NewReplacer("/", "-", ":", "-", " ", "-")

// CommitWorktree stages all changes in root and commits them, appending a
// Co-Authored-By trailer naming the worker that produced the patch — called
// only after RunGates reports Passed, so a commit always represents a green
// round.
func CommitWorktree(ctx context.Context, root, workerName, model, message string) error {
	if _, err := runGit(ctx, root, "add", "-A"); err != nil {
		return fmt.Errorf("worker: git add: %w", err)
	}

	if message == "" {
		message = "mixed programming: apply " + workerName + " patch"
	}
	email := trailerReplacer.Replace(model) + "@workers.local"
	full := message + "\n\nCo-Authored-By: " + workerName + " <" + email + ">\n"

	if _, err := runGit(ctx, root, "commit", "-m", full); err != nil {
		return fmt.Errorf("worker: git commit: %w", err)
	}
	return nil
}

// runGit invokes git directly (not through a shell) so commit messages and
// paths never pass through shell quoting/interpolation.
func runGit(ctx context.Context, root string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
