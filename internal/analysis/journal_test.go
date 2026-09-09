package analysis

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// --- buildJournalTaskText ---

func TestBuildJournalTaskTextIncludesAllFields(t *testing.T) {
	in := JournalInput{
		TaskPtr:          "ROADMAP.md:92",
		FilesChanged:     []string{"internal/foo.go", "internal/bar.go"},
		ResultText:       "Implemented the retry loop.",
		FailedGateOutput: "go test failed: ...",
	}
	task := buildJournalTaskText(in)
	for _, want := range []string{"ROADMAP.md:92", "internal/foo.go", "internal/bar.go",
		"Implemented the retry loop.", "go test failed"} {
		if !strings.Contains(task, want) {
			t.Errorf("task text missing %q:\n%s", want, task)
		}
	}
}

func TestBuildJournalTaskTextEmptyInput(t *testing.T) {
	if got := buildJournalTaskText(JournalInput{}); got != "" {
		t.Errorf("expected empty task text for empty input, got %q", got)
	}
}

// --- BuildJournalArgs ---

func TestBuildJournalArgsDefaults(t *testing.T) {
	args := BuildJournalArgs(AnalysisConfig{}, "some task text")

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
	if !flagHasValue(args, "--append-system-prompt", JournalSystemPrompt) {
		t.Error("expected default JournalSystemPrompt")
	}
	if flagPresent(args, "--max-budget-usd") {
		t.Error("--max-budget-usd should be absent when MaxBudgetUSD is 0")
	}
	last := args[len(args)-1]
	if !strings.Contains(last, "some task text") {
		t.Errorf("expected task text in final positional arg, got %q", last)
	}
}

func TestBuildJournalArgsJSONSchemaIsValidJSON(t *testing.T) {
	args := BuildJournalArgs(AnalysisConfig{}, "x")
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
	for _, key := range []string{"done", "surprises", "avoid"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema missing property %q", key)
		}
	}
}

// --- ParseJournalOutput ---

func TestParseJournalOutputBareObject(t *testing.T) {
	raw := `{"done": "wired the migration", "surprises": ["a"], "avoid": ["b", "c"]}`
	res, err := ParseJournalOutput([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Done != "wired the migration" {
		t.Errorf("Done = %q", res.Done)
	}
	if len(res.Surprises) != 1 || res.Surprises[0] != "a" {
		t.Errorf("Surprises = %v", res.Surprises)
	}
	if len(res.Avoid) != 2 {
		t.Errorf("Avoid = %v", res.Avoid)
	}
}

func TestParseJournalOutputResultWrapperObject(t *testing.T) {
	raw := `{
        "type": "result",
        "subtype": "success",
        "total_cost_usd": 0.003,
        "result": {"done": "did the thing", "surprises": [], "avoid": []}
    }`
	res, err := ParseJournalOutput([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.CostUSD != 0.003 {
		t.Errorf("cost: want 0.003, got %v", res.CostUSD)
	}
	if res.Done != "did the thing" {
		t.Errorf("Done = %q", res.Done)
	}
}

func TestParseJournalOutputResultWrapperString(t *testing.T) {
	inner := `{"done":"did X"}`
	encoded, _ := json.Marshal(inner)
	raw := `{"type":"result","total_cost_usd":0.001,"result":` + string(encoded) + `}`

	res, err := ParseJournalOutput([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Done != "did X" {
		t.Errorf("Done: want %q, got %q", "did X", res.Done)
	}
}

func TestParseJournalOutputErrors(t *testing.T) {
	if _, err := ParseJournalOutput(nil); err == nil {
		t.Error("expected error on empty input")
	}
	if _, err := ParseJournalOutput([]byte(`{"type":"result","is_error":true,"error":"bad"}`)); err == nil {
		t.Error("expected error when is_error=true")
	}
	if _, err := ParseJournalOutput([]byte(`{not valid`)); err == nil {
		t.Error("expected error on malformed json")
	}
}

// --- GenerateJournalEntry ---

// TestGenerateJournalEntry_NothingToSummarize: an all-empty JournalInput must
// error out before ever invoking the CLI (LEARN-TASKS.md LN-06 "тест что при
// выключенном флаге ... Claude не вызывается" — the manager-level twin of
// this is in internal/session; this is the direct-call guard).
func TestGenerateJournalEntry_NothingToSummarize(t *testing.T) {
	_, err := GenerateJournalEntry(context.Background(), "", JournalInput{}, AnalysisConfig{})
	if err == nil {
		t.Fatal("expected error for empty input")
	}
	if !strings.Contains(err.Error(), "nothing to summarize") {
		t.Errorf("unexpected error: %v", err)
	}
}
