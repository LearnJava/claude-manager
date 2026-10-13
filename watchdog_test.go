package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// The launcher's relaunch loop is the last line of defence when the manager
// dies: every session is tied to the manager's Job Object and goes with it,
// so whether the app comes back decides whether a crash costs a minute or a
// day's work. Nothing else exercises a .bat file, so it is driven here with
// a stand-in binary.

const stubSource = `package main

// Stand-in for claude-manager.exe, driven by the launcher under test.
// It records each launch, optionally leaves the clean-exit marker the real
// App.markCleanExit writes, and exits with a configurable code.

import (
	"os"
	"path/filepath"
	"strconv"
)

func main() {
	if runs := os.Getenv("CM_STUB_RUNS"); runs != "" {
		f, err := os.OpenFile(runs, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString("run\n")
			_ = f.Close()
		}
	}
	if marker := os.Getenv("CM_STUB_MARKER"); marker != "" {
		_ = os.MkdirAll(filepath.Dir(marker), 0o755)
		_ = os.WriteFile(marker, []byte("quit\n"), 0o644)
	}
	code, _ := strconv.Atoi(os.Getenv("CM_STUB_EXIT"))
	os.Exit(code)
}
`

var (
	stubOnce sync.Once
	stubPath string
	stubErr  error
)

// buildStub compiles the stand-in binary once per test run. A real .exe
// matters here: cmd transfers control to a .bat without returning unless it
// is invoked with `call`, which would make the launcher look broken for a
// reason the real app never hits.
func buildStub(t *testing.T) string {
	t.Helper()
	stubOnce.Do(func() {
		dir, err := os.MkdirTemp("", "cm-stub")
		if err != nil {
			stubErr = err
			return
		}
		src := filepath.Join(dir, "main.go")
		if err := os.WriteFile(src, []byte(stubSource), 0o644); err != nil {
			stubErr = err
			return
		}
		out := filepath.Join(dir, "stub.exe")
		build := exec.Command("go", "build", "-o", out, src)
		if combined, err := build.CombinedOutput(); err != nil {
			stubErr = err
			t.Logf("stub build output: %s", combined)
			return
		}
		stubPath = out
	})
	if stubErr != nil {
		t.Fatalf("build stub: %v", stubErr)
	}
	return stubPath
}

// stubLauncher copies run-claude-manager.bat into dir, pointing it at the
// stub executable and a private state/log directory, and shortening the
// restart budget so the test does not take a minute.
func stubLauncher(t *testing.T, dir, stub, maxRestarts string) string {
	t.Helper()

	src, err := os.ReadFile("run-claude-manager.bat")
	if err != nil {
		t.Fatalf("read launcher: %v", err)
	}
	script := string(src)

	replacements := [][2]string{
		{`set "EXE=%~dp0build\bin\claude-manager.exe"`, `set "EXE=` + stub + `"`},
		{`set "LOGDIR=%USERPROFILE%\.claude-manager\logs"`, `set "LOGDIR=` + filepath.Join(dir, "logs") + `"`},
		{`set "MARKER=%USERPROFILE%\.claude-manager\state\clean-exit"`, `set "MARKER=` + filepath.Join(dir, "clean-exit") + `"`},
		{`set "MAX_RESTARTS=10"`, `set "MAX_RESTARTS=` + maxRestarts + `"`},
		{`ping -n 6 127.0.0.1`, `ping -n 2 127.0.0.1`},
	}
	for _, r := range replacements {
		if !strings.Contains(script, r[0]) {
			t.Fatalf("launcher no longer contains %q — update this test alongside it", r[0])
		}
		script = strings.Replace(script, r[0], r[1], 1)
	}

	path := filepath.Join(dir, "launcher.bat")
	if err := os.WriteFile(path, []byte(script), 0o644); err != nil {
		t.Fatalf("write launcher: %v", err)
	}
	return path
}

func countRuns(t *testing.T, runsFile string) int {
	t.Helper()
	data, err := os.ReadFile(runsFile)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		t.Fatalf("read runs: %v", err)
	}
	return len(strings.Fields(string(data)))
}

func runLauncher(t *testing.T, launcher string, stubEnv []string) int {
	t.Helper()
	cmd := exec.Command("cmd.exe", "/c", launcher)
	cmd.Dir = filepath.Dir(launcher)
	cmd.Env = append(os.Environ(), stubEnv...)
	err := cmd.Run()
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	t.Fatalf("run launcher: %v", err)
	return -1
}

func requireWindows(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("run-claude-manager.bat is the Windows launcher")
	}
	if testing.Short() {
		t.Skip("launcher test waits on restart backoff")
	}
}

func TestLauncherRestartsAfterUnexpectedExit(t *testing.T) {
	requireWindows(t)

	dir := t.TempDir()
	runs := filepath.Join(dir, "runs.txt")
	launcher := stubLauncher(t, dir, buildStub(t), "2")

	code := runLauncher(t, launcher, []string{
		"CM_STUB_RUNS=" + runs,
		"CM_STUB_MARKER=",
		"CM_STUB_EXIT=1",
	})
	if code != 1 {
		t.Fatalf("launcher exit code = %d, want 1 after exhausting restarts", code)
	}

	// One initial run plus MAX_RESTARTS retries, then it gives up.
	if got, want := countRuns(t, runs), 3; got != want {
		t.Fatalf("app started %d times, want %d", got, want)
	}
}

func TestLauncherStopsOnCleanExit(t *testing.T) {
	requireWindows(t)

	dir := t.TempDir()
	runs := filepath.Join(dir, "runs.txt")
	launcher := stubLauncher(t, dir, buildStub(t), "2")

	code := runLauncher(t, launcher, []string{
		"CM_STUB_RUNS=" + runs,
		// Stand in for App.markCleanExit: a deliberate shutdown leaves this.
		"CM_STUB_MARKER=" + filepath.Join(dir, "clean-exit"),
		"CM_STUB_EXIT=0",
	})
	if code != 0 {
		t.Fatalf("launcher exit code = %d, want 0 after a clean exit", code)
	}

	if got := countRuns(t, runs); got != 1 {
		t.Fatalf("app started %d times, want 1 — a deliberate quit must not restart", got)
	}
}

// A kill leaves no marker, so the watchdog must treat a zero exit code the
// same as a crash: taskkill /F reports success, and the app can exit 0 when
// its window process dies under it.
func TestLauncherRestartsOnZeroExitWithoutMarker(t *testing.T) {
	requireWindows(t)

	dir := t.TempDir()
	runs := filepath.Join(dir, "runs.txt")
	launcher := stubLauncher(t, dir, buildStub(t), "1")

	code := runLauncher(t, launcher, []string{
		"CM_STUB_RUNS=" + runs,
		"CM_STUB_MARKER=",
		"CM_STUB_EXIT=0",
	})
	if code != 1 {
		t.Fatalf("launcher exit code = %d, want 1 after exhausting restarts", code)
	}

	if got, want := countRuns(t, runs), 2; got != want {
		t.Fatalf("app started %d times, want %d", got, want)
	}
}

// A marker left over from an earlier quit must not be mistaken for this
// run's: the loop clears it before each launch.
func TestLauncherIgnoresStaleCleanExitMarker(t *testing.T) {
	requireWindows(t)

	dir := t.TempDir()
	runs := filepath.Join(dir, "runs.txt")
	marker := filepath.Join(dir, "clean-exit")
	if err := os.WriteFile(marker, []byte("stale\n"), 0o644); err != nil {
		t.Fatalf("seed stale marker: %v", err)
	}
	launcher := stubLauncher(t, dir, buildStub(t), "1")

	code := runLauncher(t, launcher, []string{
		"CM_STUB_RUNS=" + runs,
		"CM_STUB_MARKER=",
		"CM_STUB_EXIT=1",
	})
	if code != 1 {
		t.Fatalf("launcher exit code = %d, want 1 — stale marker must not count as a clean exit", code)
	}

	if got, want := countRuns(t, runs), 2; got != want {
		t.Fatalf("app started %d times, want %d", got, want)
	}
}
