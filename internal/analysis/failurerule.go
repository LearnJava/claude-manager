package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"claude-manager/internal/proc"
)

// DefaultFailureRuleModel drafts the one-line CLAUDE.md rule (LEARN-TASKS.md
// LN-38). The text is a single sentence the user reviews, so the mid-size
// model is enough.
const DefaultFailureRuleModel = "sonnet"

// maxFailureRuleField caps every example field sent to the model, in runes.
const maxFailureRuleField = 300

// FailureRuleExample is one occurrence of a recurring error: the call that
// failed, its error line, and what ran successfully afterwards.
type FailureRuleExample struct {
	FailedArg string
	ErrText   string
	FixedArg  string
}

// FailureRuleInput is everything the drafting model sees about one error.
type FailureRuleInput struct {
	ErrorKey     string
	DistinctRuns int
	Examples     []FailureRuleExample
}

// FailureRuleDraft is the model's proposal. Duplicate is set when CLAUDE.md
// (global or the project's) already says the same thing.
type FailureRuleDraft struct {
	Rule      string  `json:"rule"`
	Duplicate bool    `json:"duplicate"`
	CostUSD   float64 `json:"-"`
}

// FailureRuleJSONSchema is the drafting call's structured output.
const FailureRuleJSONSchema = `{
  "type": "object",
  "properties": {
    "rule": {"type": "string", "maxLength": 300},
    "duplicate": {"type": "boolean"}
  },
  "required": ["rule", "duplicate"],
  "additionalProperties": false
}`

// FailureRuleSystemPrompt asks for ONE rule line.
const FailureRuleSystemPrompt = `You turn a recurring tool error of a Claude Code project into ONE line for the project's CLAUDE.md.

Write the line in the imperative mood: a fact about the project or an order of actions that prevents this error (a path, a flag, a command, a precondition). Write it in the language CLAUDE.md of the project is written in. No quotes, no markdown list marker, no explanation, a single sentence.

Read the CLAUDE.md of the project and the global one. If either already says this, set duplicate to true (and still give the rule).`

func truncRunes(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n]) + "…"
}

func buildFailureRuleTask(in FailureRuleInput) string {
	var b strings.Builder
	b.WriteString("Error: " + truncRunes(in.ErrorKey, maxFailureRuleField) + "\n")
	b.WriteString(fmt.Sprintf("Seen in %d distinct runs.\n", in.DistinctRuns))
	for i, e := range in.Examples {
		b.WriteString(fmt.Sprintf("\nExample %d:\n", i+1))
		b.WriteString("- failed call: " + truncRunes(e.FailedArg, maxFailureRuleField) + "\n")
		b.WriteString("- error: " + truncRunes(e.ErrText, maxFailureRuleField) + "\n")
		if e.FixedArg != "" {
			b.WriteString("- what worked afterwards: " + truncRunes(e.FixedArg, maxFailureRuleField) + "\n")
		}
	}
	return b.String()
}

// BuildFailureRuleArgs constructs the drafting call's argv. The prompt is not
// in it: it travels on stdin (setPromptStdin).
func BuildFailureRuleArgs(cfg AnalysisConfig) []string {
	model := cfg.Model
	if model == "" {
		model = DefaultFailureRuleModel
	}
	effort := cfg.Effort
	if effort == "" {
		effort = "low"
	}
	args := []string{
		"-p",
		"--model", model,
		"--effort", effort,
		"--permission-mode", "plan",
		"--json-schema", FailureRuleJSONSchema,
		"--output-format", "json",
		"--append-system-prompt", FailureRuleSystemPrompt,
	}
	if cfg.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(cfg.MaxBudgetUSD, 'f', -1, 64))
	}
	return args
}

func failureRulePrompt(in FailureRuleInput) string {
	return "Write the CLAUDE.md rule for this recurring error:\n\n" + buildFailureRuleTask(in)
}

// ParseFailureRuleOutput decodes the CLI stdout — the bare object or the
// `--output-format json` result wrapper.
func ParseFailureRuleOutput(out []byte) (*FailureRuleDraft, error) {
	if len(out) == 0 {
		return nil, errors.New("failure rule: empty CLI output")
	}
	var wrap resultWrapper
	if err := json.Unmarshal(out, &wrap); err == nil && wrap.Type != "" {
		if wrap.IsError {
			msg := wrap.Error
			if msg == "" {
				msg = "failure rule reported error"
			}
			return nil, fmt.Errorf("failure rule: %s", msg)
		}
		if len(wrap.Result) == 0 {
			return nil, errors.New("failure rule: wrapper has empty result field")
		}
		res, err := decodeFailureRulePayload(wrap.Result)
		if err != nil {
			return nil, err
		}
		res.CostUSD = wrap.TotalCostUSD
		return res, nil
	}
	return decodeFailureRulePayload(out)
}

func decodeFailureRulePayload(raw json.RawMessage) (*FailureRuleDraft, error) {
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("failure rule: empty payload")
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("failure rule: unwrap string payload: %w", err)
		}
		trimmed = []byte(s)
	}
	var res FailureRuleDraft
	if err := json.Unmarshal(trimmed, &res); err != nil {
		return nil, fmt.Errorf("failure rule: decode payload: %w", err)
	}
	res.Rule = strings.TrimSpace(res.Rule)
	if res.Rule == "" {
		return nil, errors.New("failure rule: the model returned an empty rule")
	}
	return &res, nil
}

// DraftFailureRule asks the model for a one-line CLAUDE.md rule that would
// have prevented the error (LEARN-TASKS.md LN-38). It writes nothing.
func DraftFailureRule(ctx context.Context, projectPath string, in FailureRuleInput, cfg AnalysisConfig) (*FailureRuleDraft, error) {
	if strings.TrimSpace(in.ErrorKey) == "" {
		return nil, errors.New("failure rule: empty error key")
	}
	bin := cfg.ClaudePath
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(ctx, bin, BuildFailureRuleArgs(cfg)...)
	setPromptStdin(cmd, failureRulePrompt(in))
	proc.HideConsole(cmd)
	if projectPath != "" {
		cmd.Dir = projectPath
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("failure rule: claude exited: %w: %s", err, string(ee.Stderr))
		}
		return nil, fmt.Errorf("failure rule: claude exited: %w", err)
	}
	return ParseFailureRuleOutput(out)
}
