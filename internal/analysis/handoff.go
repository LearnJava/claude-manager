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

// HandoffInput bundles what GenerateHandoff needs to distill a compact recap
// of an in-flight task interrupted by a context-restart (LEARN-TASKS.md
// LN-15): the task pointer, the current TodoWrite state and the tail of the
// interrupted CLI session's own transcript. RecentSteps is best-effort — a
// backend that could not locate/read the transcript (e.g. fakeclaude in
// tests, which never writes one) leaves it empty rather than failing the
// whole distillation.
type HandoffInput struct {
	TaskPtr     string
	Todos       []string
	RecentSteps []string
}

// HandoffResult is the decoded structured response from a handoff-
// distillation session (HandoffJSONSchema).
type HandoffResult struct {
	Done         string   `json:"done"`
	Remaining    string   `json:"remaining"`
	Decisions    []string `json:"decisions,omitempty"`
	FilesChanged []string `json:"files_changed,omitempty"`

	// CostUSD is the cost of the distillation run itself, extracted from the
	// outer result wrapper. Zero if the wrapper did not include it.
	CostUSD float64 `json:"-"`
}

// buildHandoffTaskText composes HandoffInput into the plain-text task Claude
// is asked to summarize. Returns "" when there is nothing to summarize at all
// (no caller should invoke the CLI in that case).
func buildHandoffTaskText(in HandoffInput) string {
	var b strings.Builder
	if in.TaskPtr != "" {
		b.WriteString("Task: " + in.TaskPtr + "\n\n")
	}
	if len(in.Todos) > 0 {
		b.WriteString("Current TodoWrite state:\n" + strings.Join(in.Todos, "\n") + "\n\n")
	}
	if len(in.RecentSteps) > 0 {
		b.WriteString("Most recent actions in the interrupted session (oldest first):\n" +
			strings.Join(in.RecentSteps, "\n") + "\n\n")
	}
	return strings.TrimSpace(b.String())
}

// BuildHandoffArgs constructs the argv passed to the Claude CLI for a
// handoff-distillation run. Mirrors BuildJournalArgs/BuildBriefArgs.
func BuildHandoffArgs(cfg AnalysisConfig, task string) []string {
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
		sysPrompt = HandoffSystemPrompt
	}

	args := []string{
		"-p",
		"--model", model,
		"--effort", effort,
		"--permission-mode", "plan",
		"--json-schema", HandoffJSONSchema,
		"--output-format", "json",
		"--append-system-prompt", sysPrompt,
	}
	if cfg.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd",
			strconv.FormatFloat(cfg.MaxBudgetUSD, 'f', -1, 64))
	}
	args = append(args, "Summarize this interrupted task so a fresh session can continue it:\n\n"+task)
	return args
}

// ParseHandoffOutput decodes raw CLI stdout into a HandoffResult. It accepts
// either the bare structured object or the result-wrapper envelope produced
// by `--output-format json` (mirrors ParseJournalOutput).
func ParseHandoffOutput(out []byte) (*HandoffResult, error) {
	if len(out) == 0 {
		return nil, errors.New("handoff: empty CLI output")
	}

	var wrap resultWrapper
	if err := json.Unmarshal(out, &wrap); err == nil && wrap.Type != "" {
		if wrap.IsError {
			msg := wrap.Error
			if msg == "" {
				msg = "handoff distillation reported error"
			}
			return nil, fmt.Errorf("handoff: %s", msg)
		}
		if len(wrap.Result) == 0 {
			return nil, errors.New("handoff: wrapper has empty result field")
		}
		res, err := decodeHandoffPayload(wrap.Result)
		if err != nil {
			return nil, err
		}
		res.CostUSD = wrap.TotalCostUSD
		return res, nil
	}

	return decodeHandoffPayload(out)
}

func decodeHandoffPayload(raw json.RawMessage) (*HandoffResult, error) {
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("handoff: empty payload")
	}

	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("handoff: unwrap string payload: %w", err)
		}
		trimmed = []byte(s)
	}

	var res HandoffResult
	if err := json.Unmarshal(trimmed, &res); err != nil {
		return nil, fmt.Errorf("handoff: decode payload: %w", err)
	}
	return &res, nil
}

// GenerateHandoff runs a preflight-style session (haiku — cheap, called only
// when a context restart actually fires) that distills in into a compact
// recap of an interrupted task (LEARN-TASKS.md LN-15). Returns an error
// without invoking the CLI at all when there is nothing to summarize.
func GenerateHandoff(ctx context.Context, projectPath string, in HandoffInput, cfg AnalysisConfig) (*HandoffResult, error) {
	task := buildHandoffTaskText(in)
	if task == "" {
		return nil, errors.New("handoff: nothing to summarize")
	}

	bin := cfg.ClaudePath
	if bin == "" {
		bin = "claude"
	}

	cmd := exec.CommandContext(ctx, bin, BuildHandoffArgs(cfg, task)...)
	proc.HideConsole(cmd)
	if projectPath != "" {
		cmd.Dir = projectPath
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("handoff: claude exited: %w: %s", err, string(ee.Stderr))
		}
		return nil, fmt.Errorf("handoff: claude exited: %w", err)
	}

	return ParseHandoffOutput(out)
}
