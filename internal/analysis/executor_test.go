package analysis

import (
	"strings"
	"testing"
)

// ---- BuildSubtaskArgs ----

func argsContainPair(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func TestBuildSubtaskArgs_Defaults(t *testing.T) {
	args := BuildSubtaskArgs(CLIExecutor{}, PlannedSubtask{ID: "a", Prompt: "do it"}, "")
	if args[0] != "-p" {
		t.Errorf("args[0] = %q, want -p", args[0])
	}
	if !argsContainPair(args, "--model", "sonnet") {
		t.Errorf("expected default model sonnet, args: %v", args)
	}
	if !argsContainPair(args, "--effort", "medium") {
		t.Errorf("expected default effort medium, args: %v", args)
	}
	if !argsContainPair(args, "--output-format", "json") {
		t.Errorf("expected json output format, args: %v", args)
	}
	for _, a := range args {
		if a == "--append-system-prompt" {
			t.Error("empty contextAppend must omit --append-system-prompt")
		}
	}
	if args[len(args)-1] != "do it" {
		t.Errorf("prompt must be the final positional arg, got %q", args[len(args)-1])
	}
}

func TestBuildSubtaskArgs_SubtaskOverridesExecutorDefaults(t *testing.T) {
	e := CLIExecutor{DefaultModel: "haiku", DefaultEffort: "low"}

	// Executor defaults apply when the subtask has none.
	args := BuildSubtaskArgs(e, PlannedSubtask{Prompt: "x"}, "")
	if !argsContainPair(args, "--model", "haiku") || !argsContainPair(args, "--effort", "low") {
		t.Errorf("executor defaults not applied: %v", args)
	}

	// Subtask values win over executor defaults.
	args = BuildSubtaskArgs(e, PlannedSubtask{Prompt: "x", Model: "opus", Effort: "high"}, "")
	if !argsContainPair(args, "--model", "opus") || !argsContainPair(args, "--effort", "high") {
		t.Errorf("subtask overrides not applied: %v", args)
	}
}

func TestBuildSubtaskArgs_ContextAppend(t *testing.T) {
	args := BuildSubtaskArgs(CLIExecutor{}, PlannedSubtask{Prompt: "x"}, "prior summary")
	if !argsContainPair(args, "--append-system-prompt", "prior summary") {
		t.Errorf("contextAppend must be forwarded via --append-system-prompt: %v", args)
	}
}

// ---- ParseSubtaskOutput ----

func TestParseSubtaskOutput_Success(t *testing.T) {
	out := []byte(`{
		"type":"result","subtype":"success","is_error":false,
		"result":"Implemented the parser.",
		"session_id":"sess-123","total_cost_usd":0.42,
		"usage":{"input_tokens":1000,"output_tokens":250}
	}`)
	res, err := ParseSubtaskOutput(out)
	if err != nil {
		t.Fatalf("ParseSubtaskOutput: %v", err)
	}
	if res.SessionID != "sess-123" {
		t.Errorf("SessionID = %q", res.SessionID)
	}
	if res.Summary != "Implemented the parser." {
		t.Errorf("Summary = %q", res.Summary)
	}
	if res.CostUSD != 0.42 {
		t.Errorf("CostUSD = %v", res.CostUSD)
	}
	if res.InputTokens != 1000 || res.OutputTokens != 250 {
		t.Errorf("tokens = %d/%d, want 1000/250", res.InputTokens, res.OutputTokens)
	}
}

func TestParseSubtaskOutput_Error(t *testing.T) {
	out := []byte(`{"type":"result","is_error":true,"error":"budget exceeded"}`)
	if _, err := ParseSubtaskOutput(out); err == nil || !strings.Contains(err.Error(), "budget exceeded") {
		t.Fatalf("expected budget exceeded error, got %v", err)
	}
}

func TestParseSubtaskOutput_Empty(t *testing.T) {
	if _, err := ParseSubtaskOutput([]byte("  \n")); err == nil {
		t.Fatal("expected error on empty output")
	}
}

func TestParseSubtaskOutput_SummaryTruncated(t *testing.T) {
	long := strings.Repeat("я", subtaskSummaryLimit+100)
	out := []byte(`{"type":"result","result":"` + long + `"}`)
	res, err := ParseSubtaskOutput(out)
	if err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(res.Summary)); got != subtaskSummaryLimit+1 { // +1 for the ellipsis
		t.Errorf("summary rune length = %d, want %d", got, subtaskSummaryLimit+1)
	}
	if !strings.HasSuffix(res.Summary, "…") {
		t.Error("truncated summary must end with ellipsis")
	}
}
