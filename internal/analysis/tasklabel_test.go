package analysis

import (
	"strings"
	"testing"

	"claude-manager/internal/store"
)

func TestTaskLabelSchemaListsEveryLabel(t *testing.T) {
	sch := TaskLabelJSONSchema()
	for _, l := range append([]string{LabelNone}, store.TaskLabelNames...) {
		if !strings.Contains(sch, `"`+l+`"`) {
			t.Errorf("schema misses %q: %s", l, sch)
		}
	}
}

func TestBuildTaskLabelArgsUsesCheapestModel(t *testing.T) {
	args := strings.Join(BuildTaskLabelArgs(AnalysisConfig{}), " ")
	if !strings.Contains(args, "--model haiku") || !strings.Contains(args, "--effort low") {
		t.Errorf("defaults must be the cheapest: %s", args)
	}
	if got := strings.Join(BuildTaskLabelArgs(AnalysisConfig{Model: "sonnet"}), " "); !strings.Contains(got, "--model sonnet") {
		t.Errorf("model override lost: %s", got)
	}
}

func TestParseTaskLabelOutput(t *testing.T) {
	wrapped := `{"type":"result","is_error":false,"total_cost_usd":0.002,"usage":{"input_tokens":120,"output_tokens":30},` +
		`"result":"{\"label\":\"tests_fitted_to_code\",\"reason\":\"foo_test.go skips the case\"}"}`
	res, err := ParseTaskLabelOutput([]byte(wrapped))
	if err != nil || res.Label != store.LabelTestsFitted || res.InputTokens != 120 || res.OutputTokens != 30 || res.CostUSD != 0.002 {
		t.Fatalf("wrapped = %+v %v", res, err)
	}
	res, err = ParseTaskLabelOutput([]byte(`{"label":"none","reason":"nothing visible"}`))
	if err != nil || res.Label != LabelNone {
		t.Fatalf("bare none = %+v %v", res, err)
	}
	for _, bad := range []string{
		``, `{"label":"sloppy","reason":"x"}`, `{"label":"","reason":"x"}`, `not json`,
		`{"type":"result","is_error":true,"result":"{}"}`,
	} {
		if r, err := ParseTaskLabelOutput([]byte(bad)); err == nil {
			t.Errorf("%q must be refused, got %+v", bad, r)
		}
	}
}

func TestTaskLabelPromptCapsLongMaterial(t *testing.T) {
	long := strings.Repeat("x", 50000)
	p := taskLabelPrompt(TaskLabelInput{TaskText: long, Outcome: "gates red", Feedback: long, Diff: long})
	if len([]rune(p)) > maxLabelTaskRunes+maxLabelOutputRunes+maxLabelDiffRunes+500 {
		t.Errorf("prompt is %d runes, the excerpt caps are not applied", len([]rune(p)))
	}
	if !strings.Contains(p, "gates red") {
		t.Error("outcome line missing")
	}
}
