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

// DefaultSkillMinScore is the minimum SkillCandidate.Score (LEARN-TASKS.md
// LN-08) a candidate must clear before DistillSkill invokes the CLI at all —
// distillation is a paid model call, so a low-score candidate must never
// trigger one. Unlike DefaultMinRunShare (LN-08), this is not corpus-measured
// — LN-08 shipped without a distribution of real Score values to calibrate
// against — so it is a conservative starting point, always overridable per
// call (minScore <= 0 falls back to this).
const DefaultSkillMinScore = 10.0

// ErrBelowThreshold is returned by DistillSkill when a candidate's score does
// not clear minScore. Not a failure: the CLI was correctly never invoked, and
// the caller should treat this as "skip this candidate", not report an error.
var ErrBelowThreshold = errors.New("analysis: skill candidate below distillation threshold")

// SkillSample is one concrete example call to seed the distillation prompt
// (LEARN-TASKS.md LN-09): the verbatim command plus, when available, the
// first lines of its captured output.
//
// Output is best-effort and, via the current ActionRow-based candidate
// pipeline (LN-08), always empty: action_signatures (LN-02) stores only the
// normalized signature and the verbatim arg, never raw tool output, by
// design (a transcript's full result text is the thing this app is
// deliberately not duplicating into SQLite). The field exists as a distinct
// slot so a future caller with access to real output text (e.g. a source
// that re-reads the original transcript for its samples) can populate it
// without a schema change; DistillSkill/buildSkillTaskText already render it
// when non-empty.
type SkillSample struct {
	Command string
	Output  string
}

// SkillFailureSummary is a compact rendering of one FailureCluster
// (LEARN-TASKS.md LN-07) related to the candidate being distilled — enough
// for the prompt to name a known failure and its fix without this package
// importing internal/experience's own aggregation types (see SkillDistillInput).
type SkillFailureSummary struct {
	ErrorKey  string
	FailedArg string
	FixedArg  string
}

// SkillDistillInput bundles what DistillSkill needs to turn a recurring
// tool-call sequence into a skill draft (LEARN-TASKS.md LN-09).
//
// Deliberately its own plain shape rather than experience.SkillCandidate/
// FailureCluster directly: internal/experience already imports
// internal/session (for Step/TokenUsage), and internal/session's
// SessionManager needs to call DistillSkill (mirroring how it calls
// GenerateRoadmap) to stream skill:progress — if this package's exported
// signature named an internal/experience type, SessionManager would have to
// import internal/experience too, which cycles straight back through
// internal/session. app.go (which already imports both analysis and
// experience) does the translation from experience.SkillCandidate to this
// shape before calling SessionManager.DistillSkill.
type SkillDistillInput struct {
	// Sig is the candidate's normalized signature sequence, length 1..4
	// (LN-08's SkillCandidate.Sig).
	Sig []string
	// Samples is up to 5 real occurrences (LN-08's SkillCandidate.Samples).
	Samples []SkillSample
	// RelatedFailures are failure clusters (LN-07) observed alongside this
	// candidate (LN-08's SkillCandidate.RelatedFailures) — one representative
	// example per cluster is enough context for the prompt.
	RelatedFailures []SkillFailureSummary
	// Gates are the project's configured gate commands.
	Gates []string
}

// SkillStep is one step of a distilled skill: a command plus, optionally, why
// it matters.
type SkillStep struct {
	Command string `json:"command"`
	Why     string `json:"why,omitempty"`
}

// SkillDraft is the decoded structured response from a skill-distillation
// session (SkillJSONSchema, LEARN-TASKS.md LN-09).
type SkillDraft struct {
	Name         string      `json:"name"`
	Description  string      `json:"description"`
	WhenToUse    []string    `json:"when_to_use,omitempty"`
	Steps        []SkillStep `json:"steps"`
	Gotchas      []string    `json:"gotchas,omitempty"`
	DoneWhen     string      `json:"done_when"`
	FilesTouched []string    `json:"files_touched,omitempty"`

	// CostUSD is the cost of the distillation run itself, extracted from the
	// outer result wrapper. Zero if the wrapper did not include it.
	CostUSD float64 `json:"-"`
}

// buildSkillTaskText composes SkillDistillInput into the plain-text task
// Claude is asked to distill into a skill draft. truncateLines caps each
// sample's captured output at 20 lines, per LEARN-TASKS.md LN-09 ("до 5
// реальных примеров ... первые 20 строк её вывода, обрезанные").
func buildSkillTaskText(in SkillDistillInput) string {
	var b strings.Builder

	if len(in.Sig) > 0 {
		b.WriteString("Recurring call sequence (normalized signatures):\n")
		for _, s := range in.Sig {
			b.WriteString("  " + s + "\n")
		}
		b.WriteString("\n")
	}

	if len(in.Samples) > 0 {
		b.WriteString("Real examples:\n")
		for _, s := range in.Samples {
			if s.Command == "" {
				continue
			}
			b.WriteString("$ " + s.Command + "\n")
			if out := truncateLines(s.Output, 20); out != "" {
				b.WriteString(out + "\n")
			}
			b.WriteString("\n")
		}
	}

	if len(in.RelatedFailures) > 0 {
		b.WriteString("Related failures observed alongside this sequence:\n")
		for _, f := range in.RelatedFailures {
			b.WriteString(fmt.Sprintf("- %s: failed with `%s`, next attempt fixed it with `%s`\n",
				f.ErrorKey, f.FailedArg, f.FixedArg))
		}
		b.WriteString("\n")
	}

	if len(in.Gates) > 0 {
		b.WriteString("Project gate commands:\n" + strings.Join(in.Gates, "\n") + "\n")
	}

	return strings.TrimSpace(b.String())
}

// truncateLines returns at most n lines of s, appending a marker when it cut
// something off. Returns "" for empty input.
func truncateLines(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return ""
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return strings.Join(lines, "\n")
	}
	return strings.Join(lines[:n], "\n") + "\n… (truncated)"
}

// buildSkillStreamArgs mirrors buildStreamingAnalysisArgs but with skill
// distillation's own default model (sonnet, not haiku — a distillation is
// invoked rarely and only past DefaultSkillMinScore, so quality is worth more
// than the cost difference here) and schema/system prompt.
func buildSkillStreamArgs(cfg AnalysisConfig, task string) []string {
	model := cfg.Model
	if model == "" {
		model = "sonnet"
	}
	effort := cfg.Effort
	if effort == "" {
		effort = "medium"
	}
	sysPrompt := cfg.SystemPrompt
	if sysPrompt == "" {
		sysPrompt = SkillSystemPrompt
	}
	schema := cfg.JSONSchema
	if schema == "" {
		schema = SkillJSONSchema
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
	args = append(args, "Distill this recurring pattern into a skill:\n\n"+task)
	return args
}

// ParseSkillOutput decodes raw CLI stdout into a SkillDraft. It accepts
// either the bare structured object or the result-wrapper envelope produced
// by `--output-format json`/`stream-json`'s terminating `result` event
// (mirrors ParseAnalysisOutput/ParseJournalOutput/ParseBriefOutput).
func ParseSkillOutput(out []byte) (*SkillDraft, error) {
	if len(out) == 0 {
		return nil, errors.New("skill: empty CLI output")
	}

	var wrap resultWrapper
	if err := json.Unmarshal(out, &wrap); err == nil && wrap.Type != "" {
		if wrap.IsError {
			msg := wrap.Error
			if msg == "" {
				msg = "skill distillation reported error"
			}
			return nil, fmt.Errorf("skill: %s", msg)
		}
		if len(wrap.Result) == 0 {
			return nil, errors.New("skill: wrapper has empty result field")
		}
		res, err := decodeSkillPayload(wrap.Result)
		if err != nil {
			return nil, err
		}
		res.CostUSD = wrap.TotalCostUSD
		return res, nil
	}

	return decodeSkillPayload(out)
}

func decodeSkillPayload(raw json.RawMessage) (*SkillDraft, error) {
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("skill: empty payload")
	}

	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("skill: unwrap string payload: %w", err)
		}
		trimmed = []byte(s)
	}

	var res SkillDraft
	if err := json.Unmarshal(trimmed, &res); err != nil {
		return nil, fmt.Errorf("skill: decode payload: %w", err)
	}
	return &res, nil
}

// DistillSkill turns one recurring tool-call sequence into a skill draft
// (LEARN-TASKS.md LN-09), streaming lightweight progress through onProgress
// exactly like RunAnalysisStreaming does for roadmap generation — a
// distillation run has no other visible sign of being alive rather than hung.
//
// Returns ErrBelowThreshold without invoking the CLI at all when score does
// not clear minScore (minScore <= 0 falls back to DefaultSkillMinScore) —
// distillation is a paid call and must never fire for a negligible candidate.
func DistillSkill(ctx context.Context, projectPath string, in SkillDistillInput, score, minScore float64, cfg AnalysisConfig, onProgress ProgressFunc) (*SkillDraft, error) {
	if minScore <= 0 {
		minScore = DefaultSkillMinScore
	}
	if score < minScore {
		return nil, ErrBelowThreshold
	}

	task := buildSkillTaskText(in)
	if task == "" {
		return nil, errors.New("skill: nothing to distill")
	}

	bin := cfg.ClaudePath
	if bin == "" {
		bin = "claude"
	}

	args := buildSkillStreamArgs(cfg, task)
	cmd := exec.CommandContext(ctx, bin, args...)
	proc.HideConsole(cmd)
	if projectPath != "" {
		cmd.Dir = projectPath
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("skill: stdout pipe: %w", err)
	}
	var stderrBuf bytes.Buffer
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("skill: start: %w", err)
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
			return nil, fmt.Errorf("skill: claude exited: %w: %s", waitErr, stderrBuf.String())
		}
		return nil, fmt.Errorf("skill: claude exited: %w", waitErr)
	}
	if scanErr != nil {
		return nil, fmt.Errorf("skill: read stdout: %w", scanErr)
	}
	if resultLine == nil {
		return nil, errors.New("skill: no result event in stream")
	}

	return ParseSkillOutput(resultLine)
}

// titleFromName renders a kebab-case skill name as a human title for the
// markdown body's H1 — "git-session-preamble" -> "Git session preamble".
func titleFromName(name string) string {
	words := strings.Split(name, "-")
	for i, w := range words {
		if w == "" {
			continue
		}
		if i == 0 {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// maxSkillBodyLines caps RenderSkillMarkdown's output (LEARN-TASKS.md LN-09:
// "тело ≤120 строк") — SkillSystemPrompt already asks the model to stay under
// this, but the renderer enforces it regardless of whether the model
// complied, rather than trusting a length instruction alone.
const maxSkillBodyLines = 120

// RenderSkillMarkdown renders a SkillDraft as a SKILL.md body: a YAML
// frontmatter with just name/description — the only two fields that stay
// permanently in context, matching the .claude/skills/*/SKILL.md convention
// this app itself writes — followed by the rest of the draft as sections,
// each omitted when empty rather than rendered as a bare empty heading.
func RenderSkillMarkdown(d SkillDraft) string {
	var b strings.Builder

	b.WriteString("---\n")
	b.WriteString("name: " + d.Name + "\n")
	b.WriteString("description: " + d.Description + "\n")
	b.WriteString("---\n\n")

	b.WriteString("# " + titleFromName(d.Name) + "\n")

	if len(d.WhenToUse) > 0 {
		b.WriteString("\n## When to use\n\n")
		for _, w := range d.WhenToUse {
			b.WriteString("- " + w + "\n")
		}
	}

	if len(d.Steps) > 0 {
		b.WriteString("\n## Steps\n\n")
		for i, s := range d.Steps {
			if s.Why != "" {
				b.WriteString(fmt.Sprintf("%d. `%s` — %s\n", i+1, s.Command, s.Why))
			} else {
				b.WriteString(fmt.Sprintf("%d. `%s`\n", i+1, s.Command))
			}
		}
	}

	if len(d.Gotchas) > 0 {
		b.WriteString("\n## Gotchas\n\n")
		for _, g := range d.Gotchas {
			b.WriteString("- " + g + "\n")
		}
	}

	if d.DoneWhen != "" {
		b.WriteString("\n## Done when\n\n" + d.DoneWhen + "\n")
	}

	if len(d.FilesTouched) > 0 {
		b.WriteString("\n## Files touched\n\n")
		for _, f := range d.FilesTouched {
			b.WriteString("- " + f + "\n")
		}
	}

	return truncateMarkdownLines(b.String(), maxSkillBodyLines)
}

// truncateMarkdownLines keeps at most n lines of a rendered document, always
// preserving the frontmatter (the first "---\n...\n---\n\n" block) even if
// that alone would exceed n — a truncated frontmatter is a broken skill file,
// while a truncated body is merely an incomplete one.
func truncateMarkdownLines(md string, n int) string {
	lines := strings.Split(md, "\n")
	if len(lines) <= n {
		return md
	}
	frontmatterEnd := 0
	if len(lines) > 0 && lines[0] == "---" {
		for i := 1; i < len(lines); i++ {
			if lines[i] == "---" {
				frontmatterEnd = i + 1
				break
			}
		}
	}
	cut := n
	if cut < frontmatterEnd {
		cut = frontmatterEnd
	}
	if cut >= len(lines) {
		return md
	}
	return strings.Join(lines[:cut], "\n") + "\n… (truncated)\n"
}
