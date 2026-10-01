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

// DefaultSkillReviewModel reviews the autopilot's drafts (LEARN-TASKS.md
// LN-25). Deliberately not the distiller's own default (sonnet): a model
// grading text it produced itself is lenient toward it, and the review
// prompt is small enough that the stronger model costs little.
const DefaultSkillReviewModel = "opus"

// ExistingSkillSummary is one skill already in the project's library, as
// shown to the reviewer for duplicate detection.
type ExistingSkillSummary struct {
	Name        string
	Description string
}

// SkillReviewInput is everything the reviewer judges a draft against.
type SkillReviewInput struct {
	Sig      []string // the candidate's signature sequence
	RunShare float64  // share of the project's runs the sequence occurs in
	DraftMD  string   // the rendered SKILL.md (analysis.RenderSkillMarkdown)
	Existing []ExistingSkillSummary
}

// SkillReview is the reviewer's verdict.
type SkillReview struct {
	Accept      bool   `json:"accept"`
	Reason      string `json:"reason"`
	DuplicateOf string `json:"duplicate_of,omitempty"`

	CostUSD float64 `json:"-"`
}

// SkillReviewJSONSchema is the reviewer's structured output.
const SkillReviewJSONSchema = `{
  "type": "object",
  "properties": {
    "accept": {"type": "boolean"},
    "reason": {"type": "string", "maxLength": 300},
    "duplicate_of": {"type": "string"}
  },
  "required": ["accept", "reason"],
  "additionalProperties": false
}`

// SkillReviewSystemPrompt states the acceptance bar. A wrongly accepted
// skill is cheap to undo (the autopilot measures it and switches it off),
// but each one costs every future session context, so the bar is "clearly
// useful", not "not harmful".
const SkillReviewSystemPrompt = `You review a draft Claude Code skill (SKILL.md) that was distilled automatically from a recurring tool-call pattern in this project's history. If accepted, it is written to .claude/skills/ and every future agent session in this project will see its description.

Accept ONLY if all of these hold:
- It teaches a concrete, project-specific procedure (ordering, flags, paths, checks, gotchas) that a capable agent would not already do correctly by default.
- Its "description" says precisely WHEN to use it, so an agent can decide from that one line alone.
- It does not duplicate or substantially overlap any existing skill listed below (if it does, set duplicate_of to that skill's name).
- It contains no destructive or policy-violating steps (force push, rewriting history, --no-verify, deleting data, disabling checks) and does not contradict the project's CLAUDE.md, which you may read.

Reject generic advice ("read the file before editing", "use grep to find code"), single trivial commands, and anything that just restates how a common tool works.

Answer with one short sentence of reason, in Russian.`

func buildSkillReviewTask(in SkillReviewInput) string {
	var b strings.Builder
	b.WriteString("Recurring pattern: " + strings.Join(in.Sig, " -> ") + "\n")
	b.WriteString(fmt.Sprintf("Occurs in %.0f%% of this project's runs.\n\n", in.RunShare*100))
	if len(in.Existing) > 0 {
		b.WriteString("Existing skills in this project:\n")
		for _, e := range in.Existing {
			b.WriteString("- " + e.Name + ": " + e.Description + "\n")
		}
		b.WriteString("\n")
	} else {
		b.WriteString("The project has no other skills yet.\n\n")
	}
	b.WriteString("Draft SKILL.md:\n\n" + in.DraftMD)
	return b.String()
}

// BuildSkillReviewArgs constructs the reviewer's CLI argv — mirrors
// BuildJournalArgs (one-shot, plan mode so it can read CLAUDE.md but never
// write).
func BuildSkillReviewArgs(cfg AnalysisConfig, task string) []string {
	model := cfg.Model
	if model == "" {
		model = DefaultSkillReviewModel
	}
	effort := cfg.Effort
	if effort == "" {
		effort = "medium"
	}
	args := []string{
		"-p",
		"--model", model,
		"--effort", effort,
		"--permission-mode", "plan",
		"--json-schema", SkillReviewJSONSchema,
		"--output-format", "json",
		"--append-system-prompt", SkillReviewSystemPrompt,
	}
	if cfg.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(cfg.MaxBudgetUSD, 'f', -1, 64))
	}
	return append(args, "Review this skill draft:\n\n"+task)
}

// ParseSkillReviewOutput decodes the reviewer's CLI stdout — either the
// bare object or the `--output-format json` result wrapper.
func ParseSkillReviewOutput(out []byte) (*SkillReview, error) {
	if len(out) == 0 {
		return nil, errors.New("skill review: empty CLI output")
	}
	var wrap resultWrapper
	if err := json.Unmarshal(out, &wrap); err == nil && wrap.Type != "" {
		if wrap.IsError {
			msg := wrap.Error
			if msg == "" {
				msg = "skill review reported error"
			}
			return nil, fmt.Errorf("skill review: %s", msg)
		}
		if len(wrap.Result) == 0 {
			return nil, errors.New("skill review: wrapper has empty result field")
		}
		res, err := decodeSkillReviewPayload(wrap.Result)
		if err != nil {
			return nil, err
		}
		res.CostUSD = wrap.TotalCostUSD
		return res, nil
	}
	return decodeSkillReviewPayload(out)
}

func decodeSkillReviewPayload(raw json.RawMessage) (*SkillReview, error) {
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("skill review: empty payload")
	}
	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("skill review: unwrap string payload: %w", err)
		}
		trimmed = []byte(s)
	}
	var res SkillReview
	if err := json.Unmarshal(trimmed, &res); err != nil {
		return nil, fmt.Errorf("skill review: decode payload: %w", err)
	}
	return &res, nil
}

// ReviewSkill asks the reviewer model whether an autopilot draft is worth
// applying (LEARN-TASKS.md LN-25).
func ReviewSkill(ctx context.Context, projectPath string, in SkillReviewInput, cfg AnalysisConfig) (*SkillReview, error) {
	if strings.TrimSpace(in.DraftMD) == "" {
		return nil, errors.New("skill review: empty draft")
	}
	bin := cfg.ClaudePath
	if bin == "" {
		bin = "claude"
	}
	cmd := exec.CommandContext(ctx, bin, BuildSkillReviewArgs(cfg, buildSkillReviewTask(in))...)
	proc.HideConsole(cmd)
	if projectPath != "" {
		cmd.Dir = projectPath
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("skill review: claude exited: %w: %s", err, string(ee.Stderr))
		}
		return nil, fmt.Errorf("skill review: claude exited: %w", err)
	}
	return ParseSkillReviewOutput(out)
}
