package session

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// TestRealClaude_RunOnceCompletes drives ONE real Claude CLI turn through the
// full Session I/O path and asserts it produces a result and exits cleanly
// (i.e. the close-stdin-on-result logic makes the process terminate so the Run
// loop can iterate). It spends a few cents of API budget, so it is gated behind
// CM_REAL_CLAUDE=1 and skipped by default.
//
//	CM_REAL_CLAUDE=1 go test ./internal/session/ -run TestRealClaude -v
func TestRealClaude_RunOnceCompletes(t *testing.T) {
	if os.Getenv("CM_REAL_CLAUDE") != "1" {
		t.Skip("set CM_REAL_CLAUDE=1 to run the real-CLI integration test")
	}

	dir := t.TempDir()

	var (
		mu         sync.Mutex
		gotResult  bool
		gotInit    bool
		assistants int
	)

	s := New(Params{
		ID:          "it/IT",
		ProjectName: "it",
		ProjectPath: dir,
		ClaudePath:  "claude",
		Config: config.SessionConfig{
			Name:            "IT",
			Model:           "haiku",
			PermissionMode:  "bypassPermissions",
			StopWhenNoTasks: true, // autonomous → close stdin on result
			Prompt:          "Reply with exactly the word PONG and nothing else. Do not use any tools.",
		},
		OnEvent: func(_ string, ev SessionEvent) {
			mu.Lock()
			defer mu.Unlock()
			switch ev.Type {
			case EvtInit:
				gotInit = true
			case EvtResult:
				gotResult = true
			case EvtLog:
				if ev.Entry != nil && ev.Entry.Level == "text" {
					assistants++
				}
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	start := time.Now()
	err := s.runOnce(ctx, false)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("runOnce returned error: %v", err)
	}
	if elapsed > 115*time.Second {
		t.Fatalf("runOnce took %s — likely hung on stdin (expected clean exit after result)", elapsed)
	}

	mu.Lock()
	defer mu.Unlock()
	if !gotInit {
		t.Error("no init event — CLI never started the session")
	}
	if !gotResult {
		t.Error("no result event — turn did not complete")
	}
	if assistants == 0 {
		t.Error("no assistant text — model produced no output")
	}
	t.Logf("real claude OK: init=%v result=%v assistant_texts=%d elapsed=%s",
		gotInit, gotResult, assistants, elapsed)
}
