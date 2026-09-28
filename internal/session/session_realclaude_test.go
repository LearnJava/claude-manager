package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

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

// TestRealClaude_AskUserQuestionUnavailable is RT-02: drives a real `claude
// -p` process (not through Session/runOnce — an autonomous runOnce injects
// askUserProtocolPrompt, which teaches the model the ```ask-user marker
// fallback and would confuse "the model used our own marker" with "the model
// used the real AskUserQuestion tool"; this test isolates the raw CLI
// tool-availability question, exactly like the recording that produced
// testdata/claude-stream/ask-user-question.jsonl) and re-confirms the finding
// documented in docs/runtimes.md §"AskUserQuestion у Claude Code" (v2.1.280):
// in headless `-p` mode the tool is not offered to the model at all (no
// tool_use, no permission_request) — the model just says so in plain text and
// the turn ends normally (`stop_reason":"end_turn"`, subtype "success"). This
// is a regression/documentation test, not a feature test: if a future Claude
// Code version ships AskUserQuestion in `-p` mode, the assertions below start
// failing and RT-03 needs real handling for it.
//
//	CM_REAL_CLAUDE=1 go test ./internal/session/ -run TestRealClaude_AskUserQuestion -v
func TestRealClaude_AskUserQuestionUnavailable(t *testing.T) {
	if os.Getenv("CM_REAL_CLAUDE") != "1" {
		t.Skip("set CM_REAL_CLAUDE=1 to run the real-CLI integration test")
	}

	dir := t.TempDir()
	sessionID := uuid.NewString()
	inputLine, err := json.Marshal(userMessage(
		"Ask me a question using the AskUserQuestion tool with two options: red or blue. Do it now, nothing else.",
	))
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "claude",
		"-p", "--verbose",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--replay-user-messages",
		"--session-id", sessionID,
		"--model", "haiku",
		"--permission-mode", "bypassPermissions",
	)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(string(inputLine) + "\n")
	out, runErr := cmd.Output()
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			t.Fatalf("claude exited: %v, stderr: %s", runErr, ee.Stderr)
		}
		t.Fatalf("claude exited: %v", runErr)
	}

	var (
		sawInit       bool
		sawPermission bool
		sawAskUserUse bool
		result        *SessionResult
	)
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		ev := ParseLine(line)
		if ev.EventType == EventInit {
			sawInit = true
		}
		if ev.EventType == EventPermission {
			sawPermission = true
		}
		for _, e := range ev.Entries {
			if e.Level == "tool" && strings.Contains(e.ToolName, "AskUserQuestion") {
				sawAskUserUse = true
			}
		}
		if ev.Result != nil {
			result = ev.Result
		}
	}

	if !sawInit {
		t.Error("no init event — CLI never started the session")
	}
	if result == nil {
		t.Fatal("no result event — turn did not complete")
	}
	if sawAskUserUse {
		t.Error("saw an AskUserQuestion tool_use — the finding that the tool is " +
			"unavailable in -p mode no longer holds, RT-03 needs real handling")
	}
	if sawPermission {
		t.Error("saw a permission_request — Claude apparently now prompts for " +
			"AskUserQuestion in -p mode, RT-03 needs real handling")
	}
	if result.Subtype != "" && result.Subtype != "success" {
		t.Errorf("result.subtype = %q, want \"success\"", result.Subtype)
	}
	t.Logf("real claude AskUserQuestion probe: result=%q", result.ResultText)
}

// parseClaudeFile runs a real `claude -p --output-format stream-json`
// recording (testdata/claude-stream/) through ParseLine, mirroring
// parseHermesFile in hermes_test.go.
func parseClaudeFile(t *testing.T, name string) []ParsedEvent {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "claude-stream", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var out []ParsedEvent
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		out = append(out, ParseLine(line))
	}
	return out
}

// TestParseClaudeStream_AskUserQuestionUnavailable replays the RT-02
// recording offline (no real CLI, no API cost — this is the test the normal
// gate runs) and checks the same claims the opt-in test above verifies live:
// no AskUserQuestion tool_use, no permission_request, the turn ends with
// stop_reason "end_turn" and is_error:false.
func TestParseClaudeStream_AskUserQuestionUnavailable(t *testing.T) {
	evs := parseClaudeFile(t, "ask-user-question.jsonl")
	if len(evs) == 0 {
		t.Fatal("no events parsed from fixture")
	}

	var (
		sawInit       bool
		sawPermission bool
		sawAskUserUse bool
		result        *SessionResult
	)
	for _, ev := range evs {
		if ev.EventType == EventInit {
			sawInit = true
		}
		if ev.EventType == EventPermission {
			sawPermission = true
		}
		for _, e := range ev.Entries {
			if e.Level == "tool" && strings.Contains(e.ToolName, "AskUserQuestion") {
				sawAskUserUse = true
			}
		}
		if ev.Result != nil {
			result = ev.Result
		}
	}

	if !sawInit {
		t.Error("no EventInit in fixture")
	}
	if sawPermission {
		t.Error("fixture has a permission_request — recording doesn't match the documented finding")
	}
	if sawAskUserUse {
		t.Error("fixture has an AskUserQuestion tool_use — recording doesn't match the documented finding")
	}
	if result == nil {
		t.Fatal("no result event in fixture")
	}
	if result.Subtype != "" && result.Subtype != "success" {
		t.Errorf("result.subtype = %q, want \"success\" (turn should end cleanly): %+v", result.Subtype, result)
	}
	if !strings.Contains(strings.ToLower(result.ResultText), "askuserquestion") {
		t.Errorf("result text = %q, want it to mention AskUserQuestion (model explaining it can't use it)", result.ResultText)
	}
}
