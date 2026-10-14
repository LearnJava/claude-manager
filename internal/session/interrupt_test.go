package session

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// TestInterrupt_NothingInFlight: a session with no turn running has nothing
// to interrupt — the call fails instead of arming an interrupt that would
// swallow the next turn's result.
func TestInterrupt_NothingInFlight(t *testing.T) {
	s := New(Params{ID: "p/s", ProjectName: "p", Config: config.SessionConfig{Name: "s"}})
	if err := s.Interrupt(); err == nil {
		t.Fatal("Interrupt on an idle session: want error, got nil")
	}
	s.setStatus(config.StatusWorking)
	s.setActivity(Activity{Kind: ActivityIdle})
	if err := s.Interrupt(); err == nil {
		t.Fatal("Interrupt between turns: want error, got nil")
	}
	if s.interruptPending.Load() {
		t.Fatal("a rejected Interrupt left interruptPending armed")
	}
}

// TestInterrupt_AbortsTurnAndWaitsForUser drives a real fakeclaude process
// (testdata/scenarios/interrupt-demo.json) in an autonomous session: the
// interrupt aborts the running tool call, the interrupted turn is neither a
// failure nor a finished task (stdin stays open), the session waits for the
// user, and the next message continues the same process.
func TestInterrupt_AbortsTurnAndWaitsForUser(t *testing.T) {
	claudePath := buildFakeclaudeForSession(t)
	scenarioDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "scenarios"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	t.Setenv("FAKECLAUDE_SCENARIO", scenarioDir)

	var (
		mu       sync.Mutex
		statuses []config.SessionStatus
		logs     []string
		inits    int
		taskDone int
		once     sync.Once
		replied  sync.Once
		errs     = make(chan error, 2)
	)
	var s *Session
	s = New(Params{
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
			Prompt:         "run the interrupt-demo",
		},
		OnEvent: func(_ string, ev SessionEvent) {
			mu.Lock()
			defer mu.Unlock()
			switch ev.Type {
			case EvtInit:
				inits++
			case EvtTaskDone:
				taskDone++
			case EvtLog:
				if ev.Entry != nil {
					logs = append(logs, ev.Entry.Message)
					if ev.Entry.Level == "tool" {
						once.Do(func() { go func() { errs <- s.Interrupt() }() })
					}
				}
			case EvtStatus:
				statuses = append(statuses, ev.Status)
				if ev.Status == config.StatusWaitingForUser {
					replied.Do(func() { go func() { errs <- s.SendMessage("go on") }() })
				}
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.Run(ctx)
	if ctx.Err() != nil {
		t.Fatal("Run did not finish: the interrupted turn or the follow-up message got stuck")
	}
	for i := 0; i < 2; i++ {
		select {
		case err := <-errs:
			if err != nil {
				t.Fatalf("Interrupt/SendMessage: %v", err)
			}
		default:
			t.Fatalf("only %d of Interrupt+SendMessage ran", i)
		}
	}

	mu.Lock()
	defer mu.Unlock()
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "Action interrupted") {
		t.Errorf("no interrupt notice in the log:\n%s", joined)
	}
	if !strings.Contains(joined, "Continuing after the interrupt.") {
		t.Errorf("the message after the interrupt got no reply from the same process:\n%s", joined)
	}
	if inits != 1 {
		t.Errorf("CLI launched %d times, want 1 (the interrupt must not end the process)", inits)
	}
	if taskDone != 1 {
		t.Errorf("EvtTaskDone fired %d times, want 1 (only the turn after the interrupt ends the task)", taskDone)
	}
	sawWait := false
	for _, st := range statuses {
		if st == config.StatusWaitingForUser {
			sawWait = true
		}
		if st == config.StatusError {
			t.Errorf("session went to error: statuses %v", statuses)
		}
	}
	if !sawWait {
		t.Errorf("session never waited for the user: statuses %v", statuses)
	}
}
