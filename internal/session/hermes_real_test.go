package session

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// TestHermesRuntime_RealHermes drives the real `hermes` CLI through two
// turns of an interactive session: the second turn must resume the first
// one's conversation and remember what it said. Costs real tokens, so it
// only runs with CM_REAL_HERMES=1; CM_REAL_HERMES_MODEL picks the model
// (default claude-haiku-4-5).
func TestHermesRuntime_RealHermes(t *testing.T) {
	if os.Getenv("CM_REAL_HERMES") != "1" {
		t.Skip("set CM_REAL_HERMES=1 to run against the real hermes CLI")
	}
	bin, err := exec.LookPath("hermes")
	if err != nil {
		t.Skip("hermes not in PATH")
	}
	model := os.Getenv("CM_REAL_HERMES_MODEL")
	if model == "" {
		model = "claude-haiku-4-5"
	}

	results := make(chan string, 4)
	var sawTool, sawInit bool
	s := New(Params{
		ID: "real/H", ProjectName: "real", ProjectPath: t.TempDir(), HermesPath: bin,
		Config: config.SessionConfig{
			Name: "H", Runtime: "hermes", Model: model, Effort: "low",
			Prompt: "Run the shell command `echo cm-marker-731` and then reply with exactly the word it printed.",
		},
		OnEvent: func(_ string, ev SessionEvent) {
			switch ev.Type {
			case EvtInit:
				sawInit = true
			case EvtLog:
				if ev.Entry.Level == "tool" {
					sawTool = true
				}
			case EvtResult:
				results <- ev.Result.ResultText
			}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	first := <-results
	if !strings.Contains(first, "cm-marker-731") || !sawInit || !sawTool {
		t.Fatalf("first turn: result=%q init=%v tool=%v", first, sawInit, sawTool)
	}
	conv := s.CLISessionID
	if err := s.SendMessage("What exact word did you reply with in your previous message? Reply with only that word."); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	second := <-results
	if !strings.Contains(second, "cm-marker-731") {
		t.Fatalf("second turn did not remember the first: %q", second)
	}
	if s.CLISessionID != conv {
		t.Errorf("conversation id changed across turns: %q -> %q", conv, s.CLISessionID)
	}
	s.Stop(false)
	<-done
}
