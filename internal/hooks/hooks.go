// Package hooks provides pre/post task shell command execution
// for session lifecycles (see PLAN.md section 9).
package hooks

import (
	"context"
	"errors"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// Result holds the outcome of running a hook command.
type Result struct {
	Command  string
	Cwd      string
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	Err      error
}

// Success reports whether the hook exited cleanly with code 0.
func (r *Result) Success() bool {
	return r.Err == nil && r.ExitCode == 0
}

// RunHook executes the given shell command in cwd, capturing stdout, stderr,
// and exit code. An empty command returns a zero-valued Result with Err set
// to nil and ExitCode 0 (treated as a no-op). The command is invoked through
// the platform shell (cmd /C on Windows, sh -c elsewhere) so users can pass
// arbitrary shell expressions like "git fetch && make test".
func RunHook(command, cwd string) Result {
	cmd := strings.TrimSpace(command)
	res := Result{Command: cmd, Cwd: cwd}
	if cmd == "" {
		return res
	}

	return RunHookContext(context.Background(), cmd, cwd, 0)
}

// RunHookContext is like RunHook but accepts a context for cancellation and
// an optional timeout. A non-positive timeout means "no timeout"; ctx is
// honoured regardless.
func RunHookContext(ctx context.Context, command, cwd string, timeout time.Duration) Result {
	cmd := strings.TrimSpace(command)
	res := Result{Command: cmd, Cwd: cwd}
	if cmd == "" {
		return res
	}

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	start := time.Now()

	var execCmd *exec.Cmd
	if runtime.GOOS == "windows" {
		execCmd = exec.CommandContext(ctx, "cmd", "/C", cmd)
	} else {
		execCmd = exec.CommandContext(ctx, "sh", "-c", cmd)
	}
	if cwd != "" {
		execCmd.Dir = cwd
	}

	var stdout, stderr strings.Builder
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	err := execCmd.Run()
	res.Duration = time.Since(start)
	res.Stdout = stdout.String()
	res.Stderr = stderr.String()

	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitCode()
			// Non-zero exit code: surface the err so callers can check Success().
			res.Err = err
		} else {
			res.ExitCode = -1
			res.Err = err
		}
		return res
	}

	if execCmd.ProcessState != nil {
		res.ExitCode = execCmd.ProcessState.ExitCode()
	}
	return res
}
