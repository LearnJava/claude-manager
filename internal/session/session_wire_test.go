package session

import (
	"encoding/json"
	"testing"

	"claude-manager/internal/config"
)

// TestUserMessage_Envelope locks in the exact stream-json input envelope the
// real Claude CLI expects: {"type":"user","message":{"role":"user","content":...}}.
// The legacy {"type":"user_message","message":"..."} shape made the CLI hang.
func TestUserMessage_Envelope(t *testing.T) {
	data, err := json.Marshal(userMessage("do the thing"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["type"] != "user" {
		t.Errorf("type = %v, want \"user\"", got["type"])
	}
	msg, ok := got["message"].(map[string]any)
	if !ok {
		t.Fatalf("message is not a nested object: %T", got["message"])
	}
	if msg["role"] != "user" {
		t.Errorf("message.role = %v, want \"user\"", msg["role"])
	}
	if msg["content"] != "do the thing" {
		t.Errorf("message.content = %v, want \"do the thing\"", msg["content"])
	}
}

// TestUserMessageWithImages_ContentBlocks verifies an image attachment
// produces an Anthropic content-block array (image block + trailing text
// block), not the plain-string shape used when there are no images.
func TestUserMessageWithImages_ContentBlocks(t *testing.T) {
	data, err := json.Marshal(userMessageWithImages("what is this?", []ImageAttachment{
		{MediaType: "image/png", DataBase64: "Zm9v"},
	}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	msg := got["message"].(map[string]any)
	blocks, ok := msg["content"].([]any)
	if !ok {
		t.Fatalf("content is not a block array: %T", msg["content"])
	}
	if len(blocks) != 2 {
		t.Fatalf("expected 2 content blocks, got %d: %v", len(blocks), blocks)
	}
	img := blocks[0].(map[string]any)
	if img["type"] != "image" {
		t.Errorf("blocks[0].type = %v, want \"image\"", img["type"])
	}
	src := img["source"].(map[string]any)
	if src["type"] != "base64" || src["media_type"] != "image/png" || src["data"] != "Zm9v" {
		t.Errorf("unexpected image source: %v", src)
	}
	text := blocks[1].(map[string]any)
	if text["type"] != "text" || text["text"] != "what is this?" {
		t.Errorf("unexpected text block: %v", text)
	}
}

// TestUserMessageWithImages_NoImagesFallsBackToPlainString verifies the
// zero-images call site produces the exact same plain-string envelope as
// userMessage, so callers without attachments see no behavior change.
func TestUserMessageWithImages_NoImagesFallsBackToPlainString(t *testing.T) {
	got, err := json.Marshal(userMessageWithImages("hi", nil))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want, err := json.Marshal(userMessage("hi"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("userMessageWithImages(no images) = %s, want %s", got, want)
	}
}

// TestBuildCLIArgs_WorktreeNameExplicit verifies an explicit WorktreeName is
// passed as the --worktree argument so branches aren't randomly named.
func TestBuildCLIArgs_WorktreeNameExplicit(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{
			Name:         "P4",
			UseWorktree:  true,
			WorktreeName: "p4-feature",
		},
	})
	if v := findFlag(s.buildCLIArgs(false), "--worktree"); v != "p4-feature" {
		t.Errorf("--worktree = %q, want \"p4-feature\"", v)
	}
}

// TestBuildCLIArgs_WorktreeBareWhenNameUnset verifies that without an explicit
// WorktreeName the flag stays bare (no value), so the CLI creates a fresh
// worktree from HEAD each run — even when the session has a name. A fixed name
// would reuse a stale worktree across the autonomous restart loop.
func TestBuildCLIArgs_WorktreeBareWhenNameUnset(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{
			Name:        "P4",
			UseWorktree: true,
		},
	})
	args := s.buildCLIArgs(false)
	if !hasFlag(args, "--worktree") {
		t.Fatal("missing --worktree")
	}
	if v := findFlag(args, "--worktree"); v != "" && v[0] != '-' {
		t.Errorf("--worktree should be bare when WorktreeName unset, got value %q", v)
	}
}

// TestBuildCLIArgs_WorktreeBareWhenNoName keeps the bare flag when neither a
// worktree name nor a session name is available.
func TestBuildCLIArgs_WorktreeBareWhenNoName(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{UseWorktree: true},
	})
	args := s.buildCLIArgs(false)
	if !hasFlag(args, "--worktree") {
		t.Fatal("missing --worktree")
	}
	// The token after --worktree must not be a value (it should be the end of
	// args or another flag).
	if v := findFlag(args, "--worktree"); v != "" && v[0] != '-' {
		t.Errorf("--worktree unexpectedly has value %q", v)
	}
}

// TestRateLimit_ThrottledClassification locks in which statuses count as an
// actual throttle. The real CLI emits status "allowed"/"allowed_warning" on
// healthy sessions, which must NOT abort the run.
func TestRateLimit_ThrottledClassification(t *testing.T) {
	cases := map[string]bool{
		"":                false,
		"allowed":         false,
		"allowed_warning": false,
		"ok":              false,
		"within_limit":    false,
		"rejected":        true,
		"exceeded":        true,
		"throttled":       true,
	}
	for status, want := range cases {
		info := &RateLimitInfo{Status: status}
		if got := info.throttled(); got != want {
			t.Errorf("throttled(status=%q) = %v, want %v", status, got, want)
		}
	}
}

// TestOnRateLimit_InformationalAllowedDoesNotAbort verifies the per-session
// informational rate_limit_event (status "allowed", no utilization) neither
// marks the session rate-limited nor emits a bogus event.
func TestOnRateLimit_InformationalAllowedDoesNotAbort(t *testing.T) {
	var emitted int
	s := New(Params{
		Config:  config.SessionConfig{Name: "P1"},
		OnEvent: func(_ string, ev SessionEvent) { emitted++ },
	})
	s.onRateLimit(&RateLimitInfo{Status: "allowed", ResetsAt: 1783201800})
	if s.rateLimited.Load() {
		t.Error("informational allowed event marked session rate-limited")
	}
	if emitted != 0 {
		t.Errorf("informational allowed event emitted %d events, want 0", emitted)
	}
}

// TestOnRateLimit_WarningEmitsButDoesNotAbort verifies an approaching-limit
// warning is surfaced to the UI but does not abort the run.
func TestOnRateLimit_WarningEmitsButDoesNotAbort(t *testing.T) {
	var emitted int
	s := New(Params{
		Config:  config.SessionConfig{Name: "P1"},
		OnEvent: func(_ string, ev SessionEvent) { emitted++ },
	})
	s.onRateLimit(&RateLimitInfo{Status: "allowed_warning", Utilization: 0.9})
	if s.rateLimited.Load() {
		t.Error("warning event marked session rate-limited")
	}
	if emitted != 1 {
		t.Errorf("warning event emitted %d events, want 1", emitted)
	}
}

// TestOnRateLimit_RejectedAborts verifies a real throttle aborts the run.
func TestOnRateLimit_RejectedAborts(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "P1"}})
	s.onRateLimit(&RateLimitInfo{Status: "rejected", ResetsAt: 1783201800})
	if !s.rateLimited.Load() {
		t.Error("rejected event did not mark session rate-limited")
	}
}

// TestHandleLine_ResultReportsTurnComplete verifies handleLine returns true only
// for a result event (the signal used to close stdin so the CLI exits).
func TestHandleLine_ResultReportsTurnComplete(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "P1"}})

	if s.handleLine(`{"type":"assistant","message":{"content":[{"type":"text","text":"working"}]}}`, false) {
		t.Error("assistant line reported turn complete")
	}
	if !s.handleLine(`{"type":"result","subtype":"success","total_cost_usd":0.01}`, false) {
		t.Error("result line did not report turn complete")
	}
}
