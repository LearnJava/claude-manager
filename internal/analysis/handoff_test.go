package analysis

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// --- buildHandoffTaskText ---

func TestBuildHandoffTaskTextIncludesAllFields(t *testing.T) {
	in := HandoffInput{
		TaskPtr:     "ROADMAP.md:92",
		Todos:       []string{"[x] step one", "[~] step two"},
		RecentSteps: []string{"Read: internal/foo.go", "Edit: internal/foo.go"},
	}
	task := buildHandoffTaskText(in)
	for _, want := range []string{"ROADMAP.md:92", "step one", "step two",
		"Read: internal/foo.go", "Edit: internal/foo.go"} {
		if !strings.Contains(task, want) {
			t.Errorf("task text missing %q:\n%s", want, task)
		}
	}
}

func TestBuildHandoffTaskTextEmptyInput(t *testing.T) {
	if got := buildHandoffTaskText(HandoffInput{}); got != "" {
		t.Errorf("expected empty task text for empty input, got %q", got)
	}
}

// --- BuildHandoffArgs ---

func TestBuildHandoffArgsDefaults(t *testing.T) {
	args := BuildHandoffArgs(AnalysisConfig{}, "some task text")

	want := map[string]string{
		"--model":           "haiku",
		"--effort":          "medium",
		"--permission-mode": "plan",
		"--output-format":   "json",
	}
	for flag, val := range want {
		if !flagHasValue(args, flag, val) {
			t.Errorf("missing flag %q = %q in args: %v", flag, val, args)
		}
	}
	if !flagPresent(args, "--json-schema") {
		t.Error("expected --json-schema flag")
	}
	if !flagHasValue(args, "--append-system-prompt", HandoffSystemPrompt) {
		t.Error("expected default HandoffSystemPrompt")
	}
	if flagPresent(args, "--max-budget-usd") {
		t.Error("--max-budget-usd should be absent when MaxBudgetUSD is 0")
	}
	last := args[len(args)-1]
	if !strings.Contains(last, "some task text") {
		t.Errorf("expected task text in final positional arg, got %q", last)
	}
}

func TestBuildHandoffArgsJSONSchemaIsValidJSON(t *testing.T) {
	args := BuildHandoffArgs(AnalysisConfig{}, "x")
	schema := flagValue(args, "--json-schema")
	if schema == "" {
		t.Fatal("missing --json-schema value")
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(schema), &v); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	props, ok := v["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}
	for _, key := range []string{"done", "remaining", "decisions", "files_changed"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema missing property %q", key)
		}
	}
}

// --- ParseHandoffOutput ---

func TestParseHandoffOutputBareObject(t *testing.T) {
	raw := `{"done": "wired the migration", "remaining": "add a test",
	         "decisions": ["use sync.Mutex"], "files_changed": ["internal/foo.go"]}`
	res, err := ParseHandoffOutput([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Done != "wired the migration" {
		t.Errorf("Done = %q", res.Done)
	}
	if res.Remaining != "add a test" {
		t.Errorf("Remaining = %q", res.Remaining)
	}
	if len(res.Decisions) != 1 || res.Decisions[0] != "use sync.Mutex" {
		t.Errorf("Decisions = %v", res.Decisions)
	}
	if len(res.FilesChanged) != 1 || res.FilesChanged[0] != "internal/foo.go" {
		t.Errorf("FilesChanged = %v", res.FilesChanged)
	}
}

func TestParseHandoffOutputResultWrapperObject(t *testing.T) {
	raw := `{
        "type": "result",
        "subtype": "success",
        "total_cost_usd": 0.002,
        "result": {"done": "did the thing", "remaining": "finish up"}
    }`
	res, err := ParseHandoffOutput([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.CostUSD != 0.002 {
		t.Errorf("cost: want 0.002, got %v", res.CostUSD)
	}
	if res.Done != "did the thing" {
		t.Errorf("Done = %q", res.Done)
	}
}

func TestParseHandoffOutputResultWrapperString(t *testing.T) {
	inner := `{"done":"did X","remaining":"finish Y"}`
	encoded, _ := json.Marshal(inner)
	raw := `{"type":"result","total_cost_usd":0.001,"result":` + string(encoded) + `}`

	res, err := ParseHandoffOutput([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Done != "did X" {
		t.Errorf("Done: want %q, got %q", "did X", res.Done)
	}
}

func TestParseHandoffOutputErrors(t *testing.T) {
	if _, err := ParseHandoffOutput(nil); err == nil {
		t.Error("expected error on empty input")
	}
	if _, err := ParseHandoffOutput([]byte(`{"type":"result","is_error":true,"error":"bad"}`)); err == nil {
		t.Error("expected error when is_error=true")
	}
	if _, err := ParseHandoffOutput([]byte(`{not valid`)); err == nil {
		t.Error("expected error on malformed json")
	}
}

// --- GenerateHandoff ---

// TestGenerateHandoff_NothingToSummarize: an all-empty HandoffInput must
// error out before ever invoking the CLI — mirrors
// TestGenerateJournalEntry_NothingToSummarize.
func TestGenerateHandoff_NothingToSummarize(t *testing.T) {
	_, err := GenerateHandoff(context.Background(), "", HandoffInput{}, AnalysisConfig{})
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !strings.Contains(err.Error(), "nothing to summarize") {
		t.Errorf("unexpected error: %v", err)
	}
}
