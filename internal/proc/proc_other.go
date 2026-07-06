//go:build !windows

package proc

import "os/exec"

func hideConsole(_ *exec.Cmd) {}

// job is a stub on non-Windows platforms.
type job struct{}

func newJob() (*job, error)      { return nil, nil }
func (j *job) assign(_ int) error { return nil }
func (j *job) close() error       { return nil }
