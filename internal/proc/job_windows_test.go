//go:build windows

package proc

import (
	"os/exec"
	"testing"
	"time"
)

// A job with KILL_ON_JOB_CLOSE must terminate the assigned process (and its
// descendants) as soon as the job handle is closed.
func TestJobCloseKillsProcessTree(t *testing.T) {
	// cmd.exe spawns ping as a child — closing the job must reap both.
	cmd := exec.Command("cmd", "/C", "ping -n 30 127.0.0.1 >nul")
	HideConsole(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}

	j, err := NewJob()
	if err != nil {
		t.Fatalf("NewJob: %v", err)
	}
	if err := j.Assign(cmd.Process.Pid); err != nil {
		t.Fatalf("Assign: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait() // exit error expected: the tree was terminated
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("process survived job close")
	}
}

// A zero-value / failed Job must be safe to use.
func TestJobNilSafe(t *testing.T) {
	var j *Job
	if err := j.Assign(1234); err != nil {
		t.Errorf("nil Job Assign: %v", err)
	}
	if err := j.Close(); err != nil {
		t.Errorf("nil Job Close: %v", err)
	}
	empty := &Job{}
	if err := empty.Assign(1234); err != nil {
		t.Errorf("empty Job Assign: %v", err)
	}
	if err := empty.Close(); err != nil {
		t.Errorf("empty Job Close: %v", err)
	}
}
