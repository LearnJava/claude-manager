package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestSkillJSONSchemaIsValidJSON(t *testing.T) {
	var v map[string]any
	if err := json.Unmarshal([]byte(SkillJSONSchema), &v); err != nil {
		t.Fatalf("SkillJSONSchema is not valid JSON: %v", err)
	}
	if v["type"] != "object" {
		t.Errorf("schema root type should be object, got %v", v["type"])
	}
	props, ok := v["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected a properties object")
	}
	for _, key := range []string{"name", "description", "when_to_use", "steps", "gotchas", "done_when", "files_touched"} {
		if _, ok := props[key]; !ok {
			t.Errorf("SkillJSONSchema missing property %q", key)
		}
	}
}

func TestBuildSkillStreamArgsDefaults(t *testing.T) {
	args := buildSkillStreamArgs(AnalysisConfig{}, "do the thing")

	want := map[string]string{
		"--model":           "sonnet",
		"--effort":          "medium",
		"--permission-mode": "plan",
		"--output-format":   "stream-json",
	}
	for flag, val := range want {
		if !flagHasValue(args, flag, val) {
			t.Errorf("missing flag %q = %q in args: %v", flag, val, args)
		}
	}
	if !flagHasValue(args, "--json-schema", SkillJSONSchema) {
		t.Error("expected default SkillJSONSchema")
	}
	if !flagHasValue(args, "--append-system-prompt", SkillSystemPrompt) {
		t.Error("expected default SkillSystemPrompt")
	}
	if !flagPresent(args, "--verbose") {
		t.Error("expected --verbose (required for stream-json output)")
	}
	last := args[len(args)-1]
	if !strings.Contains(last, "do the thing") {
		t.Errorf("expected task text in final positional arg, got %q", last)
	}
}

func TestBuildSkillStreamArgsOverrides(t *testing.T) {
	cfg := AnalysisConfig{Model: "opus", Effort: "high", MaxBudgetUSD: 1.5}
	args := buildSkillStreamArgs(cfg, "x")
	if !flagHasValue(args, "--model", "opus") {
		t.Error("expected model override")
	}
	if !flagHasValue(args, "--effort", "high") {
		t.Error("expected effort override")
	}
	if !flagHasValue(args, "--max-budget-usd", "1.5") {
		t.Error("expected --max-budget-usd=1.5")
	}
}

func TestBuildSkillTaskText(t *testing.T) {
	in := SkillDistillInput{
		Sig: []string{"Bash:git status", "Bash:git branch -a"},
		Samples: []SkillSample{
			{Command: "git status --short", Output: "## main\n M app.go"},
			{Command: ""}, // empty command skipped
		},
		RelatedFailures: []SkillFailureSummary{
			{ErrorKey: "fatal: not a git repository", FailedArg: "git status", FixedArg: "cd repo && git status"},
		},
		Gates: []string{"go build ./...", "go test ./..."},
	}
	got := buildSkillTaskText(in)

	for _, want := range []string{
		"Bash:git status",
		"Bash:git branch -a",
		"$ git status --short",
		"## main",
		"fatal: not a git repository",
		"failed with `git status`",
		"fixed it with `cd repo && git status`",
		"go build ./...",
		"go test ./...",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("task text missing %q, got:\n%s", want, got)
		}
	}
}

func TestBuildSkillTaskTextEmpty(t *testing.T) {
	if got := buildSkillTaskText(SkillDistillInput{}); got != "" {
		t.Errorf("expected empty task text for empty input, got %q", got)
	}
}

func TestTruncateLines(t *testing.T) {
	if got := truncateLines("", 20); got != "" {
		t.Errorf("empty input should yield empty output, got %q", got)
	}
	short := "a\nb\nc"
	if got := truncateLines(short, 20); got != short {
		t.Errorf("under the limit should be unchanged: got %q", got)
	}
	var lines []string
	for i := 0; i < 25; i++ {
		lines = append(lines, "line")
	}
	long := strings.Join(lines, "\n")
	got := truncateLines(long, 20)
	if strings.Count(got, "\n") != 20 { // 20 kept lines -> 19 breaks + 1 before the marker
		t.Errorf("expected 20 lines kept, got %d newlines in %q", strings.Count(got, "\n"), got)
	}
	if !strings.HasSuffix(got, "(truncated)") {
		t.Errorf("expected truncation marker, got %q", got)
	}
}

func TestParseSkillOutputBareObject(t *testing.T) {
	raw := `{"name":"git-preamble","description":"Use at session start to orient in the repo.",
	  "steps":[{"command":"git status --short --branch","why":"see branch and dirty files"}],
	  "done_when":"branch and status are known"}`
	res, err := ParseSkillOutput([]byte(raw))
	if err != nil {
		t.Fatalf("ParseSkillOutput: %v", err)
	}
	if res.Name != "git-preamble" {
		t.Errorf("Name: got %q", res.Name)
	}
	if len(res.Steps) != 1 || res.Steps[0].Command != "git status --short --branch" {
		t.Errorf("Steps: got %+v", res.Steps)
	}
	if res.CostUSD != 0 {
		t.Errorf("bare object has no cost info, got %v", res.CostUSD)
	}
}

func TestParseSkillOutputResultWrapperObject(t *testing.T) {
	raw := `{"type":"result","subtype":"success","total_cost_usd":0.02,
	  "result":{"name":"n","description":"d","steps":[{"command":"c"}],"done_when":"x"}}`
	res, err := ParseSkillOutput([]byte(raw))
	if err != nil {
		t.Fatalf("ParseSkillOutput: %v", err)
	}
	if res.Name != "n" || res.CostUSD != 0.02 {
		t.Errorf("unexpected result: %+v", res)
	}
}

func TestParseSkillOutputResultWrapperString(t *testing.T) {
	inner := `{"name":"n","description":"d","steps":[{"command":"c"}],"done_when":"x"}`
	raw := `{"type":"result","result":` + strconvQuote(inner) + `,"total_cost_usd":0.01}`
	res, err := ParseSkillOutput([]byte(raw))
	if err != nil {
		t.Fatalf("ParseSkillOutput: %v", err)
	}
	if res.Name != "n" {
		t.Errorf("Name: got %q", res.Name)
	}
}

func TestParseSkillOutputErrors(t *testing.T) {
	if _, err := ParseSkillOutput(nil); err == nil {
		t.Error("expected error on empty input")
	}
	if _, err := ParseSkillOutput([]byte(`{"type":"result","is_error":true,"error":"bad"}`)); err == nil {
		t.Error("expected error when is_error=true")
	}
	if _, err := ParseSkillOutput([]byte(`{not valid`)); err == nil {
		t.Error("expected error on malformed json")
	}
}

func TestTitleFromName(t *testing.T) {
	cases := map[string]string{
		"git-session-preamble": "Git session preamble",
		"single":               "Single",
		"":                     "",
	}
	for in, want := range cases {
		if got := titleFromName(in); got != want {
			t.Errorf("titleFromName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderSkillMarkdownGolden(t *testing.T) {
	d := SkillDraft{
		Name:        "git-session-preamble",
		Description: "Use at the start of a task_source session to orient in the repo before doing anything else.",
		WhenToUse:   []string{"Starting a new p<N>-task session"},
		Steps: []SkillStep{
			{Command: "git pull origin master", Why: "sync before reading any *-STATUS.md"},
			{Command: "git branch -a"},
		},
		Gotchas:      []string{"An existing p<N>-... branch is your own interrupted task — continue it."},
		DoneWhen:     "the current branch is your p<N>-... branch, not master",
		FilesTouched: []string{"LEARN-STATUS.md"},
	}
	got := RenderSkillMarkdown(d)

	want := `---
name: git-session-preamble
description: Use at the start of a task_source session to orient in the repo before doing anything else.
---

# Git session preamble

## When to use

- Starting a new p<N>-task session

## Steps

1. ` + "`git pull origin master`" + ` — sync before reading any *-STATUS.md
2. ` + "`git branch -a`" + `

## Gotchas

- An existing p<N>-... branch is your own interrupted task — continue it.

## Done when

the current branch is your p<N>-... branch, not master

## Files touched

- LEARN-STATUS.md
`
	if got != want {
		t.Errorf("RenderSkillMarkdown mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderSkillMarkdownOmitsEmptySections(t *testing.T) {
	d := SkillDraft{
		Name:        "bare-skill",
		Description: "d",
		Steps:       []SkillStep{{Command: "echo hi"}},
		DoneWhen:    "hi printed",
	}
	got := RenderSkillMarkdown(d)
	for _, section := range []string{"## When to use", "## Gotchas", "## Files touched"} {
		if strings.Contains(got, section) {
			t.Errorf("expected %q to be omitted for empty input, got:\n%s", section, got)
		}
	}
	for _, want := range []string{"## Steps", "## Done when", "echo hi", "hi printed"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestRenderSkillMarkdownTruncatesBody(t *testing.T) {
	var steps []SkillStep
	for i := 0; i < 200; i++ {
		steps = append(steps, SkillStep{Command: "step"})
	}
	d := SkillDraft{Name: "long", Description: "d", Steps: steps, DoneWhen: "x"}
	got := RenderSkillMarkdown(d)

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) > maxSkillBodyLines+1 { // +1 for the truncation marker line
		t.Errorf("expected at most %d lines (+marker), got %d", maxSkillBodyLines, len(lines))
	}
	if !strings.Contains(got, "name: long") || !strings.Contains(got, "description: d") {
		t.Error("frontmatter must survive truncation")
	}
	if !strings.Contains(got, "truncated") {
		t.Error("expected a truncation marker")
	}
}

func TestDistillSkillBelowThreshold(t *testing.T) {
	_, err := DistillSkill(context.Background(), t.TempDir(),
		SkillDistillInput{Sig: []string{"Bash:git status"}}, 3, 10, AnalysisConfig{}, nil)
	if !errors.Is(err, ErrBelowThreshold) {
		t.Fatalf("expected ErrBelowThreshold, got %v", err)
	}
}

func TestDistillSkillDefaultThreshold(t *testing.T) {
	// score just under DefaultSkillMinScore, minScore <= 0 -> falls back to it.
	_, err := DistillSkill(context.Background(), t.TempDir(),
		SkillDistillInput{Sig: []string{"Bash:git status"}}, DefaultSkillMinScore-0.01, 0, AnalysisConfig{}, nil)
	if !errors.Is(err, ErrBelowThreshold) {
		t.Fatalf("expected ErrBelowThreshold, got %v", err)
	}
}

func TestDistillSkillClaudeUnavailableReturnsError(t *testing.T) {
	// Above threshold, so the CLI is actually attempted; the binary does not
	// exist, so this must return an error, never panic.
	_, err := DistillSkill(context.Background(), t.TempDir(),
		SkillDistillInput{Sig: []string{"Bash:git status"}}, 100, 1,
		AnalysisConfig{ClaudePath: "cm-no-such-claude-binary-xyz"}, nil)
	if err == nil {
		t.Fatal("expected an error when the claude binary does not exist")
	}
}

// strconvQuote avoids importing strconv just for one JSON-string test helper.
func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
