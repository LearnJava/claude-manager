package analysis

import (
	"strings"
	"testing"
)

func TestBuildFailureRuleArgs_NoPromptInArgv(t *testing.T) {
	args := BuildFailureRuleArgs(AnalysisConfig{})
	joined := strings.Join(args, " ")
	for _, want := range []string{"-p", "--model sonnet", "--effort low", "--permission-mode plan", "--output-format json", "--json-schema"} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv lacks %q: %v", want, args)
		}
	}
	if strings.Contains(joined, "EACCES") {
		t.Errorf("prompt leaked into argv")
	}
}

func TestFailureRulePrompt_HasKeyAndExamples(t *testing.T) {
	p := failureRulePrompt(FailureRuleInput{
		ErrorKey: "EACCES open file", DistinctRuns: 4,
		Examples: []FailureRuleExample{{FailedArg: "cat /x", ErrText: "EACCES: denied", FixedArg: "cat /y"}},
	})
	for _, want := range []string{"EACCES open file", "4 distinct runs", "cat /x", "EACCES: denied", "cat /y"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt lacks %q:\n%s", want, p)
		}
	}
}

func TestFailureRulePrompt_TruncatesFields(t *testing.T) {
	long := strings.Repeat("я", 500)
	p := failureRulePrompt(FailureRuleInput{ErrorKey: "k", Examples: []FailureRuleExample{{FailedArg: long}}})
	if strings.Contains(p, strings.Repeat("я", 301)) {
		t.Errorf("field not truncated to 300 runes")
	}
}

func TestParseFailureRuleOutput(t *testing.T) {
	bare, err := ParseFailureRuleOutput([]byte(`{"rule":"Use X.","duplicate":true}`))
	if err != nil || bare.Rule != "Use X." || !bare.Duplicate {
		t.Fatalf("bare: %+v %v", bare, err)
	}
	wrapped, err := ParseFailureRuleOutput([]byte(`{"type":"result","total_cost_usd":0.02,"result":"{\"rule\":\"Use Y.\",\"duplicate\":false}"}`))
	if err != nil || wrapped.Rule != "Use Y." || wrapped.Duplicate || wrapped.CostUSD != 0.02 {
		t.Fatalf("wrapped: %+v %v", wrapped, err)
	}
	if _, err := ParseFailureRuleOutput([]byte(`{"rule":"","duplicate":false}`)); err == nil {
		t.Errorf("empty rule must fail")
	}
	if _, err := ParseFailureRuleOutput(nil); err == nil {
		t.Errorf("empty output must fail")
	}
}
