package analysis

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"claude-manager/internal/proc"
)

// AnalysisConfig controls a single RunAnalysis invocation. Zero values fall
// back to defaults from PLAN.md section 17.6.
type AnalysisConfig struct {
	ClaudePath   string  // override the `claude` binary path (default: "claude")
	Model        string  // analyst model (default: "haiku")
	Effort       string  // analyst effort (default: "medium")
	MaxBudgetUSD float64 // 0 = no limit (--max-budget-usd is omitted)
	SystemPrompt string  // override AnalystSystemPrompt; empty uses default
	JSONSchema   string  // override AnalysisJSONSchema; empty uses default
}

// FeasibilityInfo mirrors the `feasibility` block of the analyst JSON output.
type FeasibilityInfo struct {
	SingleSession          bool     `json:"single_session"`
	Confidence             float64  `json:"confidence"`
	Reasoning              string   `json:"reasoning"`
	EstimatedComplexity    string   `json:"estimated_complexity"`
	EstimatedFilesAffected int      `json:"estimated_files_affected"`
	EstimatedTokens        int      `json:"estimated_tokens"`
	Risks                  []string `json:"risks"`
}

// AnalysisResult is the decoded structured response from the analyst session.
// Shape matches PLAN.md section 17.5.
type AnalysisResult struct {
	Feasibility         FeasibilityInfo  `json:"feasibility"`
	RecommendedApproach string           `json:"recommended_approach"`
	RecommendedModel    string           `json:"recommended_model"`
	RecommendedEffort   string           `json:"recommended_effort"`
	Subtasks            []PlannedSubtask `json:"subtasks"`
	ExecutionOrder      [][]string       `json:"execution_order"`
	SharedContext       string           `json:"shared_context"`

	// CostUSD is the cost of the analyst run itself, extracted from the
	// outer result wrapper. Zero if the wrapper did not include it.
	CostUSD float64 `json:"-"`
}

// resultWrapper matches the outer envelope produced by `--output-format json`.
// The `result` field can be either an already-decoded object or a JSON string
// that must be re-parsed (older CLI versions).
type resultWrapper struct {
	Type         string          `json:"type"`
	Subtype      string          `json:"subtype"`
	Result       json.RawMessage `json:"result"`
	TotalCostUSD float64         `json:"total_cost_usd"`
	IsError      bool            `json:"is_error"`
	Error        string          `json:"error"`
}

// BuildAnalysisArgs constructs the argv passed to the Claude CLI for a
// pre-flight analysis run. The task text is the final positional argument.
func BuildAnalysisArgs(cfg AnalysisConfig, task string) []string {
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
		sysPrompt = AnalystSystemPrompt
	}
	schema := cfg.JSONSchema
	if schema == "" {
		schema = AnalysisJSONSchema
	}

	args := []string{
		"-p",
		"--model", model,
		"--effort", effort,
		"--permission-mode", "plan",
		"--json-schema", schema,
		"--output-format", "json",
		"--append-system-prompt", sysPrompt,
	}
	if cfg.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd",
			strconv.FormatFloat(cfg.MaxBudgetUSD, 'f', -1, 64))
	}
	args = append(args, "Analyze this task: "+task)
	return args
}

// ParseAnalysisOutput decodes the raw CLI stdout into an AnalysisResult.
// It accepts either the bare structured object or the result-wrapper envelope
// produced by `--output-format json`.
func ParseAnalysisOutput(out []byte) (*AnalysisResult, error) {
	if len(out) == 0 {
		return nil, errors.New("analysis: empty CLI output")
	}

	// First try the wrapper envelope.
	var wrap resultWrapper
	if err := json.Unmarshal(out, &wrap); err == nil && wrap.Type != "" {
		if wrap.IsError {
			msg := wrap.Error
			if msg == "" {
				msg = "analyst reported error"
			}
			return nil, fmt.Errorf("analysis: %s", msg)
		}
		if len(wrap.Result) == 0 {
			return nil, errors.New("analysis: wrapper has empty result field")
		}
		res, err := decodeAnalysisPayload(wrap.Result)
		if err != nil {
			return nil, err
		}
		res.CostUSD = wrap.TotalCostUSD
		return res, nil
	}

	// Fallback: assume the entire output is the structured object.
	return decodeAnalysisPayload(out)
}

// decodeAnalysisPayload handles both `result: {...}` (object) and
// `result: "{...}"` (string) shapes.
func decodeAnalysisPayload(raw json.RawMessage) (*AnalysisResult, error) {
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("analysis: empty payload")
	}

	// If the payload is a JSON string, unwrap it first.
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("analysis: unwrap string payload: %w", err)
		}
		trimmed = []byte(s)
	}

	var res AnalysisResult
	if err := json.Unmarshal(trimmed, &res); err != nil {
		return nil, fmt.Errorf("analysis: decode payload: %w", err)
	}
	return &res, nil
}

// bytesTrimSpace removes leading/trailing ASCII whitespace from a json.RawMessage.
func bytesTrimSpace(b []byte) []byte {
	i, j := 0, len(b)
	for i < j && isSpace(b[i]) {
		i++
	}
	for j > i && isSpace(b[j-1]) {
		j--
	}
	return b[i:j]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// RunAnalysis launches the analyst session as a one-shot Claude CLI invocation
// and returns the decoded AnalysisResult. The process inherits projectPath as
// its working directory so the analyst can inspect the project files.
func RunAnalysis(ctx context.Context, projectPath, task string, cfg AnalysisConfig) (*AnalysisResult, error) {
	bin := cfg.ClaudePath
	if bin == "" {
		bin = "claude"
	}

	args := BuildAnalysisArgs(cfg, task)
	cmd := exec.CommandContext(ctx, bin, args...)
	proc.HideConsole(cmd)
	if projectPath != "" {
		cmd.Dir = projectPath
	}

	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("analysis: claude exited: %w: %s", err, string(ee.Stderr))
		}
		return nil, fmt.Errorf("analysis: claude exited: %w", err)
	}

	return ParseAnalysisOutput(out)
}

// ProgressFunc receives a short line of activity (assistant text or a tool
// name) while a streaming analysis run is in flight. RunAnalysis's
// `--output-format json` gives the caller nothing at all until the whole CLI
// process exits, which for a Opus roadmap decomposition can be many minutes
// of total silence with no way to tell a slow run from a hung one.
type ProgressFunc func(text string)

// buildStreamingAnalysisArgs mirrors BuildAnalysisArgs but requests
// `--output-format stream-json` (plus `--verbose`, which the CLI requires for
// stream-json output) so RunAnalysisStreaming can report progress as it goes.
func buildStreamingAnalysisArgs(cfg AnalysisConfig, task string) []string {
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
		sysPrompt = AnalystSystemPrompt
	}
	schema := cfg.JSONSchema
	if schema == "" {
		schema = AnalysisJSONSchema
	}

	args := []string{
		"-p",
		"--verbose",
		"--model", model,
		"--effort", effort,
		"--permission-mode", "plan",
		"--json-schema", schema,
		"--output-format", "stream-json",
		"--append-system-prompt", sysPrompt,
	}
	if cfg.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd",
			strconv.FormatFloat(cfg.MaxBudgetUSD, 'f', -1, 64))
	}
	args = append(args, "Analyze this task: "+task)
	return args
}

// summarizeAssistantLine extracts a short human-readable progress line from
// one `assistant` stream-json event: the tool name for a tool_use block, or
// the (truncated) text for a text block. Returns "" for anything it can't
// summarize, so the caller can skip emitting a progress update for that line.
func summarizeAssistantLine(line []byte) string {
	var msg struct {
		Message struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Name string `json:"name"`
			} `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return ""
	}
	for _, c := range msg.Message.Content {
		switch c.Type {
		case "tool_use":
			if c.Name != "" {
				return "-> " + c.Name
			}
		case "text":
			t := strings.TrimSpace(c.Text)
			if t == "" {
				continue
			}
			const maxLen = 140
			if len(t) > maxLen {
				t = t[:maxLen] + "…"
			}
			return t
		}
	}
	return ""
}

// RunAnalysisStreaming behaves like RunAnalysis but requests stream-json
// output and reports lightweight progress (assistant text / tool calls)
// through onProgress as they arrive, instead of leaving the caller with total
// silence until the CLI process exits. onProgress may be nil. The final
// AnalysisResult is decoded from the stream's terminating `result` event,
// which carries the same wrapper shape ParseAnalysisOutput already handles.
func RunAnalysisStreaming(ctx context.Context, projectPath, task string, cfg AnalysisConfig, onProgress ProgressFunc) (*AnalysisResult, error) {
	bin := cfg.ClaudePath
	if bin == "" {
		bin = "claude"
	}

	args := buildStreamingAnalysisArgs(cfg, task)
	cmd := exec.CommandContext(ctx, bin, args...)
	proc.HideConsole(cmd)
	if projectPath != "" {
		cmd.Dir = projectPath
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("analysis: stdout pipe: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("analysis: start: %w", err)
	}

	var resultLine []byte
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(line, &probe); err != nil {
			continue
		}
		switch probe.Type {
		case "assistant":
			if onProgress != nil {
				if text := summarizeAssistantLine(line); text != "" {
					onProgress(text)
				}
			}
		case "result":
			resultLine = append([]byte(nil), line...)
		}
	}
	scanErr := scanner.Err()

	waitErr := cmd.Wait()
	if waitErr != nil {
		if stderrBuf.Len() > 0 {
			return nil, fmt.Errorf("analysis: claude exited: %w: %s", waitErr, stderrBuf.String())
		}
		return nil, fmt.Errorf("analysis: claude exited: %w", waitErr)
	}
	if scanErr != nil {
		return nil, fmt.Errorf("analysis: read stdout: %w", scanErr)
	}
	if resultLine == nil {
		return nil, errors.New("analysis: no result event in stream")
	}

	return ParseAnalysisOutput(resultLine)
}
