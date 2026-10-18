package session

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// A CLI that outlives its finished autonomous turn (`claude -p` waits for a
// run_in_background command, then continues on its own) is busy, not hung:
// the session log explains the wait after turnExitNotice, the process is
// left alone so its later output still arrives, and the run ends normally.
func TestRun_LingeringCLIIsExplainedNotKilled(t *testing.T) {
	claudePath := buildFakeclaudeForSession(t)
	scenarioDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "scenarios"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	t.Setenv("FAKECLAUDE_SCENARIO", scenarioDir)

	old := turnExitNotice
	turnExitNotice = 300 * time.Millisecond
	defer func() { turnExitNotice = old }()
	// A plain result (no done marker) is never force-killed, however short
	// the grace is.
	oldG := turnExitGrace
	turnExitGrace = 50 * time.Millisecond
	defer func() { turnExitGrace = oldG }()

	var (
		taskDone, errs int
		noticed, alive bool
	)
	s := New(Params{
		ID:          "it/IT",
		ProjectName: "it",
		ProjectPath: t.TempDir(),
		ClaudePath:  claudePath,
		Config: config.SessionConfig{
			Name:           "IT",
			Model:          "sonnet",
			PermissionMode: "bypassPermissions",
			AutoRestart:    true,
			MaxTasks:       1,
			Prompt:         "linger-after-result",
		},
		OnEvent: func(_ string, ev SessionEvent) {
			switch ev.Type {
			case EvtTaskDone:
				taskDone++
			case EvtError:
				errs++
			case EvtLog:
				if ev.Entry == nil {
					return
				}
				if strings.Contains(ev.Entry.Message, "the CLI is still running") {
					noticed = true
				}
				if strings.Contains(ev.Entry.Message, "still alive") {
					alive = true
				}
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.Run(ctx)

	if !noticed {
		t.Error("no session-log notice while the CLI outlived its turn")
	}
	if !alive {
		t.Error("output the CLI wrote after its turn never arrived — the process was cut off")
	}
	if taskDone != 1 {
		t.Errorf("EvtTaskDone count = %d, want 1", taskDone)
	}
	if errs != 0 {
		t.Errorf("got %d error event(s), want none", errs)
	}
}

// After the done marker the task is closed: a CLI that still lingers (a
// background command) is terminated once turnExitNotice+turnExitGrace pass,
// and the run ends as a normal finish so the next queued task can start.
func TestRun_LingeringCLIAfterDoneMarkerIsForceKilled(t *testing.T) {
	claudePath := buildFakeclaudeForSession(t)
	scenarioDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "scenarios"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	t.Setenv("FAKECLAUDE_SCENARIO", scenarioDir)

	oldN, oldG := turnExitNotice, turnExitGrace
	turnExitNotice, turnExitGrace = 50*time.Millisecond, 100*time.Millisecond
	defer func() { turnExitNotice, turnExitGrace = oldN, oldG }()

	var (
		taskDone, errs int
		forced, alive  bool
	)
	s := New(Params{
		ID:          "it/IT",
		ProjectName: "it",
		ProjectPath: t.TempDir(),
		ClaudePath:  claudePath,
		Config: config.SessionConfig{
			Name:           "IT",
			Model:          "sonnet",
			PermissionMode: "bypassPermissions",
			AutoRestart:    true,
			MaxTasks:       1,
			Prompt:         "linger-after-marker",
		},
		OnEvent: func(_ string, ev SessionEvent) {
			switch ev.Type {
			case EvtTaskDone:
				taskDone++
			case EvtError:
				errs++
			case EvtLog:
				if ev.Entry == nil {
					return
				}
				if strings.Contains(ev.Entry.Message, "terminating it") {
					forced = true
				}
				if strings.Contains(ev.Entry.Message, "still alive") {
					alive = true
				}
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	s.Run(ctx)

	if !forced {
		t.Error("the lingering CLI was not terminated after the done marker")
	}
	if alive {
		t.Error("the CLI kept running to its late output — it was not killed")
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("run took %s — it waited for the CLI instead of killing it", time.Since(start))
	}
	if taskDone != 1 {
		t.Errorf("EvtTaskDone count = %d, want 1", taskDone)
	}
	if errs != 0 {
		t.Errorf("got %d error event(s), want none", errs)
	}
}
