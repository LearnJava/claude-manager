package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// findFlag returns the value following flag in args, or "" if missing.
// "" means flag-with-no-value or flag absent — disambiguate with hasFlag.
func findFlag(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func TestBuildCLIArgs_DefaultsAndCore(t *testing.T) {
	s := New(Params{
		ID:          "lumen/P1",
		ProjectName: "lumen",
		Config: config.SessionConfig{
			Name:           "P1",
			Model:          "sonnet",
			Effort:         "high",
			PermissionMode: "acceptEdits",
		},
	})
	args := s.buildCLIArgs()

	if !hasFlag(args, "-p") {
		t.Error("missing -p")
	}
	if v := findFlag(args, "--input-format"); v != "stream-json" {
		t.Errorf("--input-format=%q", v)
	}
	if v := findFlag(args, "--output-format"); v != "stream-json" {
		t.Errorf("--output-format=%q", v)
	}
	if !hasFlag(args, "--include-partial-messages") {
		t.Error("missing --include-partial-messages")
	}
	if !hasFlag(args, "--replay-user-messages") {
		t.Error("missing --replay-user-messages")
	}
	if v := findFlag(args, "--session-id"); v == "" {
		t.Error("missing --session-id value")
	}
	if v := findFlag(args, "--name"); v != "P1" {
		t.Errorf("--name=%q", v)
	}
	if v := findFlag(args, "--model"); v != "sonnet" {
		t.Errorf("--model=%q", v)
	}
	if v := findFlag(args, "--effort"); v != "high" {
		t.Errorf("--effort=%q", v)
	}
	if v := findFlag(args, "--permission-mode"); v != "acceptEdits" {
		t.Errorf("--permission-mode=%q", v)
	}
}

func TestBuildCLIArgs_OptionalFlags(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{
			Model:              "sonnet",
			FallbackModel:      "haiku",
			MaxBudgetUSD:       12.5,
			UseWorktree:        true,
			AllowedTools:       []string{"Bash(npm test)", "Read"},
			DisallowedTools:    []string{"Bash(rm *)"},
			SystemPromptAppend: "Be terse.",
			AddDirs:            []string{"/extra/one", "/extra/two"},
		},
	})
	args := s.buildCLIArgs()

	if v := findFlag(args, "--fallback-model"); v != "haiku" {
		t.Errorf("--fallback-model=%q", v)
	}
	if v := findFlag(args, "--max-budget-usd"); v != "12.5" {
		t.Errorf("--max-budget-usd=%q", v)
	}
	if !hasFlag(args, "--worktree") {
		t.Error("missing --worktree")
	}
	if v := findFlag(args, "--allowedTools"); v != "Bash(npm test) Read" {
		t.Errorf("--allowedTools=%q", v)
	}
	if v := findFlag(args, "--disallowedTools"); v != "Bash(rm *)" {
		t.Errorf("--disallowedTools=%q", v)
	}
	if v := findFlag(args, "--append-system-prompt"); v != "Be terse." {
		t.Errorf("--append-system-prompt=%q", v)
	}

	// --add-dir appears once per directory
	addDirs := 0
	for i, a := range args {
		if a == "--add-dir" && i+1 < len(args) {
			addDirs++
		}
	}
	if addDirs != 2 {
		t.Errorf("expected 2 --add-dir flags, got %d", addDirs)
	}
}

func TestBuildCLIArgs_OmitsEmptyOptionals(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{
			Name:           "P1",
			Model:          "sonnet",
			PermissionMode: "acceptEdits",
			// MaxBudgetUSD=0, UseWorktree=false, empty slices and strings
		},
	})
	args := s.buildCLIArgs()
	joined := strings.Join(args, " ")

	for _, omitted := range []string{
		"--fallback-model",
		"--max-budget-usd",
		"--worktree",
		"--allowedTools",
		"--disallowedTools",
		"--append-system-prompt",
		"--add-dir",
	} {
		if strings.Contains(joined, omitted) {
			t.Errorf("expected %s to be omitted, but it is present: %s", omitted, joined)
		}
	}
}

func TestDetectRateLimitText(t *testing.T) {
	cases := []struct {
		name string
		line string
		want bool
	}{
		{"hit limit phrase", "You've hit your limit. Try again later.", true},
		{"rate limit phrase", "Error: rate limit exceeded", true},
		{"usage limit phrase", "Daily usage limit reached", true},
		{"unrelated text", "Reading file foo.go", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, got := detectRateLimitText(tc.line)
			if got != tc.want {
				t.Errorf("detectRateLimitText(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

func TestDetectRateLimitText_ExtractsResetTime(t *testing.T) {
	info, ok := detectRateLimitText("You hit your limit; resets at 3:45pm")
	if !ok {
		t.Fatal("expected rate limit match")
	}
	if info.ResetsAt == 0 {
		t.Error("expected ResetsAt to be set")
	}
}

func TestParseResetClock(t *testing.T) {
	now := time.Date(2026, 5, 23, 10, 0, 0, 0, time.UTC)

	// 12-hour with pm
	got, ok := parseResetClock("3:45pm", now)
	if !ok {
		t.Fatal("expected 3:45pm to parse")
	}
	if got.Hour() != 15 || got.Minute() != 45 {
		t.Errorf("3:45pm -> %v", got)
	}

	// 24-hour
	got, ok = parseResetClock("15:30", now)
	if !ok {
		t.Fatal("expected 15:30 to parse")
	}
	if got.Hour() != 15 || got.Minute() != 30 {
		t.Errorf("15:30 -> %v", got)
	}

	// Time already passed today rolls into tomorrow
	got, _ = parseResetClock("08:00", now)
	if !got.After(now) {
		t.Errorf("expected past time to roll to tomorrow, got %v (now=%v)", got, now)
	}

	if _, ok := parseResetClock("not a time", now); ok {
		t.Error("expected garbage to fail to parse")
	}
}

func TestSession_StatusInitiallyIdle(t *testing.T) {
	s := New(Params{ID: "x", Config: config.SessionConfig{}})
	if s.Status() != config.StatusIdle {
		t.Errorf("expected idle, got %s", s.Status())
	}
}

func TestSession_SendMessageRejectedWhenInactive(t *testing.T) {
	s := New(Params{ID: "x", Config: config.SessionConfig{}})
	if err := s.SendMessage("hi"); err == nil {
		t.Error("expected error sending message to idle session")
	}
}

// ---- hasTasks ----

func TestHasTasks(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"in_progress", "## In progress:\n- Task A\n", true},
		{"next_with_checkbox", "## Next:\n- [ ] Task B\n", true},
		{"next_without_checkbox", "## Next:\nsome text without checkboxes\n", false},
		{"empty", "", false},
		{"unrelated", "# Done\n- [x] old task\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := write(tc.name+".md", tc.content)
			if got := hasTasks(p); got != tc.want {
				t.Errorf("hasTasks(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

func TestHasTasks_MissingFile(t *testing.T) {
	if hasTasks("/no/such/file.md") {
		t.Error("missing file should return false")
	}
}

// ---- isAuthError ----

func TestIsAuthError(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{"403 Forbidden: authentication required", true},
		{"HTTP 403: you need to authenticate", true},
		{"403 Unauthorized", true},
		{"Error: rate limit exceeded", false},
		{"404 Not Found", false},
		{"200 OK", false},
		{"", false},
	}
	for _, tc := range cases {
		if got := isAuthError(tc.line); got != tc.want {
			t.Errorf("isAuthError(%q) = %v, want %v", tc.line, got, tc.want)
		}
	}
}

// ---- StateStore ----

func TestStateStore_SaveLoadClear(t *testing.T) {
	dir := t.TempDir()
	ss := NewStateStore(dir)

	// Nothing saved yet.
	st, err := ss.Load("proj", "sess")
	if err != nil || st != nil {
		t.Fatalf("expected nil state, got %v / %v", st, err)
	}

	// Save a state without session_id.
	saved := &PersistedState{StartedAt: time.Now().Truncate(time.Second)}
	if err := ss.Save("proj", "sess", saved); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := ss.Load("proj", "sess")
	if err != nil || loaded == nil {
		t.Fatalf("Load after Save: %v / %v", loaded, err)
	}
	if !loaded.StartedAt.Equal(saved.StartedAt) {
		t.Errorf("StartedAt mismatch: got %v, want %v", loaded.StartedAt, saved.StartedAt)
	}
	if loaded.SessionID != "" {
		t.Error("SessionID should be empty")
	}

	// Update session_id.
	ss.UpdateSessionID("proj", "sess", "abc-123")
	loaded2, _ := ss.Load("proj", "sess")
	if loaded2 == nil || loaded2.SessionID != "abc-123" {
		t.Errorf("UpdateSessionID: got %v", loaded2)
	}

	// Clear removes the file.
	ss.Clear("proj", "sess")
	st, err = ss.Load("proj", "sess")
	if err != nil || st != nil {
		t.Errorf("expected nil after Clear, got %v / %v", st, err)
	}
}

func TestStateStore_UpdateSessionID_NoFile(t *testing.T) {
	dir := t.TempDir()
	ss := NewStateStore(dir)
	// Should not panic or create a file when nothing is saved.
	ss.UpdateSessionID("proj", "sess", "abc")
	st, _ := ss.Load("proj", "sess")
	if st != nil {
		t.Error("UpdateSessionID should be no-op when no file exists")
	}
}

func TestStateStore_Clear_MissingFileIsNoOp(t *testing.T) {
	dir := t.TempDir()
	ss := NewStateStore(dir)
	// Must not panic.
	ss.Clear("proj", "sess")
}

func TestStateStore_AtomicWrite(t *testing.T) {
	dir := t.TempDir()
	ss := NewStateStore(dir)
	st := &PersistedState{StartedAt: time.Now()}
	if err := ss.Save("proj", "sess", st); err != nil {
		t.Fatal(err)
	}
	// No .tmp file should remain after Save.
	pattern := filepath.Join(dir, "*.tmp")
	matches, _ := filepath.Glob(pattern)
	if len(matches) > 0 {
		t.Errorf("tmp file(s) left behind: %v", matches)
	}
}

// ---- buildCLIArgs: resume flag ----

func TestBuildCLIArgs_ResumeReplaceSessionID(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{Model: "sonnet"},
	})
	s.mu.Lock()
	s.resumeSessionID = "saved-cli-id-xyz"
	s.mu.Unlock()

	args := s.buildCLIArgs()
	if !hasFlag(args, "--resume") {
		t.Error("expected --resume flag")
	}
	if v := findFlag(args, "--resume"); v != "saved-cli-id-xyz" {
		t.Errorf("--resume value = %q, want saved-cli-id-xyz", v)
	}
	if hasFlag(args, "--session-id") {
		t.Error("--session-id must be absent when --resume is used")
	}
}

func TestBuildCLIArgs_SessionIDWhenNoResume(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{Model: "sonnet"},
	})
	args := s.buildCLIArgs()
	if hasFlag(args, "--resume") {
		t.Error("--resume must be absent on normal start")
	}
	if !hasFlag(args, "--session-id") {
		t.Error("--session-id must be present on normal start")
	}
}

// ---- buildCLIArgs: fallback model ----

func TestBuildCLIArgs_FallbackModelOmittedWhenUsingFallback(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{
			Model:         "sonnet",
			FallbackModel: "haiku",
		},
	})
	s.usingFallback = true
	s.activeModel = "haiku"

	args := s.buildCLIArgs()
	if v := findFlag(args, "--model"); v != "haiku" {
		t.Errorf("--model should be haiku (active fallback), got %q", v)
	}
	if hasFlag(args, "--fallback-model") {
		t.Error("--fallback-model should be omitted when already using fallback")
	}
}

func TestBuildCLIArgs_ActiveModelOverridesConfig(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{Model: "sonnet"},
	})
	s.activeModel = "opus"

	args := s.buildCLIArgs()
	if v := findFlag(args, "--model"); v != "opus" {
		t.Errorf("--model = %q, want opus", v)
	}
}

// ---- initialPromptText ----

func TestInitialPromptText_Normal(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{Prompt: "  do the thing  "},
	})
	if got := s.initialPromptText(); got != "do the thing" {
		t.Errorf("got %q", got)
	}
}

func TestInitialPromptText_Recovery_DefaultPrompt(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{Prompt: "normal prompt"},
	})
	s.mu.Lock()
	s.resumeSessionID = "abc-123"
	s.mu.Unlock()

	got := s.initialPromptText()
	if got == "normal prompt" {
		t.Error("should not use normal prompt during recovery")
	}
	if got == "" {
		t.Error("recovery should return a non-empty default prompt")
	}
}

func TestInitialPromptText_Recovery_CustomPrompt(t *testing.T) {
	custom := "Проверь git status и продолжи задачу."
	s := New(Params{
		Config: config.SessionConfig{
			Prompt:              "normal",
			CrashRecoveryPrompt: custom,
		},
	})
	s.mu.Lock()
	s.resumeSessionID = "abc-123"
	s.mu.Unlock()

	if got := s.initialPromptText(); got != custom {
		t.Errorf("got %q, want %q", got, custom)
	}
}

func TestSession_EmitInvokesCallback(t *testing.T) {
	var got []SessionEvent
	s := New(Params{
		ID: "lumen/P1",
		OnEvent: func(id string, ev SessionEvent) {
			if id != "lumen/P1" {
				t.Errorf("unexpected id %q", id)
			}
			got = append(got, ev)
		},
	})
	s.setStatus(config.StatusWorking)
	s.setStatus(config.StatusWorking) // dedup
	s.setStatus(config.StatusError)

	if len(got) != 2 {
		t.Fatalf("expected 2 status events (dedup'd), got %d", len(got))
	}
	if got[0].Status != config.StatusWorking || got[1].Status != config.StatusError {
		t.Errorf("unexpected status sequence: %+v", got)
	}
}
