package session

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func envValue(env []string, name string) (string, int) {
	var value string
	count := 0
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok && strings.EqualFold(k, name) {
			value = v
			count++
		}
	}
	return value, count
}

func TestSessionEnvAdvertisesManagerIdentity(t *testing.T) {
	env := sessionEnv()

	pid, n := envValue(env, EnvManagerPID)
	if n != 1 {
		t.Fatalf("%s appears %d times, want exactly 1", EnvManagerPID, n)
	}
	if pid != strconv.Itoa(os.Getpid()) {
		t.Fatalf("%s = %q, want %d", EnvManagerPID, pid, os.Getpid())
	}

	exe, n := envValue(env, EnvManagerExe)
	if n > 1 {
		t.Fatalf("%s appears %d times, want at most 1", EnvManagerExe, n)
	}
	if n == 1 && exe == "" {
		t.Fatalf("%s present but empty", EnvManagerExe)
	}
}

// A manager launched from inside a session inherits that session's variables.
// Passing the outer manager's PID through would point the guard at a process
// that no longer owns anything, so inherited values must be replaced.
func TestSessionEnvOverridesInheritedIdentity(t *testing.T) {
	t.Setenv(EnvManagerPID, "999999")
	t.Setenv(EnvManagerExe, "C:/stale/claude-manager.exe")

	env := sessionEnv()

	pid, n := envValue(env, EnvManagerPID)
	if n != 1 {
		t.Fatalf("%s appears %d times, want exactly 1", EnvManagerPID, n)
	}
	if pid == "999999" {
		t.Fatalf("%s kept the inherited value %q", EnvManagerPID, pid)
	}

	exe, n := envValue(env, EnvManagerExe)
	if n > 1 {
		t.Fatalf("%s appears %d times, want at most 1", EnvManagerExe, n)
	}
	if exe == "C:/stale/claude-manager.exe" {
		t.Fatalf("%s kept the inherited value", EnvManagerExe)
	}
}

func TestSessionEnvPreservesParentEnvironment(t *testing.T) {
	t.Setenv("CM_GUARD_PROBE", "kept")

	if v, n := envValue(sessionEnv(), "CM_GUARD_PROBE"); n != 1 || v != "kept" {
		t.Fatalf("parent variable lost: value=%q count=%d", v, n)
	}
}

func TestWithSessionIDReplacesInherited(t *testing.T) {
	env := withSessionID([]string{"A=1", "cm_session_id=outer/X"}, "lumen/P1")
	v, n := envValue(env, EnvSessionID)
	if n != 1 || v != "lumen/P1" {
		t.Fatalf("%s = %q (%d times), want lumen/P1 once", EnvSessionID, v, n)
	}
}
