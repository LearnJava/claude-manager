package session

import (
	"os"
	"strconv"
	"strings"
)

// Environment variables every session CLI inherits, identifying the manager
// process that owns it.
//
// These exist so a session can recognise its own host. A session runs as a
// child of claude-manager and is assigned to a Job Object created with
// JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE (internal/proc/proc_windows.go), so
// killing the manager terminates that session and every other session running
// in parallel, instantly and without a clean shutdown. A session that rebuilds
// the app and then kills the running instance to restart it destroys itself
// and all its siblings — see the "self-kill" entry in docs/design-decisions.md.
//
// The guard that acts on this lives in .claude/hooks/guard-self-kill.py. It
// can fall back to resolving PIDs through tasklist, but that only catches a
// kill aimed at a PID; CLAUDE_MANAGER_PID lets it recognise the host with no
// process lookup at all, and works the same under every runtime.
const (
	EnvManagerPID = "CLAUDE_MANAGER_PID"
	EnvManagerExe = "CLAUDE_MANAGER_EXE"
)

// sessionEnv returns the parent environment with the manager identity added.
// Any inherited value of these names is dropped first: when the manager is
// itself launched from a session (a developer session rebuilding and running
// the app), the stale PID of the *outer* manager would otherwise shadow ours
// and point the guard at the wrong process.
func sessionEnv() []string {
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	}

	parent := os.Environ()
	env := make([]string, 0, len(parent)+2)
	for _, kv := range parent {
		name, _, ok := strings.Cut(kv, "=")
		if ok && (strings.EqualFold(name, EnvManagerPID) || strings.EqualFold(name, EnvManagerExe)) {
			continue
		}
		env = append(env, kv)
	}

	env = append(env, EnvManagerPID+"="+strconv.Itoa(os.Getpid()))
	if exe != "" {
		env = append(env, EnvManagerExe+"="+exe)
	}
	return env
}
