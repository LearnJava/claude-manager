package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// runEmptyQueue runs one session over an empty task_source with fakeclaude
// and returns whether the "no pending tasks" fallback fired and how many
// runs ended.
func runEmptyQueue(t *testing.T, cfg config.SessionConfig) (fallback bool, inits int) {
	t.Helper()
	claudePath := buildFakeclaudeForSession(t)
	scenarioDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "scenarios"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	t.Setenv("FAKECLAUDE_SCENARIO", scenarioDir)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "STATUS-P1.md"), []byte("# queue\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg.Name, cfg.Model, cfg.PermissionMode = "E", "sonnet", "bypassPermissions"
	cfg.TaskSource, cfg.Prompt = "STATUS-P1.md", "hello"
	s := New(Params{
		ID: "it/E", ProjectName: "it", ProjectPath: dir, ClaudePath: claudePath, Config: cfg,
		OnEvent: func(_ string, ev SessionEvent) {
			switch ev.Type {
			case EvtInit:
				inits++
			case EvtLog:
				if ev.Entry != nil && strings.Contains(ev.Entry.Message, "No pending tasks") {
					fallback = true
				}
			}
		},
	})
	s.retryDelay = 0
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.Run(ctx)
	return fallback, inits
}

// QV-03: with only auto_restart set, an empty queue ends the loop in a plain
// interactive session instead of relaunching the queue prompt.
func TestRun_EmptyQueueStopsByDefault(t *testing.T) {
	fallback, _ := runEmptyQueue(t, config.SessionConfig{AutoRestart: true})
	if !fallback {
		t.Fatal("empty queue + auto_restart did not fall back to an interactive session")
	}
}

// run_without_tasks restores the old behaviour: no queue check, normal launch.
func TestRun_EmptyQueueRunsWithRunWithoutTasks(t *testing.T) {
	fallback, inits := runEmptyQueue(t, config.SessionConfig{RunWithoutTasks: true})
	if fallback {
		t.Fatal("run_without_tasks must skip the empty-queue fallback")
	}
	if inits == 0 {
		t.Fatal("expected a normal launch with the queue prompt")
	}
}
