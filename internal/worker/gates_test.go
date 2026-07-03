package worker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var errTestUnrunnable = errors.New("exec: \"cm-worker-ghost\": executable file not found in $PATH")

// echoCmd returns a gate command that prints to stdout and stderr and exits
// with the given code, portable across the Windows/Unix shells hooks.RunHook
// dispatches to.
func echoCmd(t *testing.T, stdout, stderr string, exitCode int) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return strings.TrimSpace(strings.Join([]string{
			echoPart(stdout, false),
			echoPart(stderr, true),
			exitPart(exitCode),
		}, " & "))
	}
	return strings.TrimSpace(strings.Join([]string{
		echoPart(stdout, false),
		echoPart(stderr, true),
		exitPart(exitCode),
	}, "; "))
}

func echoPart(text string, stderr bool) string {
	if text == "" {
		return "rem noop"
	}
	if runtime.GOOS == "windows" {
		if stderr {
			return "echo " + text + " 1>&2"
		}
		return "echo " + text
	}
	if stderr {
		return "echo " + text + " >&2"
	}
	return "echo " + text
}

func exitPart(code int) string {
	if runtime.GOOS == "windows" {
		return "exit /b " + itoa(code)
	}
	return "exit " + itoa(code)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

func TestRunGatesAllPass(t *testing.T) {
	dir := t.TempDir()
	gates := []string{
		echoCmd(t, "step one", "", 0),
		echoCmd(t, "step two", "", 0),
	}
	res := RunGates(context.Background(), dir, gates)
	if !res.Passed {
		t.Fatalf("Passed = false, feedback:\n%s", res.Feedback())
	}
	if len(res.Commands) != 2 {
		t.Fatalf("Commands = %d, want 2", len(res.Commands))
	}
	if res.FailedCommand() != nil {
		t.Error("FailedCommand should be nil when Passed")
	}
}

func TestRunGatesStopsAtFirstFailure(t *testing.T) {
	dir := t.TempDir()
	gates := []string{
		echoCmd(t, "ok", "", 0),
		echoCmd(t, "boom out", "boom err", 1),
		echoCmd(t, "never runs", "", 0),
	}
	res := RunGates(context.Background(), dir, gates)
	if res.Passed {
		t.Fatal("Passed = true, want false")
	}
	if len(res.Commands) != 2 {
		t.Fatalf("Commands = %d, want 2 (third gate must not run)", len(res.Commands))
	}
	failed := res.FailedCommand()
	if failed == nil || failed.ExitCode != 1 {
		t.Fatalf("FailedCommand = %+v", failed)
	}
	if !strings.Contains(failed.Output, "boom out") || !strings.Contains(failed.Output, "boom err") {
		t.Errorf("failed command output missing stdout/stderr: %q", failed.Output)
	}
}

func TestRunGatesCapturesOutput(t *testing.T) {
	dir := t.TempDir()
	res := RunGates(context.Background(), dir, []string{echoCmd(t, "hello-stdout", "hello-stderr", 0)})
	if !res.Passed {
		t.Fatalf("Passed = false: %s", res.Feedback())
	}
	out := res.Commands[0].Output
	if !strings.Contains(out, "hello-stdout") || !strings.Contains(out, "hello-stderr") {
		t.Errorf("output = %q, want both streams", out)
	}
}

func TestRunGatesCommandNotFound(t *testing.T) {
	// hooks.RunHookContext always dispatches through the platform shell
	// (cmd /C / sh -c), so a missing binary surfaces as a non-zero exit from
	// the shell itself, not a raw Go exec error — Passed() must still be
	// false either way.
	dir := t.TempDir()
	res := RunGates(context.Background(), dir, []string{"cm-worker-definitely-not-a-real-binary-xyz"})
	if res.Passed {
		t.Fatal("Passed = true, want false")
	}
	if res.Commands[0].Passed() {
		t.Error("Passed() = true for a command that could not run")
	}
	if !strings.Contains(res.Feedback(), "FAILED") {
		t.Errorf("feedback missing FAILED marker: %s", res.Feedback())
	}
}

// TestGateCommandResultErrForUnrunnableShell exercises the Err path directly
// (rather than through RunGates/hooks, which always finds cmd/sh itself):
// Err set and ExitCode==0 must still report Passed()==false and surface in
// Feedback distinctly from a non-zero exit.
func TestGateCommandResultErrForUnrunnableShell(t *testing.T) {
	res := GateResult{Commands: []GateCommandResult{
		{Command: "ghost", ExitCode: 0, Err: errTestUnrunnable},
	}}
	if res.Commands[0].Passed() {
		t.Error("Passed() = true despite Err set")
	}
	fb := res.Feedback()
	if !strings.Contains(fb, "FAILED") || !strings.Contains(fb, errTestUnrunnable.Error()) {
		t.Errorf("feedback missing Err detail: %s", fb)
	}
}

func TestGateResultFeedbackIncludesCommandsAndStatus(t *testing.T) {
	dir := t.TempDir()
	res := RunGates(context.Background(), dir, []string{
		echoCmd(t, "build ok", "", 0),
		echoCmd(t, "test output", "assertion failed", 1),
	})
	fb := res.Feedback()
	if !strings.Contains(fb, "build ok") {
		t.Error("feedback missing passing command's output")
	}
	if !strings.Contains(fb, "assertion failed") || !strings.Contains(fb, "FAILED (exit 1)") {
		t.Errorf("feedback missing failure detail: %s", fb)
	}
}

func TestGateResultFeedbackTruncatesKeepingTail(t *testing.T) {
	long := strings.Repeat("A", FeedbackByteLimit) + "TAIL_MARKER_END"
	res := GateResult{
		Commands: []GateCommandResult{{Command: "big", Output: long, ExitCode: 1}},
	}
	fb := res.Feedback()
	if len(fb) > FeedbackByteLimit+200 {
		t.Errorf("feedback len = %d, want roughly <= %d", len(fb), FeedbackByteLimit)
	}
	if !strings.Contains(fb, "TAIL_MARKER_END") {
		t.Error("feedback dropped the tail — the part with the actual error")
	}
	if !strings.Contains(fb, "truncated") {
		t.Error("feedback missing truncation marker")
	}
}

func TestGateResultFeedbackNoTruncationWhenShort(t *testing.T) {
	res := GateResult{Commands: []GateCommandResult{{Command: "x", Output: "short", ExitCode: 0}}}
	fb := res.Feedback()
	if strings.Contains(fb, "truncated") {
		t.Errorf("short feedback should not be marked truncated: %s", fb)
	}
}

// --- CommitWorktree ---

func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available on PATH")
	}
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
}

func TestCommitWorktreeSuccess(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := CommitWorktree(context.Background(), dir, "step37", "stepfun/step-3.7-flash:free", "apply patch for MP-04")
	if err != nil {
		t.Fatalf("CommitWorktree: %v", err)
	}

	cmd := exec.Command("git", "log", "-1", "--pretty=%B")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log: %v: %s", err, out)
	}
	msg := string(out)
	if !strings.Contains(msg, "apply patch for MP-04") {
		t.Errorf("commit message missing subject: %s", msg)
	}
	if !strings.Contains(msg, "Co-Authored-By: step37 <stepfun-step-3.7-flash-free@workers.local>") {
		t.Errorf("commit message missing trailer: %s", msg)
	}
}

func TestCommitWorktreeDefaultMessage(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644)

	if err := CommitWorktree(context.Background(), dir, "nemotron-ultra", "nvidia/nemotron-3-ultra-550b-a55b:free", ""); err != nil {
		t.Fatalf("CommitWorktree: %v", err)
	}
	cmd := exec.Command("git", "log", "-1", "--pretty=%B")
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	if !strings.Contains(string(out), "nemotron-ultra") {
		t.Errorf("default message missing worker name: %s", out)
	}
}

func TestCommitWorktreeNoChangesFails(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	initGitRepo(t, dir)

	err := CommitWorktree(context.Background(), dir, "step37", "stepfun/step-3.7-flash:free", "empty")
	if err == nil {
		t.Fatal("expected error committing with nothing staged")
	}
}
