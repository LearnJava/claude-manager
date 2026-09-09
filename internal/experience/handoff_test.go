package experience

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/analysis"
)

func TestRecentStepLines_ToolUseAndProse(t *testing.T) {
	steps := []Step{
		{Kind: StepText, InputText: "Let me look at the file."},
		{Kind: StepToolUse, ToolName: "Read", InputText: "internal/foo.go"},
		{Kind: StepThinking, InputText: "  "}, // blank thinking must be skipped
		{Kind: StepToolUse, ToolName: "Edit", InputText: "internal/foo.go"},
	}
	got := recentStepLines(steps, 10)
	want := []string{
		"Let me look at the file.",
		"Read: internal/foo.go",
		"Edit: internal/foo.go",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRecentStepLines_CapsToTail(t *testing.T) {
	steps := make([]Step, 0, 5)
	for i := 0; i < 5; i++ {
		steps = append(steps, Step{Kind: StepToolUse, ToolName: "Bash", InputText: string(rune('a' + i))})
	}
	got := recentStepLines(steps, 2)
	want := []string{"Bash: d", "Bash: e"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v (last 2 of 5)", got, want)
	}
}

func TestBuildHandoffInput_TranscriptFound(t *testing.T) {
	root := t.TempDir()
	projectPath := `D:\Some\Project`
	slugDir := filepath.Join(root, ProjectSlug(projectPath))
	if err := os.MkdirAll(slugDir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"assistant","sessionId":"abc-123","message":{"role":"assistant","content":[{"type":"tool_use","id":"1","name":"Bash","input":{"command":"go build ./..."}}]}}` + "\n"
	if err := os.WriteFile(filepath.Join(slugDir, "abc-123.jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}

	in := BuildHandoffInput(root, projectPath, "abc-123", "ROADMAP.md:1 | do X", []string{"[~] step one"})
	if in.TaskPtr != "ROADMAP.md:1 | do X" {
		t.Errorf("TaskPtr = %q", in.TaskPtr)
	}
	if len(in.Todos) != 1 || in.Todos[0] != "[~] step one" {
		t.Errorf("Todos = %v", in.Todos)
	}
	if len(in.RecentSteps) != 1 || !strings.Contains(in.RecentSteps[0], "go build") {
		t.Errorf("RecentSteps = %v, want the Bash call rendered", in.RecentSteps)
	}
}

// TestBuildHandoffInput_NoTranscript verifies the best-effort contract: a
// missing transcript (e.g. fakeclaude in tests, which never writes one to
// disk) must not error — it just yields no RecentSteps.
func TestBuildHandoffInput_NoTranscript(t *testing.T) {
	root := t.TempDir()
	in := BuildHandoffInput(root, `D:\Nonexistent`, "missing-session", "task", []string{"[ ] a"})
	if in.TaskPtr != "task" {
		t.Errorf("TaskPtr = %q", in.TaskPtr)
	}
	if in.RecentSteps != nil {
		t.Errorf("RecentSteps = %v, want nil when no transcript exists", in.RecentSteps)
	}
}

func TestRenderHandoffPrompt(t *testing.T) {
	res := &analysis.HandoffResult{
		Done:         "wired the migration",
		Remaining:    "add a test",
		Decisions:    []string{"use sync.Mutex, not channels"},
		FilesChanged: []string{"internal/foo.go"},
	}
	got := RenderHandoffPrompt(res)
	if !strings.HasPrefix(got, HandoffMarker) {
		t.Errorf("expected prompt to start with the handoff marker, got %q", got)
	}
	for _, want := range []string{"wired the migration", "add a test", "use sync.Mutex, not channels", "internal/foo.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered prompt missing %q:\n%s", want, got)
		}
	}
}

func TestRenderHandoffPrompt_NilResult(t *testing.T) {
	if got := RenderHandoffPrompt(nil); got != "" {
		t.Errorf("got %q, want empty for nil result", got)
	}
}

func TestRenderHandoffPrompt_OmitsEmptySections(t *testing.T) {
	got := RenderHandoffPrompt(&analysis.HandoffResult{Done: "x", Remaining: "y"})
	if strings.Contains(got, "Decisions already made") || strings.Contains(got, "Files already touched") {
		t.Errorf("expected empty sections to be omitted entirely, got:\n%s", got)
	}
}
