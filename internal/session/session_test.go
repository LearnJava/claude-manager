package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/analysis"
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
	args := s.buildCLIArgs(false)

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
	args := s.buildCLIArgs(false)

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
	args := s.buildCLIArgs(false)
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

// TestBuildCLIArgs_AutonomousInjectsAskUserProtocol verifies an autonomous
// (task_source/auto_restart) run's system prompt teaches Claude the ask-user
// marker convention, and that a configured SystemPromptAppend is preserved
// alongside it rather than overwritten.
func TestBuildCLIArgs_AutonomousInjectsAskUserProtocol(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{
			Model:              "sonnet",
			SystemPromptAppend: "Be terse.",
		},
	})
	args := s.buildCLIArgs(true)
	v := findFlag(args, "--append-system-prompt")
	if !strings.Contains(v, "Be terse.") {
		t.Errorf("expected configured SystemPromptAppend to be preserved, got %q", v)
	}
	if !strings.Contains(v, "ask-user") {
		t.Errorf("expected ask-user protocol instruction to be injected, got %q", v)
	}
	if !strings.Contains(v, "run_in_background") {
		t.Errorf("expected background-task warning to be injected, got %q", v)
	}
}

// TestBuildCLIArgs_NonAutonomousOmitsAskUserProtocol verifies an interactive
// (no auto-restart, no task loop) run's prompt is left untouched — the user
// is already reading every reply directly, so the marker convention would
// just be unused instruction noise.
func TestBuildCLIArgs_NonAutonomousOmitsAskUserProtocol(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{
			Model:              "sonnet",
			SystemPromptAppend: "Be terse.",
		},
	})
	args := s.buildCLIArgs(false)
	if v := findFlag(args, "--append-system-prompt"); v != "Be terse." {
		t.Errorf("--append-system-prompt = %q, want exactly the configured value", v)
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
		// New canonical format: bare pointer lines `<source>:NN`.
		{"pointer_single", "ROADMAP.md:92\n", true},
		{"pointer_list", "BUGS.md:283\nBUGS.md:284\nCSS-SPECS.md:221\n", true},
		{"pointer_code_anchor", "crates/engine/layout/src/ruby.rs:76\n", true},
		{"pointer_with_prose_header", "# STATUS-P1\n> приоритет сверху вниз\nROADMAP.md:185\n", true},
		{"pointer_markers_ignored", "# heading\n- ROADMAP.md:92\n> ROADMAP.md:92\n_ROADMAP.md:92\n", false},
		{"pointer_not_bare", "see ROADMAP.md:92 for details\n", false},
		{"pointer_no_line_number", "ROADMAP.md\n", false},
		{"pointers_all_done_empty", "# STATUS-MP\n_(пусто)_\n", false},
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

// TestRoadmapFiles_ConsumedByTaskSourceCheck closes the loop between
// analysis.WriteRoadmapFiles (the generator) and hasTasks/
// resolveTaskSourceDescription (the consumer, already shipped): it confirms
// they actually agree on the bare-pointer-line format, not just each in
// isolation against a hand-written fixture.
func TestRoadmapFiles_ConsumedByTaskSourceCheck(t *testing.T) {
	dir := t.TempDir()
	plan := &analysis.TaskPlan{
		Project:       "demo",
		SharedContext: "A demo project.",
		Subtasks: []analysis.PlannedSubtask{
			{ID: "setup", Name: "project-setup", Prompt: "Scaffold the repo."},
			{ID: "auth", Name: "auth", Prompt: "Add JWT auth middleware.", DependsOn: []string{"setup"}},
		},
		ExecutionOrder: [][]string{{"setup"}, {"auth"}},
	}

	_, statusPath, err := analysis.WriteRoadmapFiles(dir, plan, false)
	if err != nil {
		t.Fatalf("WriteRoadmapFiles: %v", err)
	}

	if !hasTasks(statusPath) {
		t.Fatal("hasTasks should see the generated STATUS-P1.md as having open tasks")
	}

	desc := resolveTaskSourceDescription(dir, statusPath)
	if !strings.Contains(desc, "project-setup") {
		t.Errorf("resolveTaskSourceDescription should resolve to the first (setup) task's row, got: %q", desc)
	}
}

// ---- firstTaskPointer ----

func TestFirstTaskPointer(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"single", "ROADMAP.md:92\n", "ROADMAP.md:92"},
		{"picks_first_of_several", "ROADMAP.md:92\nROADMAP.md:93\n", "ROADMAP.md:92"},
		{"skips_headers_and_markers", "# heading\n> quote\n- ROADMAP.md:92\n_ROADMAP.md:92\nROADMAP.md:185\n", "ROADMAP.md:185"},
		{"code_anchor", "crates/engine/layout/src/ruby.rs:76\n", "crates/engine/layout/src/ruby.rs:76"},
		{"none_present", "## In progress:\n- Task A\n", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstTaskPointer(tc.content); got != tc.want {
				t.Errorf("firstTaskPointer(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

// ---- legacyTaskDescription ----

func TestLegacyTaskDescription(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{"in_progress_same_paragraph", "## In progress:\nFix the toolbar overflow\n", "Fix the toolbar overflow"},
		{"next_checkbox", "## Next:\n- [ ] Wire up the sidebar\n", "- [ ] Wire up the sidebar"},
		{"prefers_in_progress_over_next", "## In progress:\nFix A\n## Next:\n- [ ] Do B\n", "Fix A"},
		{"next_without_checkbox", "## Next:\nsome text without checkboxes\n", ""},
		{"neither_marker", "# Done\n- [x] old task\n", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := legacyTaskDescription(tc.content); got != tc.want {
				t.Errorf("legacyTaskDescription(%q) = %q, want %q", tc.content, got, tc.want)
			}
		})
	}
}

// ---- truncateRunes ----

func TestTruncateRunes(t *testing.T) {
	if got := truncateRunes("short", 200); got != "short" {
		t.Errorf("truncateRunes should not alter strings under the limit, got %q", got)
	}
	long := strings.Repeat("а", 250) // multi-byte rune (Cyrillic) to catch byte-vs-rune bugs
	got := truncateRunes(long, 200)
	if n := len([]rune(got)); n != 200 {
		t.Errorf("truncateRunes(250 runes, 200) = %d runes, want 200", n)
	}
}

// ---- resolveTaskSourceDescription ----

func TestResolveTaskSourceDescription(t *testing.T) {
	projectDir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(projectDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("resolves_pointer_into_source_line", func(t *testing.T) {
		write("ROADMAP.md", "line one\n| DS-14 | P3 | DS | planned | title |\nline three\n")
		write("STATUS-P1.md", "ROADMAP.md:2\n")
		got := resolveTaskSourceDescription(projectDir, filepath.Join(projectDir, "STATUS-P1.md"))
		want := "| DS-14 | P3 | DS | planned | title |"
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("truncates_long_lines", func(t *testing.T) {
		long := strings.Repeat("x", 300)
		write("BUGS.md", long+"\n")
		write("STATUS-P2.md", "BUGS.md:1\n")
		got := resolveTaskSourceDescription(projectDir, filepath.Join(projectDir, "STATUS-P2.md"))
		if len([]rune(got)) != 200 {
			t.Errorf("expected truncation to 200 runes, got %d", len([]rune(got)))
		}
	})

	t.Run("falls_back_to_pointer_when_source_missing", func(t *testing.T) {
		write("STATUS-P3.md", "MISSING.md:5\n")
		got := resolveTaskSourceDescription(projectDir, filepath.Join(projectDir, "STATUS-P3.md"))
		if got != "MISSING.md:5" {
			t.Errorf("got %q, want fallback to pointer text", got)
		}
	})

	t.Run("falls_back_to_pointer_when_line_out_of_range", func(t *testing.T) {
		write("SHORT.md", "only one line\n")
		write("STATUS-P4.md", "SHORT.md:99\n")
		got := resolveTaskSourceDescription(projectDir, filepath.Join(projectDir, "STATUS-P4.md"))
		if got != "SHORT.md:99" {
			t.Errorf("got %q, want fallback to pointer text", got)
		}
	})

	t.Run("falls_back_to_legacy_format", func(t *testing.T) {
		write("STATUS-P5.md", "## In progress:\nHealth sweep in progress\n")
		got := resolveTaskSourceDescription(projectDir, filepath.Join(projectDir, "STATUS-P5.md"))
		if got != "Health sweep in progress" {
			t.Errorf("got %q, want legacy description", got)
		}
	})

	t.Run("missing_task_source_file", func(t *testing.T) {
		got := resolveTaskSourceDescription(projectDir, filepath.Join(projectDir, "no-such-status.md"))
		if got != "" {
			t.Errorf("got %q, want empty string for missing file", got)
		}
	})
}

// ---- Session.setTaskSourceDesc ----

func TestSession_SetTaskSourceDesc(t *testing.T) {
	var events []SessionEvent
	s := New(Params{
		ID:     "x",
		Config: config.SessionConfig{},
		OnEvent: func(id string, ev SessionEvent) {
			events = append(events, ev)
		},
	})

	s.setTaskSourceDesc("first description")
	s.setTaskSourceDesc("first description") // no-op, must not re-emit
	s.setTaskSourceDesc("second description")

	if len(events) != 2 {
		t.Fatalf("expected 2 emitted events (no dup for unchanged value), got %d", len(events))
	}
	if events[0].Type != EvtTaskSource || events[0].TaskSourceDesc != "first description" {
		t.Errorf("unexpected first event: %+v", events[0])
	}
	if events[1].TaskSourceDesc != "second description" {
		t.Errorf("unexpected second event: %+v", events[1])
	}
	if got := s.Snapshot().TaskSourceDesc; got != "second description" {
		t.Errorf("Snapshot().TaskSourceDesc = %q, want %q", got, "second description")
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

	args := s.buildCLIArgs(false)
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
	args := s.buildCLIArgs(false)
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

	args := s.buildCLIArgs(false)
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

	args := s.buildCLIArgs(false)
	if v := findFlag(args, "--model"); v != "opus" {
		t.Errorf("--model = %q, want opus", v)
	}
}

// ---- initialPromptText ----

func TestInitialPromptText_Normal(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{Prompt: "  do the thing  "},
	})
	if got := s.initialPromptText(false); got != "do the thing" {
		t.Errorf("got %q", got)
	}
}

func TestInitialPromptText_ForceInteractive(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{Prompt: "normal prompt", TaskSource: "STATUS-P1.md"},
	})
	got := s.initialPromptText(true)
	if got == "normal prompt" {
		t.Error("should not use the normal task prompt when forced interactive")
	}
	if got == "" {
		t.Error("force-interactive should return a non-empty prompt")
	}
}

func TestInitialPromptText_RecoveryTakesPriorityOverForceInteractive(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{CrashRecoveryPrompt: "resume please"},
	})
	s.mu.Lock()
	s.resumeSessionID = "abc-123"
	s.mu.Unlock()

	if got := s.initialPromptText(true); got != "resume please" {
		t.Errorf("got %q, want crash recovery prompt to take priority", got)
	}
}

func TestInitialPromptText_Recovery_DefaultPrompt(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{Prompt: "normal prompt"},
	})
	s.mu.Lock()
	s.resumeSessionID = "abc-123"
	s.mu.Unlock()

	got := s.initialPromptText(false)
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

	if got := s.initialPromptText(false); got != custom {
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

func TestCurrentTaskFromTodos(t *testing.T) {
	tests := []struct {
		name  string
		todos []TodoItem
		want  string
	}{
		{"empty", nil, ""},
		{
			"in_progress wins, activeForm preferred",
			[]TodoItem{
				{Content: "Done one", Status: "completed"},
				{Content: "Fix bug", Status: "in_progress", ActiveForm: "Fixing bug"},
				{Content: "Next", Status: "pending"},
			},
			"Fixing bug",
		},
		{
			"in_progress without activeForm falls back to content",
			[]TodoItem{{Content: "Fix bug", Status: "in_progress"}},
			"Fix bug",
		},
		{
			"no in_progress: first pending",
			[]TodoItem{
				{Content: "Done", Status: "completed"},
				{Content: "Queued", Status: "pending"},
			},
			"Queued",
		},
		{
			"all completed: last item",
			[]TodoItem{
				{Content: "First", Status: "completed"},
				{Content: "Last", Status: "completed"},
			},
			"Last",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := currentTaskFromTodos(tc.todos); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSession_HandleLineTodoWrite(t *testing.T) {
	var todoEvents [][]TodoItem
	s := New(Params{
		ID: "lumen/P1",
		OnEvent: func(id string, ev SessionEvent) {
			if ev.Type == EvtTodo {
				todoEvents = append(todoEvents, ev.Todos)
			}
		},
	})

	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[{"content":"Step A","status":"completed"},{"content":"Step B","status":"in_progress","activeForm":"Doing step B"}]}}]}}`
	if done := s.handleLine(line, false); done {
		t.Error("TodoWrite line must not end the turn")
	}

	if len(todoEvents) != 1 {
		t.Fatalf("expected 1 EvtTodo, got %d", len(todoEvents))
	}
	if len(todoEvents[0]) != 2 || todoEvents[0][1].Content != "Step B" {
		t.Errorf("unexpected todos: %+v", todoEvents[0])
	}

	snap := s.Snapshot()
	if snap.CurrentTask != "Doing step B" {
		t.Errorf("unexpected CurrentTask: %q", snap.CurrentTask)
	}
	if len(snap.Todos) != 2 {
		t.Errorf("expected 2 todos in snapshot, got %d", len(snap.Todos))
	}
}

func TestSession_UpdateTodosNilClears(t *testing.T) {
	var events int
	s := New(Params{
		ID: "lumen/P1",
		OnEvent: func(id string, ev SessionEvent) {
			if ev.Type == EvtTodo {
				events++
			}
		},
	})
	s.updateTodos([]TodoItem{{Content: "X", Status: "in_progress"}})
	s.updateTodos(nil)

	if events != 2 {
		t.Fatalf("expected 2 EvtTodo events, got %d", events)
	}
	snap := s.Snapshot()
	if snap.CurrentTask != "" || len(snap.Todos) != 0 {
		t.Errorf("expected cleared state, got task=%q todos=%+v", snap.CurrentTask, snap.Todos)
	}
}
