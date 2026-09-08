// Package experience mines the app's own operational history — CLI
// transcripts, auto-saved logs, session outcomes — into things that save the
// next session tokens and time: permission allowlists, a warm context primer,
// and distilled skills. See LEARN-TASKS.md for the full plan.
//
// Every feature in this package is opt-in (default off) per the invariants in
// LEARN-TASKS.md — this file only reads transcripts already on disk; nothing
// here writes anywhere.
package experience

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"claude-manager/internal/session"
)

// StepKind identifies what a Step represents.
type StepKind string

const (
	StepToolUse  StepKind = "tool_use"
	StepText     StepKind = "text"
	StepThinking StepKind = "thinking"
)

// Step is one unit of a Trajectory: an assistant tool call, a piece of prose,
// or a thinking block, with its result (for tool calls) joined in. This is
// the common shape both ingest backends (JSONL transcripts here, auto-saved
// markdown logs in LN-17) produce — consumers must not assume every field is
// populated. Fields absent from the markdown backend (Usage, ToolUseID,
// Input) are simply zero there.
type Step struct {
	Index int
	Time  time.Time
	Kind  StepKind

	// ToolName/Input/ToolUseID are only set for Kind == StepToolUse.
	ToolName  string
	Input     json.RawMessage // raw tool_use input JSON; empty on the markdown backend
	InputText string          // one-line rendering: the Bash command / file path / pattern for
	// StepToolUse (same rule as session_logs.tool_input, via
	// session.AbbreviateInput); the prose itself for StepText/StepThinking.
	ToolUseID string

	// Result fields are only ever set for Kind == StepToolUse.
	ResultText    string
	ResultChars   int
	ResultIsError bool
	Stdout        string
	Stderr        string
	// ResultTime is the timestamp of the tool_result (or error) line that
	// closed this call — zero when no result ever arrived (a call still in
	// flight when the read window ended). Time and ResultTime together are
	// the manager's own measurement of how long the call actually took
	// (LEARN-TASKS.md LN-18); a naive parser has no other source for this,
	// since neither backend's tool_use event carries a duration itself.
	ResultTime time.Time

	Usage *session.TokenUsage
}

// Trajectory is one CLI session's timeline of steps.
type Trajectory struct {
	SessionID   string
	ProjectPath string
	Branch      string
	Steps       []Step
	Skipped     int
}

// TranscriptsRoot returns the directory the Claude CLI writes JSONL
// transcripts under. CM_TRANSCRIPTS_DIR overrides it — required for tests,
// since fakeclaude never writes to the real ~/.claude/projects/ (invariant 7,
// LEARN-TASKS.md).
func TranscriptsRoot() string {
	if v := os.Getenv("CM_TRANSCRIPTS_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// ProjectSlug mirrors the Claude CLI's project directory naming: every path
// separator and colon in projectPath becomes '-'.
// D:\GolangProjects\claude-manager -> D--GolangProjects-claude-manager.
func ProjectSlug(projectPath string) string {
	r := strings.NewReplacer(":", "-", "\\", "-", "/", "-")
	return r.Replace(projectPath)
}

// FindTranscript locates the JSONL transcript for a CLI session under root.
// It first tries the slug directory derived from projectPath (the common
// case), then falls back to scanning every subdirectory of root for
// <cliSessionID>.jsonl — the slug is a naming convention, not a guarantee
// (a project folder can move, or root can hold transcripts from elsewhere).
func FindTranscript(root, projectPath, cliSessionID string) (string, error) {
	if cliSessionID == "" {
		return "", fmt.Errorf("experience: empty CLI session id")
	}
	if root == "" {
		return "", fmt.Errorf("experience: empty transcripts root")
	}
	name := cliSessionID + ".jsonl"

	if projectPath != "" {
		direct := filepath.Join(root, ProjectSlug(projectPath), name)
		if fi, err := os.Stat(direct); err == nil && !fi.IsDir() {
			return direct, nil
		}
	}

	var found string
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || found != "" {
			return nil //nolint:nilerr // best-effort scan, unreadable entries are just skipped
		}
		if !d.IsDir() && d.Name() == name {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	if found == "" {
		return "", fmt.Errorf("experience: transcript for session %s not found under %s", cliSessionID, root)
	}
	return found, nil
}

// Read parses an entire transcript file from the start.
func Read(path string) (Trajectory, error) {
	traj, _, err := ReadFrom(path, 0)
	return traj, err
}

// ReadFrom parses the transcript at path starting at byte offset, returning
// the parsed steps and the offset to resume from next time. Only complete
// lines (terminated by '\n') advance the returned offset — an incomplete
// trailing line is left unconsumed, because the CLI may still be writing to
// this file concurrently with ingestion.
func ReadFrom(path string, offset int64) (Trajectory, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return Trajectory{}, offset, err
	}
	defer f.Close()

	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return Trajectory{}, offset, err
		}
	}

	var traj Trajectory
	toolIndex := make(map[string]int)
	newOffset := offset

	r := bufio.NewReaderSize(f, 64*1024)
	for {
		lineBytes, readErr := r.ReadBytes('\n')
		if readErr != nil && readErr != io.EOF {
			return traj, newOffset, readErr
		}
		complete := readErr == nil
		if !complete {
			// Either a fully-empty final read (nothing left) or a partial
			// last line with no trailing newline yet — neither is consumed.
			break
		}
		newOffset += int64(len(lineBytes))
		line := strings.TrimRight(string(lineBytes), "\r\n")
		if strings.TrimSpace(line) == "" {
			continue
		}
		processLine(&traj, toolIndex, line)
	}
	return traj, newOffset, nil
}

// rawLine is the envelope shared by every JSONL row. Only assistant/user rows
// carry a message; every other type (attachment, system, mode, last-prompt,
// permission-mode, atis-latch, ai-title, file-history-snapshot,
// file-history-delta, queue-operation, custom-title, agent-name, ...) is
// ignored — the set of types changes between CLI versions and none of the
// others carry a step.
type rawLine struct {
	Type          string            `json:"type"`
	Message       *rawMessage       `json:"message"`
	SessionID     string            `json:"sessionId"`
	Cwd           string            `json:"cwd"`
	GitBranch     string            `json:"gitBranch"`
	Timestamp     string            `json:"timestamp"`
	ToolUseResult *rawToolUseResult `json:"toolUseResult"`
}

type rawMessage struct {
	Role    string              `json:"role"`
	Content json.RawMessage     `json:"content"`
	Usage   *session.TokenUsage `json:"usage"`
}

// rawBlock covers both assistant content blocks (text/thinking/tool_use) and
// user content blocks (tool_result) — the two shapes never share a field, so
// one struct is simpler than two.
type rawBlock struct {
	Type string `json:"type"`

	// assistant: text / thinking
	Text     string `json:"text"`
	Thinking string `json:"thinking"`

	// assistant: tool_use
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`

	// user: tool_result
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
}

type rawToolUseResult struct {
	Stdout      string `json:"stdout"`
	Stderr      string `json:"stderr"`
	Interrupted bool   `json:"interrupted"`
	IsImage     bool   `json:"isImage"`
}

func processLine(traj *Trajectory, toolIndex map[string]int, line string) {
	var raw rawLine
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		traj.Skipped++
		return
	}

	if raw.SessionID != "" {
		traj.SessionID = raw.SessionID
	}
	if raw.Cwd != "" {
		traj.ProjectPath = raw.Cwd
	}
	if raw.GitBranch != "" {
		traj.Branch = raw.GitBranch
	}

	switch raw.Type {
	case "assistant":
		processAssistant(traj, toolIndex, raw)
	case "user":
		processUser(traj, toolIndex, raw)
	default:
		// Unknown/uninteresting line type — silently ignored, not a Skipped
		// (it parsed fine, it just isn't a step).
	}
}

func parseTimestamp(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func processAssistant(traj *Trajectory, toolIndex map[string]int, raw rawLine) {
	if raw.Message == nil || len(raw.Message.Content) == 0 {
		return
	}
	var blocks []rawBlock
	if err := json.Unmarshal(raw.Message.Content, &blocks); err != nil {
		return
	}
	ts := parseTimestamp(raw.Timestamp)
	for _, b := range blocks {
		switch b.Type {
		case "tool_use":
			step := Step{
				Index:     len(traj.Steps),
				Time:      ts,
				Kind:      StepToolUse,
				ToolName:  b.Name,
				Input:     b.Input,
				InputText: session.AbbreviateInput(b.Name, b.Input),
				ToolUseID: b.ID,
				Usage:     raw.Message.Usage,
			}
			traj.Steps = append(traj.Steps, step)
			if b.ID != "" {
				toolIndex[b.ID] = len(traj.Steps) - 1
			}
		case "text":
			if strings.TrimSpace(b.Text) == "" {
				continue
			}
			traj.Steps = append(traj.Steps, Step{
				Index:     len(traj.Steps),
				Time:      ts,
				Kind:      StepText,
				InputText: b.Text,
				Usage:     raw.Message.Usage,
			})
		case "thinking":
			if strings.TrimSpace(b.Thinking) == "" {
				continue
			}
			traj.Steps = append(traj.Steps, Step{
				Index:     len(traj.Steps),
				Time:      ts,
				Kind:      StepThinking,
				InputText: b.Thinking,
				Usage:     raw.Message.Usage,
			})
		default:
			// server_tool_use, redacted_thinking, image, ... — ignored.
		}
	}
}

func processUser(traj *Trajectory, toolIndex map[string]int, raw rawLine) {
	if raw.Message == nil || len(raw.Message.Content) == 0 {
		return
	}
	// Shape 1: a plain string — a replayed user turn/prompt, not a step.
	var s string
	if err := json.Unmarshal(raw.Message.Content, &s); err == nil {
		return
	}
	// Shape 2: an array of content blocks — tool results, joined back into
	// the Step their tool_use created (never a new Step of their own, or the
	// index-to-position mapping in toolIndex breaks).
	var blocks []rawBlock
	if err := json.Unmarshal(raw.Message.Content, &blocks); err != nil {
		return
	}
	ts := parseTimestamp(raw.Timestamp)
	for _, b := range blocks {
		if b.Type != "tool_result" {
			continue
		}
		idx, ok := toolIndex[b.ToolUseID]
		if !ok {
			// Result for a tool_use outside this read window (e.g. resumed
			// from an offset that starts mid-turn) — nothing to attach to.
			continue
		}
		text := toolResultText(b.Content)
		step := &traj.Steps[idx]
		step.ResultText = text
		step.ResultChars = utf8.RuneCountInString(text)
		step.ResultIsError = b.IsError
		step.ResultTime = ts
		if raw.ToolUseResult != nil {
			step.Stdout = raw.ToolUseResult.Stdout
			step.Stderr = raw.ToolUseResult.Stderr
		}
	}
}

// toolResultText pulls the human-readable text out of a tool_result's
// content, which is either a bare string or an array of {type,text} blocks.
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err == nil {
		var b strings.Builder
		for _, p := range parts {
			if p.Type == "text" {
				b.WriteString(p.Text)
			}
		}
		return b.String()
	}
	return string(raw)
}
