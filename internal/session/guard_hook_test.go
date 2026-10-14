package session

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The self-kill guard (.claude/hooks/guard-self-kill.py) and sessionEnv form
// one contract: the manager advertises its identity through CLAUDE_MANAGER_PID
// and the hook refuses shell commands aimed at it. The hook is Python, but the
// project's gate is `go test ./...`, so it is exercised from here rather than
// living in a test runner nothing invokes.

func guardHookPath(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		candidate := filepath.Join(dir, ".claude", "hooks", "guard-self-kill.py")
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("guard-self-kill.py not found in any parent directory")
		}
		dir = parent
	}
}

// envWithout returns env minus the named variables (case-insensitive, as
// Windows treats them).
func envWithout(env []string, names ...string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		drop := false
		for _, n := range names {
			drop = drop || strings.EqualFold(k, n)
		}
		if !drop {
			out = append(out, kv)
		}
	}
	return out
}

func pythonExe(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python", "python3"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("python not available; skipping self-kill guard test")
	return ""
}

// runGuard feeds the hook a PreToolUse payload for command and returns its
// exit code. Exit 2 is the Claude Code convention for "block this tool call".
func runGuard(t *testing.T, python, hook, command string, env []string) int {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"tool_name":  "Bash",
		"tool_input": map[string]string{"command": command},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	cmd := exec.Command(python, hook)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stdout = nil
	cmd.Stderr = nil
	// A stray CLAUDE_MANAGER_PID in the developer's own environment would
	// make these cases non-deterministic, so the manager variables are
	// dropped and each case sets its own. Everything else is kept: the
	// `python` on PATH may be the Python install manager's launcher, which
	// without LOCALAPPDATA finds no installs, tries to install one into its
	// working directory and exits 1. The temp dir keeps any such side files
	// out of the package.
	cmd.Env = append(envWithout(os.Environ(), EnvManagerPID, EnvManagerExe), env...)
	cmd.Dir = t.TempDir()

	if err := cmd.Run(); err != nil {
		var exitErr *exec.ExitError
		if ok := asExitError(err, &exitErr); ok {
			return exitErr.ExitCode()
		}
		t.Fatalf("run guard: %v", err)
	}
	return 0
}

func asExitError(err error, target **exec.ExitError) bool {
	if e, ok := err.(*exec.ExitError); ok {
		*target = e
		return true
	}
	return false
}

func TestGuardSelfKill(t *testing.T) {
	python := pythonExe(t)
	hook := guardHookPath(t)

	const blocked = 2
	const allowed = 0

	// A PID that is not the manager and is very unlikely to exist, so the
	// OS-resolution fallback finds nothing and the case stays deterministic.
	const strangerPID = "424242"
	const managerPID = "31337"

	cases := []struct {
		name    string
		command string
		env     []string
		want    int
	}{
		{
			name:    "kill by image name",
			command: "taskkill /F /IM claude-manager.exe",
			want:    blocked,
		},
		{
			name:    "kill by image name through cmd",
			command: "cmd //c taskkill /F /IM claude-manager.exe",
			want:    blocked,
		},
		{
			name:    "powershell stop by name",
			command: "Get-Process claude-manager | Stop-Process -Force",
			want:    blocked,
		},
		{
			name:    "kill the advertised manager pid",
			command: "taskkill //PID " + managerPID + " //F",
			env:     []string{EnvManagerPID + "=" + managerPID},
			want:    blocked,
		},
		{
			name:    "kill by the advertised executable stem",
			command: "pkill claude-manager",
			env:     []string{EnvManagerExe + "=/opt/claude-manager/claude-manager"},
			want:    blocked,
		},
		{
			name:    "unrelated pid is left alone",
			command: "taskkill //PID " + strangerPID + " //F",
			env:     []string{EnvManagerPID + "=" + managerPID},
			want:    allowed,
		},
		{
			name:    "unrelated process by name",
			command: "pkill node",
			want:    allowed,
		},
		{
			name:    "no kill verb at all",
			command: "git log --oneline -20",
			want:    allowed,
		},
		{
			// Regression: "kill" inside a path used to read as a kill verb,
			// so editing the guard itself from the repo root was blocked.
			name:    "guard's own path is not a kill verb",
			command: "python D:/GolangProjects/claude-manager/.claude/hooks/guard-self-kill.py --check",
			env:     []string{EnvManagerPID + "=" + managerPID},
			want:    allowed,
		},
		{
			name:    "pid-shaped number without a kill verb",
			command: "go test ./... -run " + managerPID,
			env:     []string{EnvManagerPID + "=" + managerPID},
			want:    allowed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runGuard(t, python, hook, tc.command, tc.env)
			if got != tc.want {
				t.Fatalf("exit code = %d, want %d\ncommand: %s", got, tc.want, tc.command)
			}
		})
	}
}

func TestGuardMalformedPayloadIsAllowed(t *testing.T) {
	python := pythonExe(t)
	hook := guardHookPath(t)

	cmd := exec.Command(python, hook)
	cmd.Dir = t.TempDir()
	cmd.Stdin = strings.NewReader("this is not json")
	if err := cmd.Run(); err != nil {
		t.Fatalf("malformed payload must not block: %v", err)
	}
}
