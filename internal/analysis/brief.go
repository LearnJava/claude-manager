package analysis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"claude-manager/internal/proc"
	"claude-manager/internal/store"
	"claude-manager/internal/worker"

	"github.com/google/uuid"
)

// PlaceholderAnchor is the single-line content CreatePlaceholderFile writes
// for a new file, so a worker can FIND/REPLACE against it instead of
// inventing new-file syntax (lumen convention, MIXED-TASKS.md MP-06).
const PlaceholderAnchor = "// PLACEHOLDER"

// BriefResult is the decoded structured output of a brief-generation session
// (BriefJSONSchema).
type BriefResult struct {
	Task     string   `json:"task"`
	Files    []string `json:"files"`
	NewFiles []string `json:"new_files,omitempty"`

	// CostUSD is the cost of the brief-generation run itself, extracted from
	// the outer result wrapper. Zero if the wrapper did not include it.
	CostUSD float64 `json:"-"`
}

// BuildBriefArgs constructs the argv passed to the Claude CLI for a
// brief-generation run. Mirrors BuildAnalysisArgs.
func BuildBriefArgs(cfg AnalysisConfig, task string) []string {
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
		sysPrompt = BriefSystemPrompt
	}

	args := []string{
		"-p",
		"--model", model,
		"--effort", effort,
		"--permission-mode", "plan",
		"--json-schema", BriefJSONSchema,
		"--output-format", "json",
		"--append-system-prompt", sysPrompt,
	}
	if cfg.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd",
			strconv.FormatFloat(cfg.MaxBudgetUSD, 'f', -1, 64))
	}
	args = append(args, "Write a mixed-programming brief for this task: "+task)
	return args
}

// ParseBriefOutput decodes raw CLI stdout into a BriefResult. It accepts
// either the bare structured object or the result-wrapper envelope produced
// by `--output-format json` (mirrors ParseAnalysisOutput).
func ParseBriefOutput(out []byte) (*BriefResult, error) {
	if len(out) == 0 {
		return nil, errors.New("brief: empty CLI output")
	}

	var wrap resultWrapper
	if err := json.Unmarshal(out, &wrap); err == nil && wrap.Type != "" {
		if wrap.IsError {
			msg := wrap.Error
			if msg == "" {
				msg = "brief generator reported error"
			}
			return nil, fmt.Errorf("brief: %s", msg)
		}
		if len(wrap.Result) == 0 {
			return nil, errors.New("brief: wrapper has empty result field")
		}
		res, err := decodeBriefPayload(wrap.Result)
		if err != nil {
			return nil, err
		}
		res.CostUSD = wrap.TotalCostUSD
		return res, nil
	}

	return decodeBriefPayload(out)
}

func decodeBriefPayload(raw json.RawMessage) (*BriefResult, error) {
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, errors.New("brief: empty payload")
	}

	if trimmed[0] == '"' {
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return nil, fmt.Errorf("brief: unwrap string payload: %w", err)
		}
		trimmed = []byte(s)
	}

	var res BriefResult
	if err := json.Unmarshal(trimmed, &res); err != nil {
		return nil, fmt.Errorf("brief: decode payload: %w", err)
	}
	return &res, nil
}

// unresolvedDecisionPhrases flags briefs that still leave a choice to the
// worker instead of deciding — MIXED-TASKS.md's lumen rule: "никаких «выбери
// между A и B»".
var unresolvedDecisionPhrases = []string{
	"choose between", "either use", "you can choose", "pick whichever", "up to you",
}

// ValidateBriefResult checks structural completeness before a brief is
// turned into a worker.Brief: a non-empty task description, at least one
// target file, and no obviously unresolved decision left for the worker.
func ValidateBriefResult(r *BriefResult) error {
	if r == nil {
		return errors.New("brief: nil result")
	}
	if strings.TrimSpace(r.Task) == "" {
		return errors.New("brief: task description is empty")
	}
	if len(r.Files) == 0 && len(r.NewFiles) == 0 {
		return errors.New("brief: no files listed to touch")
	}
	lower := strings.ToLower(r.Task)
	for _, phrase := range unresolvedDecisionPhrases {
		if strings.Contains(lower, phrase) {
			return fmt.Errorf("brief: task leaves an unresolved decision (%q) — the brief must decide, not the worker", phrase)
		}
	}
	return nil
}

// NewBriefFromResult builds a worker.Brief from a validated BriefResult,
// assigning a fresh ID.
func NewBriefFromResult(r *BriefResult) *worker.Brief {
	return &worker.Brief{ID: uuid.NewString(), Task: r.Task}
}

// CreatePlaceholderFile creates relPath under projectPath with a single
// PlaceholderAnchor line, unless it already exists (idempotent — a retried
// brief generation must not clobber a file another round already touched).
func CreatePlaceholderFile(projectPath, relPath string) error {
	full, err := safeNewFilePath(projectPath, relPath)
	if err != nil {
		return err
	}
	if _, err := os.Stat(full); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	return os.WriteFile(full, []byte(PlaceholderAnchor+"\n"), 0o644)
}

// safeNewFilePath resolves rel inside projectPath, rejecting anything that
// escapes it (absolute paths, drive-relative paths, rooted paths without a
// drive letter, ".." escapes). filepath.IsAbs alone is not enough: on
// Windows it only recognizes "C:\..." / UNC forms, not a bare "/rel/path" —
// checked explicitly here rather than relying on filepath.Join's Clean to
// happen to keep such a path contained.
func safeNewFilePath(projectPath, rel string) (string, error) {
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" ||
		strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "\\") {
		return "", fmt.Errorf("brief: absolute path not allowed: %s", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("brief: path escapes project: %s", rel)
	}
	return filepath.Join(projectPath, clean), nil
}

// GenerateBrief runs a preflight-style session (haiku/sonnet, --json-schema)
// that writes a self-contained worker.Brief for task: verbatim code excerpts
// with exact line numbers, decisions already made, typed-locals hints, and a
// patch-format reminder (BriefSystemPrompt). Every file in the result's
// new_files that does not exist yet is pre-created with PlaceholderAnchor so
// the worker can FIND/REPLACE against it. Returns the brief plus the combined
// file list (files + new_files) for the caller to persist via SaveBrief.
func GenerateBrief(ctx context.Context, projectPath, task string, cfg AnalysisConfig) (*worker.Brief, []string, error) {
	bin := cfg.ClaudePath
	if bin == "" {
		bin = "claude"
	}

	cmd := exec.CommandContext(ctx, bin, BuildBriefArgs(cfg, task)...)
	proc.HideConsole(cmd)
	if projectPath != "" {
		cmd.Dir = projectPath
	}
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return nil, nil, fmt.Errorf("brief: claude exited: %w: %s", err, string(ee.Stderr))
		}
		return nil, nil, fmt.Errorf("brief: claude exited: %w", err)
	}

	result, err := ParseBriefOutput(out)
	if err != nil {
		return nil, nil, err
	}
	if err := ValidateBriefResult(result); err != nil {
		return nil, nil, err
	}
	for _, f := range result.NewFiles {
		if err := CreatePlaceholderFile(projectPath, f); err != nil {
			return nil, nil, fmt.Errorf("brief: create placeholder %s: %w", f, err)
		}
	}

	brief := NewBriefFromResult(result)
	files := append(append([]string{}, result.Files...), result.NewFiles...)
	return brief, files, nil
}

// ---- Store integration ----

// SaveBrief persists brief and its file list under project. A nil store is a
// no-op, matching SavePlan.
func SaveBrief(st *store.Store, project string, brief *worker.Brief, files []string) error {
	if st == nil || brief == nil {
		return nil
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return fmt.Errorf("brief: marshal files: %w", err)
	}
	row := &store.MixedBrief{
		BriefID:   brief.ID,
		Project:   project,
		Task:      brief.Task,
		Files:     string(filesJSON),
		CreatedAt: time.Now(),
	}
	if err := st.InsertBrief(row); err != nil {
		return fmt.Errorf("brief: insert: %w", err)
	}
	return nil
}

// LoadBrief reconstructs a worker.Brief and its file list from the store by
// briefID. Returns (nil, nil, nil) if no brief with that ID exists.
func LoadBrief(st *store.Store, briefID string) (*worker.Brief, []string, error) {
	if st == nil {
		return nil, nil, errors.New("brief: nil store")
	}
	row, err := st.GetBriefByBriefID(briefID)
	if err != nil {
		return nil, nil, err
	}
	if row == nil {
		return nil, nil, nil
	}

	var files []string
	if row.Files != "" {
		if err := json.Unmarshal([]byte(row.Files), &files); err != nil {
			return nil, nil, fmt.Errorf("brief: decode files: %w", err)
		}
	}
	return &worker.Brief{ID: row.BriefID, Task: row.Task}, files, nil
}
