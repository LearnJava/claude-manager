package analysis

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/worker"
)

// --- BuildBriefArgs ---

func TestBuildBriefArgsDefaults(t *testing.T) {
	args := BuildBriefArgs(AnalysisConfig{}, "add retry logic to the client")

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
	if !flagHasValue(args, "--append-system-prompt", BriefSystemPrompt) {
		t.Error("expected default BriefSystemPrompt")
	}
	if flagPresent(args, "--max-budget-usd") {
		t.Error("--max-budget-usd should be absent when MaxBudgetUSD is 0")
	}
	if args[0] != "-p" {
		t.Errorf("expected -p first, got %q", args[0])
	}
	last := args[len(args)-1]
	if !strings.HasPrefix(last, "Write a mixed-programming brief for this task:") {
		t.Errorf("expected task as final positional arg, got %q", last)
	}
}

func TestBuildBriefArgsOverrides(t *testing.T) {
	cfg := AnalysisConfig{
		Model:        "sonnet",
		Effort:       "high",
		MaxBudgetUSD: 0.5,
		SystemPrompt: "custom brief prompt",
	}
	args := BuildBriefArgs(cfg, "do thing")

	if !flagHasValue(args, "--model", "sonnet") {
		t.Error("expected model=sonnet")
	}
	if !flagHasValue(args, "--effort", "high") {
		t.Error("expected effort=high")
	}
	if !flagHasValue(args, "--max-budget-usd", "0.5") {
		t.Error("expected --max-budget-usd=0.5")
	}
	if !flagHasValue(args, "--append-system-prompt", "custom brief prompt") {
		t.Error("expected custom system prompt")
	}
}

func TestBuildBriefArgsJSONSchemaIsValidJSON(t *testing.T) {
	args := BuildBriefArgs(AnalysisConfig{}, "x")
	schema := flagValue(args, "--json-schema")
	if schema == "" {
		t.Fatal("missing --json-schema value")
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(schema), &v); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	if v["type"] != "object" {
		t.Errorf("schema root type should be object, got %v", v["type"])
	}
	props, ok := v["properties"].(map[string]any)
	if !ok {
		t.Fatal("schema missing properties")
	}
	for _, key := range []string{"task", "files", "new_files"} {
		if _, ok := props[key]; !ok {
			t.Errorf("schema missing property %q", key)
		}
	}
}

// --- ParseBriefOutput ---

func TestParseBriefOutputBareObject(t *testing.T) {
	raw := `{
        "task": "Replace the retry loop in internal/foo.go:42-58 with exponential backoff.",
        "files": ["internal/foo.go"],
        "new_files": []
    }`
	res, err := ParseBriefOutput([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.Contains(res.Task, "exponential backoff") {
		t.Errorf("task: got %q", res.Task)
	}
	if len(res.Files) != 1 || res.Files[0] != "internal/foo.go" {
		t.Errorf("files: got %v", res.Files)
	}
}

func TestParseBriefOutputResultWrapperObject(t *testing.T) {
	raw := `{
        "type": "result",
        "subtype": "success",
        "total_cost_usd": 0.02,
        "result": {
            "task": "Add a Stringer to Status.",
            "files": ["status.go"],
            "new_files": ["status_string.go"]
        }
    }`
	res, err := ParseBriefOutput([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.CostUSD != 0.02 {
		t.Errorf("cost: want 0.02, got %v", res.CostUSD)
	}
	if len(res.NewFiles) != 1 || res.NewFiles[0] != "status_string.go" {
		t.Errorf("new_files: got %v", res.NewFiles)
	}
}

func TestParseBriefOutputResultWrapperString(t *testing.T) {
	inner := `{"task":"do X","files":["a.go"]}`
	encoded, _ := json.Marshal(inner)
	raw := `{"type":"result","total_cost_usd":0.01,"result":` + string(encoded) + `}`

	res, err := ParseBriefOutput([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Task != "do X" {
		t.Errorf("task: want %q, got %q", "do X", res.Task)
	}
}

func TestParseBriefOutputErrors(t *testing.T) {
	if _, err := ParseBriefOutput(nil); err == nil {
		t.Error("expected error on empty input")
	}
	if _, err := ParseBriefOutput([]byte(`{"type":"result","is_error":true,"error":"bad"}`)); err == nil {
		t.Error("expected error when is_error=true")
	}
	if _, err := ParseBriefOutput([]byte(`{not valid`)); err == nil {
		t.Error("expected error on malformed json")
	}
}

// --- ValidateBriefResult ---

func TestValidateBriefResultAccepts(t *testing.T) {
	r := &BriefResult{Task: "Do the well-specified thing.", Files: []string{"a.go"}}
	if err := ValidateBriefResult(r); err != nil {
		t.Errorf("valid result rejected: %v", err)
	}
}

func TestValidateBriefResultRequiresTask(t *testing.T) {
	r := &BriefResult{Task: "   ", Files: []string{"a.go"}}
	if err := ValidateBriefResult(r); err == nil {
		t.Error("expected error for empty task")
	}
}

func TestValidateBriefResultRequiresFiles(t *testing.T) {
	r := &BriefResult{Task: "Do the thing."}
	if err := ValidateBriefResult(r); err == nil {
		t.Error("expected error for no files")
	}
	r2 := &BriefResult{Task: "Do the thing.", NewFiles: []string{"new.go"}}
	if err := ValidateBriefResult(r2); err != nil {
		t.Errorf("new_files alone should satisfy the files requirement: %v", err)
	}
}

func TestValidateBriefResultRejectsUnresolvedDecision(t *testing.T) {
	cases := []string{
		"You could either use a mutex or a channel here.",
		"Choose between approach A and approach B.",
		"Pick whichever style you prefer.",
	}
	for _, task := range cases {
		r := &BriefResult{Task: task, Files: []string{"a.go"}}
		if err := ValidateBriefResult(r); err == nil {
			t.Errorf("task %q should be rejected as an unresolved decision", task)
		}
	}
}

func TestValidateBriefResultNil(t *testing.T) {
	if err := ValidateBriefResult(nil); err == nil {
		t.Error("expected error for nil result")
	}
}

// --- NewBriefFromResult ---

func TestNewBriefFromResultAssignsID(t *testing.T) {
	r := &BriefResult{Task: "do X", Files: []string{"a.go"}}
	b1 := NewBriefFromResult(r)
	b2 := NewBriefFromResult(r)
	if b1.ID == "" || b2.ID == "" {
		t.Fatal("expected non-empty IDs")
	}
	if b1.ID == b2.ID {
		t.Error("expected distinct IDs across calls")
	}
	if b1.Task != "do X" {
		t.Errorf("task not carried over: %q", b1.Task)
	}
}

// --- CreatePlaceholderFile ---

func TestCreatePlaceholderFileCreatesAnchor(t *testing.T) {
	dir := t.TempDir()
	if err := CreatePlaceholderFile(dir, "internal/new/thing.go"); err != nil {
		t.Fatalf("CreatePlaceholderFile: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dir, "internal", "new", "thing.go"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.TrimSpace(string(got)) != PlaceholderAnchor {
		t.Errorf("content = %q, want %q", got, PlaceholderAnchor)
	}
}

func TestCreatePlaceholderFileIdempotent(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "a.go")
	if err := os.WriteFile(full, []byte("package a\n\nfunc Real() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := CreatePlaceholderFile(dir, "a.go"); err != nil {
		t.Fatalf("CreatePlaceholderFile: %v", err)
	}
	got, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "Real()") {
		t.Errorf("existing file was clobbered: %q", got)
	}
}

func TestCreatePlaceholderFileRejectsPathEscape(t *testing.T) {
	dir := t.TempDir()
	cases := []string{"../escape.go", "/abs/path.go", "sub/../../escape.go"}
	for _, rel := range cases {
		if err := CreatePlaceholderFile(dir, rel); err == nil {
			t.Errorf("path %q: expected rejection", rel)
		}
	}
}

// --- SaveBrief / LoadBrief ---

func TestSaveBriefLoadBriefRoundtrip(t *testing.T) {
	st := newTestStore(t)
	brief := &worker.Brief{ID: "brief-123", Task: "Replace the retry loop with backoff."}
	files := []string{"internal/foo.go", "internal/foo_new.go"}

	if err := SaveBrief(st, "lumen", brief, files); err != nil {
		t.Fatalf("SaveBrief: %v", err)
	}

	loaded, loadedFiles, err := LoadBrief(st, "brief-123")
	if err != nil {
		t.Fatalf("LoadBrief: %v", err)
	}
	if loaded == nil {
		t.Fatal("loaded brief is nil")
	}
	if loaded.ID != brief.ID || loaded.Task != brief.Task {
		t.Errorf("loaded brief = %+v, want %+v", loaded, brief)
	}
	if len(loadedFiles) != 2 || loadedFiles[0] != files[0] || loadedFiles[1] != files[1] {
		t.Errorf("loaded files = %v, want %v", loadedFiles, files)
	}
}

func TestSaveBriefNilStore(t *testing.T) {
	if err := SaveBrief(nil, "lumen", &worker.Brief{ID: "x", Task: "y"}, nil); err != nil {
		t.Errorf("nil store should be no-op, got %v", err)
	}
}

func TestLoadBriefMissing(t *testing.T) {
	st := newTestStore(t)
	b, files, err := LoadBrief(st, "no-such-brief")
	if err != nil {
		t.Fatalf("LoadBrief: %v", err)
	}
	if b != nil || files != nil {
		t.Errorf("expected (nil, nil) for missing brief, got (%+v, %v)", b, files)
	}
}

func TestLoadBriefNilStore(t *testing.T) {
	if _, _, err := LoadBrief(nil, "x"); err == nil {
		t.Error("expected error for nil store")
	}
}
