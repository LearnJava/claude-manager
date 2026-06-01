package hooks

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunHook_EmptyCommandIsNoop(t *testing.T) {
	res := RunHook("", "")
	if res.Err != nil {
		t.Fatalf("empty command: unexpected error: %v", res.Err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("empty command: exit code = %d, want 0", res.ExitCode)
	}
	if !res.Success() {
		t.Fatalf("empty command: Success() = false, want true")
	}
}

func TestRunHook_Whitespace(t *testing.T) {
	res := RunHook("   \t\n", "")
	if res.Err != nil {
		t.Fatalf("whitespace command: unexpected error: %v", res.Err)
	}
	if !res.Success() {
		t.Fatalf("whitespace command should be treated as no-op")
	}
}

func TestRunHook_SuccessfulCommandCapturesStdout(t *testing.T) {
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "echo hello"
	} else {
		cmd = "echo hello"
	}
	res := RunHook(cmd, "")
	if !res.Success() {
		t.Fatalf("expected success: code=%d err=%v stderr=%q", res.ExitCode, res.Err, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "hello") {
		t.Fatalf("stdout missing 'hello': %q", res.Stdout)
	}
}

func TestRunHook_NonZeroExitCode(t *testing.T) {
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "exit /B 7"
	} else {
		cmd = "exit 7"
	}
	res := RunHook(cmd, "")
	if res.Success() {
		t.Fatalf("expected failure, got success: %+v", res)
	}
	if res.ExitCode != 7 {
		t.Fatalf("exit code = %d, want 7", res.ExitCode)
	}
}

func TestRunHookContext_TimeoutReturnsFailure(t *testing.T) {
	// On Windows, exec.CommandContext kills the cmd.exe wrapper but the
	// child process (e.g. ping) may linger until it exits naturally — so we
	// don't assert on wall-clock duration. What we *can* check is that the
	// command did not "succeed" from the hook's perspective.
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "ping -n 2 127.0.0.1 >NUL"
	} else {
		cmd = "sleep 2"
	}
	res := RunHookContext(context.Background(), cmd, "", 100*time.Millisecond)
	if res.Success() {
		t.Fatalf("expected timeout failure, got success: %+v", res)
	}
}

func TestRunHookContext_HonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	var cmd string
	if runtime.GOOS == "windows" {
		cmd = "echo nope"
	} else {
		cmd = "echo nope"
	}
	res := RunHookContext(ctx, cmd, "", 0)
	if res.Success() {
		t.Fatalf("expected failure due to cancelled context, got success")
	}
}
