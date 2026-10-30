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

// TestKimiRuntime_RealKimi drives the real `kimi` CLI through the MVP
// scenario: start a session, stream events, send a second message, stop,
// then resume by session id and check the conversation is remembered.
// Costs real quota, so it only runs with CM_REAL_KIMI=1;
// CM_REAL_KIMI_MODEL picks the model (default: the CLI's own default).
func TestKimiRuntime_RealKimi(t *testing.T) {
	if os.Getenv("CM_REAL_KIMI") != "1" {
		t.Skip("set CM_REAL_KIMI=1 to run against the real kimi CLI")
	}
	bin, err := exec.LookPath("kimi")
	if err != nil {
		t.Skip("kimi not in PATH")
	}
	model := os.Getenv("CM_REAL_KIMI_MODEL")

	var sawInit, sawText bool
	var conv string
	results := make(chan string, 8)
	turnDone := make(chan struct{}, 4)
	projectDir := t.TempDir() // the SAME cwd for the original and the resumed session
	s := New(Params{
		ID: "real/K", ProjectName: "real", ProjectPath: projectDir, KimiPath: bin,
		Config: config.SessionConfig{
			Name: "K", Runtime: "kimi", Model: model,
			Prompt: "Reply with exactly the word cm-marker-731 and nothing else.",
		},
		OnEvent: func(_ string, ev SessionEvent) {
			switch ev.Type {
			case EvtInit:
				sawInit = true
				conv = ev.Init.SessionID
			case EvtLog:
				if ev.Entry.Level == "text" {
					sawText = true
					results <- ev.Entry.Message
				}
			case EvtResult:
				turnDone <- struct{}{}
			}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()

	first := <-results
	<-turnDone // the resume_hint arrives after the text; wait for the turn's end
	if !strings.Contains(first, "cm-marker-731") || !sawInit || !sawText {
		t.Fatalf("first turn: result=%q init=%v text=%v", first, sawInit, sawText)
	}
	if conv == "" {
		t.Fatal("no conversation id captured from the wire (session.resume_hint)")
	}
	if err := s.SendMessage("What exact word did you reply with in your previous message? Reply with only that word."); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	second := <-results
	<-turnDone
	if !strings.Contains(second, "cm-marker-731") {
		t.Fatalf("second turn did not remember the first: %q", second)
	}
	s.Stop(false)
	<-done

	// Resume the same conversation in a fresh Session: it must still remember.
	results2 := make(chan string, 4)
	turnDone2 := make(chan struct{}, 2)
	s2 := New(Params{
		ID: "real/K2", ProjectName: "real", ProjectPath: projectDir, KimiPath: bin,
		ResumeSessionID: conv,
		Config: config.SessionConfig{
			Name: "K2", Runtime: "kimi", Model: model,
			Prompt: "What exact word did this conversation ask you to reply with? Reply with only that word.",
		},
		OnEvent: func(_ string, ev SessionEvent) {
			if ev.Type == EvtLog && ev.Entry.Level == "text" {
				results2 <- ev.Entry.Message
			}
			if ev.Type == EvtResult {
				turnDone2 <- struct{}{}
			}
		},
	})
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel2()
	done2 := make(chan struct{})
	go func() { s2.Run(ctx2); close(done2) }()
	// An interactive resume waits for the next user turn instead of starting
	// one on its own (same as Claude): drive it with an explicit message,
	// after the Run loop has actually come up (it starts as idle).
	for i := 0; i < 250; i++ {
		if st := s2.Status(); st != config.StatusIdle {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := s2.SendMessage("What exact word did this conversation ask you to reply with? Reply with only that word."); err != nil {
		t.Fatalf("SendMessage on resume: %v", err)
	}
	third := <-results2
	<-turnDone2
	if !strings.Contains(third, "cm-marker-731") {
		t.Fatalf("resume did not remember the conversation: %q", third)
	}
	s2.Stop(false)
	<-done2
}
