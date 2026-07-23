package analysis

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildAnalysisArgsDefaults(t *testing.T) {
	args := BuildAnalysisArgs(AnalysisConfig{}, "Refactor auth module")

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
	if !flagPresent(args, "--append-system-prompt") {
		t.Error("expected --append-system-prompt flag")
	}
	if flagPresent(args, "--max-budget-usd") {
		t.Error("--max-budget-usd should be absent when MaxBudgetUSD is 0")
	}
	if args[0] != "-p" {
		t.Errorf("expected -p first, got %q", args[0])
	}
	last := args[len(args)-1]
	if !strings.HasPrefix(last, "Analyze this task:") {
		t.Errorf("expected task as final positional arg, got %q", last)
	}
}

func TestBuildAnalysisArgsOverrides(t *testing.T) {
	cfg := AnalysisConfig{
		Model:        "sonnet",
		Effort:       "high",
		MaxBudgetUSD: 0.25,
		SystemPrompt: "custom analyst prompt",
	}
	args := BuildAnalysisArgs(cfg, "do thing")

	if !flagHasValue(args, "--model", "sonnet") {
		t.Error("expected model=sonnet")
	}
	if !flagHasValue(args, "--effort", "high") {
		t.Error("expected effort=high")
	}
	if !flagHasValue(args, "--max-budget-usd", "0.25") {
		t.Error("expected --max-budget-usd=0.25")
	}
	if !flagHasValue(args, "--append-system-prompt", "custom analyst prompt") {
		t.Error("expected custom system prompt")
	}
}

func TestBuildAnalysisArgsSchemaOverride(t *testing.T) {
	args := BuildAnalysisArgs(AnalysisConfig{JSONSchema: RoadmapJSONSchema}, "build a thing")
	if !flagHasValue(args, "--json-schema", RoadmapJSONSchema) {
		t.Error("expected the overridden schema, not AnalysisJSONSchema")
	}

	// Empty JSONSchema still falls back to the default.
	args = BuildAnalysisArgs(AnalysisConfig{}, "build a thing")
	if !flagHasValue(args, "--json-schema", AnalysisJSONSchema) {
		t.Error("expected AnalysisJSONSchema when JSONSchema is unset")
	}
}

func TestBuildAnalysisArgsJSONSchemaIsValidJSON(t *testing.T) {
	args := BuildAnalysisArgs(AnalysisConfig{}, "x")
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
}

func TestRoadmapJSONSchemaIsValidJSON(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal([]byte(RoadmapJSONSchema), &v); err != nil {
		t.Fatalf("RoadmapJSONSchema is not valid JSON: %v", err)
	}
	if v["type"] != "object" {
		t.Errorf("schema root type should be object, got %v", v["type"])
	}
	props, ok := v["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected a properties object")
	}
	for _, key := range []string{"subtasks", "execution_order", "shared_context"} {
		if _, ok := props[key]; !ok {
			t.Errorf("RoadmapJSONSchema missing property %q", key)
		}
	}
	if _, ok := props["feasibility"]; ok {
		t.Error("RoadmapJSONSchema should not carry a single-task feasibility block")
	}
}

func TestParseAnalysisOutputBareObject(t *testing.T) {
	raw := `{
        "feasibility": {
            "single_session": true,
            "confidence": 0.9,
            "reasoning": "small task",
            "estimated_complexity": "small",
            "estimated_files_affected": 3,
            "estimated_tokens": 5000,
            "risks": ["none"]
        },
        "recommended_approach": "single_session",
        "recommended_model": "sonnet",
        "recommended_effort": "medium",
        "subtasks": [],
        "execution_order": [],
        "shared_context": "ctx"
    }`

	res, err := ParseAnalysisOutput([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !res.Feasibility.SingleSession {
		t.Error("expected single_session=true")
	}
	if res.Feasibility.Confidence != 0.9 {
		t.Errorf("confidence: want 0.9, got %v", res.Feasibility.Confidence)
	}
	if res.RecommendedModel != "sonnet" {
		t.Errorf("model: want sonnet, got %s", res.RecommendedModel)
	}
	if res.SharedContext != "ctx" {
		t.Errorf("shared_context: want ctx, got %s", res.SharedContext)
	}
}

func TestParseAnalysisOutputResultWrapperObject(t *testing.T) {
	raw := `{
        "type": "result",
        "subtype": "success",
        "total_cost_usd": 0.04,
        "result": {
            "feasibility": {"single_session": false, "confidence": 0.7, "estimated_complexity": "large"},
            "recommended_approach": "sequential_sessions",
            "subtasks": [
                {"id": "a", "name": "core", "prompt": "do A", "model": "sonnet"},
                {"id": "b", "name": "tests", "prompt": "do B", "depends_on": ["a"]}
            ],
            "execution_order": [["a"], ["b"]]
        }
    }`

	res, err := ParseAnalysisOutput([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.CostUSD != 0.04 {
		t.Errorf("cost: want 0.04, got %v", res.CostUSD)
	}
	if len(res.Subtasks) != 2 {
		t.Fatalf("subtasks: want 2, got %d", len(res.Subtasks))
	}
	if res.Subtasks[0].ID != "a" {
		t.Errorf("first subtask id: want a, got %s", res.Subtasks[0].ID)
	}
	if got := res.Subtasks[1].DependsOn; len(got) != 1 || got[0] != "a" {
		t.Errorf("depends_on: want [a], got %v", got)
	}
	if len(res.ExecutionOrder) != 2 || res.ExecutionOrder[1][0] != "b" {
		t.Errorf("execution_order parsed wrong: %v", res.ExecutionOrder)
	}
}

func TestParseAnalysisOutputResultWrapperString(t *testing.T) {
	inner := `{"feasibility":{"single_session":true,"confidence":0.5,"estimated_complexity":"trivial"},"recommended_approach":"single_session"}`
	encoded, _ := json.Marshal(inner)
	raw := `{"type":"result","total_cost_usd":0.01,"result":` + string(encoded) + `}`

	res, err := ParseAnalysisOutput([]byte(raw))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !res.Feasibility.SingleSession {
		t.Error("expected single_session=true")
	}
	if res.CostUSD != 0.01 {
		t.Errorf("cost: want 0.01, got %v", res.CostUSD)
	}
}

func TestParseAnalysisOutputErrors(t *testing.T) {
	if _, err := ParseAnalysisOutput(nil); err == nil {
		t.Error("expected error on empty input")
	}
	if _, err := ParseAnalysisOutput([]byte(`{"type":"result","is_error":true,"error":"bad"}`)); err == nil {
		t.Error("expected error when is_error=true")
	}
	if _, err := ParseAnalysisOutput([]byte(`{not valid`)); err == nil {
		t.Error("expected error on malformed json")
	}
}

func TestBytesTrimSpace(t *testing.T) {
	got := string(bytesTrimSpace([]byte("  \n\t hello  \r\n")))
	if got != "hello" {
		t.Errorf("trim: want %q, got %q", "hello", got)
	}
	if got := bytesTrimSpace([]byte("   ")); len(got) != 0 {
		t.Errorf("all-space should yield empty, got %q", got)
	}
}

// --- helpers ---

func flagPresent(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func flagHasValue(args []string, flag, val string) bool {
	for i, a := range args {
		if a == flag && i+1 < len(args) && args[i+1] == val {
			return true
		}
	}
	return false
}

func flagValue(args []string, flag string) string {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
