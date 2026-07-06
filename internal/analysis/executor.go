package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"

	"claude-manager/internal/proc"
)

// subtaskSummaryLimit caps the summary stored per subtask. The summary is
// re-sent to every later group via --append-system-prompt (context handoff),
// so an unbounded result text would bloat all downstream prompts.
const subtaskSummaryLimit = 700

// CLIExecutor runs each plan subtask as a one-shot `claude -p` invocation
// (PLAN.md section 17.8) and satisfies SubtaskExecutor. Zero-value fields
// fall back to defaults in BuildSubtaskArgs.
type CLIExecutor struct {
	ClaudePath    string // claude binary (default "claude")
	DefaultModel  string // model when the subtask has none (default "sonnet")
	DefaultEffort string // effort when the subtask has none (default "medium")
}

// BuildSubtaskArgs constructs the argv for one subtask run. The subtask prompt
// is the final positional argument; contextAppend (the rolling summary of
// prior groups) is forwarded via --append-system-prompt when present.
func BuildSubtaskArgs(e CLIExecutor, sub PlannedSubtask, contextAppend string) []string {
	model := sub.Model
	if model == "" {
		model = e.DefaultModel
	}
	if model == "" {
		model = "sonnet"
	}
	effort := sub.Effort
	if effort == "" {
		effort = e.DefaultEffort
	}
	if effort == "" {
		effort = "medium"
	}

	args := []string{
		"-p",
		"--model", model,
		"--effort", effort,
		"--permission-mode", "acceptEdits",
		"--output-format", "json",
	}
	if contextAppend != "" {
		args = append(args, "--append-system-prompt", contextAppend)
	}
	args = append(args, sub.Prompt)
	return args
}

// subtaskEnvelope matches the `--output-format json` result wrapper of a
// one-shot run: session id, cost and token usage live on the envelope, the
// assistant's final text is in `result`.
type subtaskEnvelope struct {
	Type         string  `json:"type"`
	Result       string  `json:"result"`
	IsError      bool    `json:"is_error"`
	Error        string  `json:"error"`
	SessionID    string  `json:"session_id"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Usage        struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

// ParseSubtaskOutput decodes the one-shot CLI stdout into a SubtaskResult.
func ParseSubtaskOutput(out []byte) (*SubtaskResult, error) {
	if len(bytesTrimSpace(out)) == 0 {
		return nil, errors.New("subtask: empty CLI output")
	}
	var env subtaskEnvelope
	if err := json.Unmarshal(out, &env); err != nil {
		return nil, fmt.Errorf("subtask: decode CLI output: %w", err)
	}
	if env.IsError {
		msg := env.Error
		if msg == "" {
			msg = "subtask session reported error"
		}
		return nil, fmt.Errorf("subtask: %s", msg)
	}

	summary := env.Result
	if runes := []rune(summary); len(runes) > subtaskSummaryLimit {
		summary = string(runes[:subtaskSummaryLimit]) + "…"
	}
	return &SubtaskResult{
		SessionID:    env.SessionID,
		Summary:      summary,
		CostUSD:      env.TotalCostUSD,
		InputTokens:  env.Usage.InputTokens,
		OutputTokens: env.Usage.OutputTokens,
	}, nil
}

// Execute satisfies SubtaskExecutor: run the subtask as a one-shot claude
// process in projectPath and report its result.
func (e *CLIExecutor) Execute(ctx context.Context, projectPath string, sub PlannedSubtask, contextAppend string) (*SubtaskResult, error) {
	bin := e.ClaudePath
	if bin == "" {
		bin = "claude"
	}

	cmd := exec.CommandContext(ctx, bin, BuildSubtaskArgs(*e, sub, contextAppend)...)
	proc.HideConsole(cmd)
	if projectPath != "" {
		cmd.Dir = projectPath
	}

	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("subtask %s: claude exited: %w: %s", sub.ID, err, string(ee.Stderr))
		}
		return nil, fmt.Errorf("subtask %s: claude exited: %w", sub.ID, err)
	}
	return ParseSubtaskOutput(out)
}
