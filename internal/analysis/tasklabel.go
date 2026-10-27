package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"claude-manager/internal/proc"
	"claude-manager/internal/store"
)

// Quality labelling of a task that ended badly (WASTE-TASKS.md WS-11). One
// cheap call reads what is known about the task and picks one label from the
// fixed list (store.TaskLabelNames) or "none" when the evidence names no cause.

// LabelNone is the verdict "the material does not show what went wrong".
const LabelNone = "none"

// Caps on the text handed to the model: the judge is the cheapest model and
// its window is spent, not the task's, so it gets an excerpt, not everything.
const (
	maxLabelTaskRunes   = 4000
	maxLabelOutputRunes = 3000
	maxLabelDiffRunes   = 9000
)

// TaskLabelInput is what the judge is shown about one task that ended badly.
type TaskLabelInput struct {
	TaskText string
	Outcome  string // one line: how the task ended (gates still red, unfinished, ...)
	Feedback string // the red gates' output or the agent's last words
	Diff     string // changes in the work folder against the task's starting commit
}

// TaskLabelResult is the decoded verdict plus what the call cost.
type TaskLabelResult struct {
	Label  string `json:"label"`
	Reason string `json:"reason"`

	Model        string  `json:"-"`
	InputTokens  int64   `json:"-"`
	OutputTokens int64   `json:"-"`
	CostUSD      float64 `json:"-"`
}

// TaskLabelJSONSchema is the structured output of a labelling call; the label
// is an enum of the allowed labels plus LabelNone.
func TaskLabelJSONSchema() string {
	enum, _ := json.Marshal(append(append([]string{}, store.TaskLabelNames...), LabelNone))
	return `{"type":"object","properties":{` +
		`"label":{"type":"string","enum":` + string(enum) + `,"description":"The single most likely cause, or none if the material does not show it."},` +
		`"reason":{"type":"string","description":"One or two sentences naming the concrete evidence in the material."}` +
		`},"required":["label","reason"]}`
}

// TaskLabelSystemPrompt tells the judge what each label means.
const TaskLabelSystemPrompt = `You judge why one Claude Code task ended badly. You are shown the task, how it
ended, the last output and the changes made. Pick exactly one label:
- tests_fitted_to_code: tests were edited, loosened, skipped or special-cased so they pass, instead of fixing the code.
- protocol_broken: the project's working protocol was not followed (steps skipped, wrong branch, gates not run, marker misused).
- garbage_left: stray files, debug output, commented-out code or half-finished edits were left behind.
- wrong_task_done: the work does not match what the task asked for.
- none: the material does not show which of these happened. Prefer none to a guess.
Base the reason on concrete evidence from the material (a file, a line, a command). Write it in the language of the task.`

// BuildTaskLabelArgs is the argv of a labelling call: the cheapest model by
// default, low effort, no tools.
func BuildTaskLabelArgs(cfg AnalysisConfig) []string {
	model := cfg.Model
	if model == "" {
		model = "haiku"
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
		"--json-schema", TaskLabelJSONSchema(),
		"--output-format", "json",
		"--append-system-prompt", TaskLabelSystemPrompt,
	}
	return args
}

func tailRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return "…" + string(r[len(r)-n:])
	}
	return s
}

func headRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// taskLabelPrompt is the user prompt of a labelling call (it travels on stdin).
func taskLabelPrompt(in TaskLabelInput) string {
	var b strings.Builder
	b.WriteString("Task:\n" + headRunes(strings.TrimSpace(in.TaskText), maxLabelTaskRunes) + "\n\n")
	b.WriteString("How it ended: " + strings.TrimSpace(in.Outcome) + "\n\n")
	if f := strings.TrimSpace(in.Feedback); f != "" {
		b.WriteString("Last output:\n" + tailRunes(f, maxLabelOutputRunes) + "\n\n")
	}
	if d := strings.TrimSpace(in.Diff); d != "" {
		b.WriteString("Changes:\n" + headRunes(d, maxLabelDiffRunes) + "\n\n")
	}
	b.WriteString("Which label fits?")
	return b.String()
}

// labelWrapper is the `--output-format json` envelope, with the usage block
// resultWrapper leaves out.
type labelWrapper struct {
	Type         string          `json:"type"`
	Result       json.RawMessage `json:"result"`
	TotalCostUSD float64         `json:"total_cost_usd"`
	IsError      bool            `json:"is_error"`
	Usage        struct {
		InputTokens  int64 `json:"input_tokens"`
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

// ParseTaskLabelOutput decodes the CLI's stdout. A label outside the allowed
// list (and LabelNone) is an error.
func ParseTaskLabelOutput(out []byte) (*TaskLabelResult, error) {
	if len(out) == 0 {
		return nil, errors.New("task label: empty CLI output")
	}
	payload := json.RawMessage(out)
	var w labelWrapper
	haveWrap := json.Unmarshal(out, &w) == nil && w.Type != ""
	if haveWrap {
		if w.IsError || len(w.Result) == 0 {
			return nil, errors.New("task label: the judging call reported an error")
		}
		payload = w.Result
	}
	trimmed := bytesTrimSpace(payload)
	if len(trimmed) > 0 && trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("task label: unwrap payload: %w", err)
		}
		trimmed = []byte(s)
	}
	var res TaskLabelResult
	if err := json.Unmarshal(trimmed, &res); err != nil {
		return nil, fmt.Errorf("task label: decode payload: %w", err)
	}
	res.Label = strings.TrimSpace(res.Label)
	if res.Label != LabelNone && !store.ValidTaskLabel(res.Label) {
		return nil, fmt.Errorf("task label: %q is not an allowed label", res.Label)
	}
	if haveWrap {
		res.InputTokens, res.OutputTokens, res.CostUSD = w.Usage.InputTokens, w.Usage.OutputTokens, w.TotalCostUSD
	}
	return &res, nil
}

// GenerateTaskLabel runs one labelling call in projectPath.
func GenerateTaskLabel(ctx context.Context, projectPath string, in TaskLabelInput, cfg AnalysisConfig) (*TaskLabelResult, error) {
	bin := cfg.ClaudePath
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(ctx, bin, BuildTaskLabelArgs(cfg)...)
	setPromptStdin(cmd, taskLabelPrompt(in))
	proc.HideConsole(cmd)
	if projectPath != "" {
		cmd.Dir = projectPath
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("task label: claude exited: %w: %s", err, string(ee.Stderr))
		}
		return nil, fmt.Errorf("task label: claude exited: %w", err)
	}
	res, err := ParseTaskLabelOutput(out)
	if err != nil {
		return nil, err
	}
	res.Model = cfg.Model
	if res.Model == "" {
		res.Model = "haiku"
	}
	return res, nil
}
