// Package proc holds platform-specific tweaks for spawned subprocesses.
//
// The manager is a GUI application: on Windows every console subprocess
// (claude CLI, git, cmd /C hooks, gate commands) would otherwise open its own
// visible console window. HideConsole marks the command so no window appears;
// on non-Windows platforms it is a no-op.
package proc

import "os/exec"

// HideConsole prevents the command from opening a console window on Windows.
// Call it after exec.Command and before cmd.Start.
func HideConsole(cmd *exec.Cmd) {
	hideConsole(cmd)
}

// Job groups a spawned process with all its descendants so the whole tree
// can be terminated at once. On Windows it is a Job Object with
// KILL_ON_JOB_CLOSE; on other platforms it is a no-op (process groups are
// handled by context cancellation there).
type Job struct {
	impl *job
}

// NewJob creates a process-tree group. A nil-impl Job (on error or non-Windows)
// is safe to use: Assign and Close become no-ops.
func NewJob() (*Job, error) {
	impl, err := newJob()
	if err != nil {
		return &Job{}, err
	}
	return &Job{impl: impl}, nil
}

// Assign adds the process (by pid) and its future descendants to the group.
func (g *Job) Assign(pid int) error {
	if g == nil || g.impl == nil {
		return nil
	}
	return g.impl.assign(pid)
}

// Close terminates every process remaining in the group and releases it.
func (g *Job) Close() error {
	if g == nil || g.impl == nil {
		return nil
	}
	return g.impl.close()
}
