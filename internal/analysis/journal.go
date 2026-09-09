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

// JournalInput bundles what GenerateJournalEntry needs to describe one
// completed run to the distillation model (LEARN-TASKS.md LN-06): the task
// pointer, the files it touched, the run's final `result` text and — when
// the run's own gate command failed — that command's output. FailedGateOutput
// is best-effort: a manager-driven task_source session runs its own gates
// inside the CLI conversation (see docs/git-workflow.md), so the manager has
// no structured signal of a gate failure to pass here; it is populated only
// by callers that do run gates themselves (e.g. a future mixed-programming
// integration).
type JournalInput struct {
	TaskPtr          string
	FilesChanged     []string
	ResultText       string
	FailedGateOutput string
}

// JournalResult is the decoded structured response from a journal-distillation
// session (JournalJSONSchema).
type JournalResult struct {
	Done      string   `json:"done"`
	Surprises []string `json:"surprises,omitempty"`
	Avoid     []string `json:"avoid,omitempty"`

	// CostUSD is the cost of the distillation run itself, extracted from the
	// outer result wrapper. Zero if the wrapper did not include it.
	CostUSD float64 `json:"-"`
}

// buildJournalTaskText composes JournalInput into the plain-text task Claude
// is asked to summarize. Returns "" when there is nothing to summarize at all
// (no caller should invoke the CLI in that case).
func buildJournalTaskText(in JournalInput) string {
	var b strings.Builder
	if in.TaskPtr != "" {
		b.WriteString("Task: " + in.TaskPtr + "\n\n")
	}
	if len(in.FilesChanged) > 0 {
		b.WriteString("Files changed:\n" + strings.Join(in.FilesChanged, "\n") + "\n\n")
	}
	if strings.TrimSpace(in.ResultText) != "" {
		b.WriteString("Final result text:\n" + in.ResultText + "\n\n")
	}
	if strings.TrimSpace(in.FailedGateOutput) != "" {
		b.WriteString("Failed gate output:\n" + in.FailedGateOutput + "\n\n")
	}
	return strings.TrimSpace(b.String())
}

// BuildJournalArgs constructs the argv passed to the Claude CLI for a
// journal-distillation run. Mirrors BuildBriefArgs/BuildAnalysisArgs.
func BuildJournalArgs(cfg AnalysisConfig, task string) []string {
	model := cfg.Model
	if model == "" {
		model = "haiku"
	}
	effort := cfg.Effort
	if effort == "" {
		effort = "medium"
	}
	sysPrompt := cfg.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = JournalSystemPrompt
	}

	args := []string{
		"-p",
		"--model", model,
		"--effort", effort,
		"--permission-mode", "plan",
		"--json-schema", JournalJSONSchema,
		"--output-format", "json",
		"--append-system-prompt", sysPrompt,
	}
	if cfg.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd",
			strconv.FormatFloat(cfg.MaxBudgetUSD, 'f', -1, 64))
	}
	args = append(args, "Summarize this completed task for the project journal:\n\n"+task)
	return args
}

// ParseJournalOutput decodes raw CLI stdout into a JournalResult. It accepts
// either the bare structured object or the result-wrapper envelope produced
// by `--output-format json` (mirrors ParseAnalysisOutput/ParseBriefOutput).
func ParseJournalOutput(out []byte) (*JournalResult, error) {
	if len(out) == 0 {
		return nil, errors.New("journal: empty CLI output")
	}

	var wrap resultWrapper
	if err := json.Unmarshal(out, &wrap); err == nil && wrap.Type != "" {
		if wrap.IsError {
			msg := wrap.Error
			if msg == "" {
				msg = "journal distillation reported error"
			}
			return nil, fmt.Errorf("journal: %s", msg)
		}
		if len(wrap.Result) == 0 {
			return nil, errors.New("journal: wrapper has empty result field")
		}
		res, err := decodeJournalPayload(wrap.Result)
		if err != nil {
			return nil, err
		}
		res.CostUSD = wrap.TotalCostUSD
		return res, nil
	}

	return decodeJournalPayload(out)
}

func decodeJournalPayload(raw json.RawMessage) (*JournalResult, error) {
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("journal: empty payload")
	}

	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("journal: unwrap string payload: %w", err)
		}
		trimmed = []byte(s)
	}

	var res JournalResult
	if err := json.Unmarshal(trimmed, &res); err != nil {
		return nil, fmt.Errorf("journal: decode payload: %w", err)
	}
	return &res, nil
}

// GenerateJournalEntry runs a preflight-style session (haiku — cheap, called
// after every completed task) that distills in into a short journal entry
// (LEARN-TASKS.md LN-06). Returns an error without invoking the CLI at all
// when there is nothing to summarize.
func GenerateJournalEntry(ctx context.Context, projectPath string, in JournalInput, cfg AnalysisConfig) (*JournalResult, error) {
	task := buildJournalTaskText(in)
	if task == "" {
		return nil, errors.New("journal: nothing to summarize")
	}

	bin := cfg.ClaudePath
	if bin == "" {
		bin = "claude"
	}

	cmd := exec.CommandContext(ctx, bin, BuildJournalArgs(cfg, task)...)
	proc.HideConsole(cmd)
	if projectPath != "" {
		cmd.Dir = projectPath
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("journal: claude exited: %w: %s", err, string(ee.Stderr))
		}
		return nil, fmt.Errorf("journal: claude exited: %w", err)
	}

	return ParseJournalOutput(out)
}
