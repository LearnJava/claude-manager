package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Emitter broadcasts named events (control-plane, MCP, Wails frontend).
// Defined here rather than imported from session/control to avoid an import
// cycle — structurally identical, so any of those satisfy this too.
type Emitter interface {
	Emit(event string, data any)
}

// Event names emitted during a round (see MIXED-TASKS.md MP-05).
const (
	EventRound = "worker:round"
	EventPatch = "worker:patch"
	EventGate  = "worker:gate"
	EventDone  = "worker:done"
)

// DefaultMaxRounds caps feedback rounds per subtask before it is marked
// TaskStatusNeedsHuman, matching the lumen bench finding that models rarely
// improve past round 3.
const DefaultMaxRounds = 3

// DefaultBriefSystemPrompt tells a worker model the wire format its output
// must use (see ParsePatches). MP-06 generates the task-specific brief; this
// is the fixed protocol portion every brief needs.
const DefaultBriefSystemPrompt = `You are a code-writing assistant. Respond with FIND/REPLACE patches only,
in exactly this format (one block per change, as many as needed):

### PATCH 1
FILE path/relative/to/repo/root
<<<FIND
exact existing lines, copied verbatim, enough of them to be unique in the file
===REPLACE
the replacement lines
>>>END

Rules:
- FIND must match the target file byte-for-byte, including whitespace.
- FIND must occur exactly once in the file.
- End every patch with >>>END, even the last one.
- Do not include line numbers, diff markers (+/-), or markdown code fences.
- For a new file, FIND a single placeholder line "// PLACEHOLDER" that the
  brief has pre-created in that file.`

// Brief is the self-contained task specification sent to a worker model.
// MP-06 will generate these automatically via a preflight session; for now
// callers build one directly.
type Brief struct {
	ID           string // stable id: used for dialogue persistence and task linkage
	Task         string // task description, verbatim code excerpts, accepted decisions
	SystemPrompt string // override for DefaultBriefSystemPrompt; empty uses the default
}

// TaskStatus tracks the lifecycle of a MixedTask.
type TaskStatus string

const (
	TaskStatusRunning    TaskStatus = "running"
	TaskStatusDone       TaskStatus = "done"
	TaskStatusNeedsHuman TaskStatus = "needs_human"
)

// RoundRecord is the outcome of one round: the worker's raw reply, the
// patches parsed from it, whether they applied and passed gates, and the
// exact feedback text sent back (if the round did not end the task) — this
// is the source of truth RunTask replays from on resume, not the raw HTTP
// dialogue.
type RoundRecord struct {
	Number        int
	RawOutput     string
	ParseError    string
	ParseWarnings []string
	Applied       []Patch
	Rejected      []RejectedPatch
	Gates         GateResult
	Passed        bool
	FeedbackSent  string
}

// MixedTask is the persisted state of one brief/worker mixed-programming run.
type MixedTask struct {
	ID           string
	Project      string
	BriefID      string
	WorkerName   string
	Branch       string
	WorktreePath string
	Status       TaskStatus
	MaxRounds    int
	Rounds       []RoundRecord
	Error        string
}

// TaskStore persists MixedTask state to
// ~/.claude-manager/state/mixed-task-<id>.json (mirrors session.StateStore /
// worker.Store), so a crash mid-task resumes at the last completed round
// instead of restarting from scratch.
type TaskStore struct {
	dir string
}

// NewTaskStore creates a TaskStore that persists files under dir.
func NewTaskStore(dir string) *TaskStore {
	return &TaskStore{dir: dir}
}

func (s *TaskStore) filePath(id string) string {
	return filepath.Join(s.dir, "mixed-task-"+storeIDReplacer.Replace(id)+".json")
}

// Save writes task, overwriting any prior save for the same ID.
func (s *TaskStore) Save(task *MixedTask) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(task, "", "  ")
	if err != nil {
		return err
	}
	target := s.filePath(task.ID)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// Load returns the saved task for id, or (nil, nil) if none exists.
func (s *TaskStore) Load(id string) (*MixedTask, error) {
	data, err := os.ReadFile(s.filePath(id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var task MixedTask
	if err := json.Unmarshal(data, &task); err != nil {
		_ = os.Remove(s.filePath(id))
		return nil, nil
	}
	return &task, nil
}

// Clear deletes the persisted task for id. Safe to call when absent.
func (s *TaskStore) Clear(id string) {
	if err := os.Remove(s.filePath(id)); err != nil && !os.IsNotExist(err) {
		_ = err
	}
}

// ListForProject returns all persisted tasks belonging to project, most
// recently saved first is not guaranteed — callers needing order should sort.
func (s *TaskStore) ListForProject(project string) ([]*MixedTask, error) {
	entries, err := os.ReadDir(s.dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []*MixedTask
	for _, e := range entries {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "mixed-task-") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var task MixedTask
		if err := json.Unmarshal(data, &task); err != nil {
			continue
		}
		if task.Project == project {
			out = append(out, &task)
		}
	}
	return out, nil
}

// RoundOrchestrator runs the brief -> patches -> apply -> gates loop for one
// MixedTask against one worker.
type RoundOrchestrator struct {
	Client      *Client
	Dialogue    *Store     // worker dialogue persistence (MP-02); nil disables it
	Tasks       *TaskStore // round-state persistence; nil disables it
	Emitter     Emitter    // nil is a no-op
	Gates       []string   // project.gates (MP-04)
	MaxRounds   int        // default DefaultMaxRounds if <= 0
	WorktreeDir string     // base directory task worktrees are created under

	// now is overridden in tests for deterministic branch names.
	now func() time.Time
}

func (o *RoundOrchestrator) clock() time.Time {
	if o.now != nil {
		return o.now()
	}
	return time.Now()
}

func (o *RoundOrchestrator) emit(event string, data any) {
	if o.Emitter != nil {
		o.Emitter.Emit(event, data)
	}
}

func (o *RoundOrchestrator) saveTask(task *MixedTask) {
	if o.Tasks == nil {
		return
	}
	// Best-effort: a save failure is recoverable on the next round's save and
	// must not abort an otherwise green round.
	_ = o.Tasks.Save(task)
}

// RunTask executes task against repoRoot (the main repository) until it
// reaches TaskStatusDone (worktree left in place for review) or
// TaskStatusNeedsHuman (rounds exhausted, or an unrecoverable error). If
// task.Rounds is already populated (loaded from TaskStore after a crash),
// execution resumes at the next round instead of restarting.
func (o *RoundOrchestrator) RunTask(ctx context.Context, repoRoot string, task *MixedTask, brief Brief) error {
	if task.MaxRounds <= 0 {
		task.MaxRounds = o.MaxRounds
	}
	if task.MaxRounds <= 0 {
		task.MaxRounds = DefaultMaxRounds
	}
	task.Status = TaskStatusRunning

	if task.Branch == "" {
		task.Branch = worktreeBranchName(o.clock(), task.BriefID, task.WorkerName)
	}
	if task.WorktreePath == "" {
		task.WorktreePath = filepath.Join(o.WorktreeDir, task.Branch)
	}
	if _, err := os.Stat(task.WorktreePath); os.IsNotExist(err) {
		if err := createWorktree(ctx, repoRoot, task.Branch, task.WorktreePath); err != nil {
			return o.fail(task, fmt.Errorf("create worktree: %w", err))
		}
		o.emit(EventRound, roundEvent{TaskID: task.ID, Event: "worktree_created", Branch: task.Branch})
	}
	o.saveTask(task)

	messages := buildMessages(brief, task.Rounds)

	for len(task.Rounds) < task.MaxRounds {
		if err := ctx.Err(); err != nil {
			return o.fail(task, err)
		}
		roundNum := len(task.Rounds) + 1
		o.emit(EventRound, roundEvent{TaskID: task.ID, Round: roundNum, Event: "start"})

		result, err := o.Client.Complete(ctx, task.ID, messages, o.Dialogue)
		if err != nil {
			return o.fail(task, fmt.Errorf("round %d: worker request: %w", roundNum, err))
		}
		messages = append(messages, ChatMessage{Role: "assistant", Content: result.Content})

		round := RoundRecord{Number: roundNum, RawOutput: result.Content}

		parsed, perr := ParsePatches(result.Content)
		if perr != nil {
			round.ParseError = perr.Error()
			feedback := "Your last reply could not be parsed as FIND/REPLACE patches: " + perr.Error()
			round.FeedbackSent = feedback
			task.Rounds = append(task.Rounds, round)
			o.saveTask(task)
			o.emit(EventPatch, patchEvent{TaskID: task.ID, Round: roundNum, Error: perr.Error()})
			messages = append(messages, ChatMessage{Role: "user", Content: feedback})
			continue
		}
		round.ParseWarnings = parsed.Warnings

		applyRes, err := ApplyPatches(task.WorktreePath, parsed.Patches)
		if err != nil {
			return o.fail(task, fmt.Errorf("round %d: apply patches: %w", roundNum, err))
		}
		round.Applied = applyRes.Applied
		round.Rejected = applyRes.Rejected
		o.emit(EventPatch, patchEvent{
			TaskID: task.ID, Round: roundNum,
			Applied: len(applyRes.Applied), Rejected: len(applyRes.Rejected),
		})

		if len(applyRes.Rejected) > 0 {
			feedback := rejectionFeedback(applyRes.Rejected)
			round.FeedbackSent = feedback
			task.Rounds = append(task.Rounds, round)
			o.saveTask(task)
			messages = append(messages, ChatMessage{Role: "user", Content: feedback})
			continue
		}

		gates := RunGates(ctx, task.WorktreePath, o.Gates)
		round.Gates = gates
		round.Passed = gates.Passed
		o.emit(EventGate, gateEvent{TaskID: task.ID, Round: roundNum, Passed: gates.Passed})

		if !gates.Passed {
			feedback := gates.Feedback()
			round.FeedbackSent = feedback
			task.Rounds = append(task.Rounds, round)
			o.saveTask(task)
			messages = append(messages, ChatMessage{Role: "user", Content: feedback})
			continue
		}

		commitMsg := fmt.Sprintf("mixed programming: %s (%s)", task.BriefID, task.WorkerName)
		if err := CommitWorktree(ctx, task.WorktreePath, task.WorkerName, o.Client.cfg.Model, commitMsg); err != nil {
			return o.fail(task, fmt.Errorf("round %d: commit: %w", roundNum, err))
		}
		task.Rounds = append(task.Rounds, round)
		task.Status = TaskStatusDone
		o.saveTask(task)
		o.emit(EventDone, doneEvent{TaskID: task.ID, Status: string(task.Status), Rounds: len(task.Rounds)})
		return nil
	}

	task.Status = TaskStatusNeedsHuman
	o.saveTask(task)
	o.emit(EventDone, doneEvent{TaskID: task.ID, Status: string(task.Status), Rounds: len(task.Rounds)})
	return nil
}

func (o *RoundOrchestrator) fail(task *MixedTask, err error) error {
	task.Status = TaskStatusNeedsHuman
	task.Error = err.Error()
	o.saveTask(task)
	o.emit(EventDone, doneEvent{TaskID: task.ID, Status: string(task.Status), Rounds: len(task.Rounds)})
	return err
}

// buildMessages deterministically reconstructs the full conversation from the
// brief and completed rounds, rather than trusting the raw dialogue store —
// this is what makes RunTask's resume exact: replaying task.Rounds always
// produces the same message list a fresh run would have reached.
func buildMessages(brief Brief, rounds []RoundRecord) []ChatMessage {
	sys := brief.SystemPrompt
	if sys == "" {
		sys = DefaultBriefSystemPrompt
	}
	msgs := []ChatMessage{
		{Role: "system", Content: sys},
		{Role: "user", Content: brief.Task},
	}
	for _, r := range rounds {
		msgs = append(msgs, ChatMessage{Role: "assistant", Content: r.RawOutput})
		if r.FeedbackSent != "" {
			msgs = append(msgs, ChatMessage{Role: "user", Content: r.FeedbackSent})
		}
	}
	return msgs
}

// rejectionFeedback formats rejected patches as model-facing feedback: the
// exact reasons ValidatePatch/ApplyPatches produced, not a paraphrase.
func rejectionFeedback(rejected []RejectedPatch) string {
	var b strings.Builder
	b.WriteString("Some patches were rejected and not applied:\n\n")
	for _, r := range rejected {
		b.WriteString(r.Reason)
		b.WriteString("\n\n")
	}
	b.WriteString("Resend corrected patches for the rejected files only.")
	return b.String()
}

var branchUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// worktreeBranchName builds the "mp-<task>-<model>-<HHMMSS>" branch name from
// MIXED-TASKS.md, sanitized to a valid git ref.
func worktreeBranchName(now time.Time, briefID, workerName string) string {
	return fmt.Sprintf("mp-%s-%s-%s",
		sanitizeBranchPart(briefID), sanitizeBranchPart(workerName), now.Format("150405"))
}

func sanitizeBranchPart(s string) string {
	s = branchUnsafe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		s = "x"
	}
	return s
}

// createWorktree adds a new git worktree at path on a new branch, creating
// path's parent directory first (git worktree add requires it to exist).
func createWorktree(ctx context.Context, repoRoot, branch, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if _, err := runGit(ctx, repoRoot, "worktree", "add", "-b", branch, path); err != nil {
		return err
	}
	return nil
}

type roundEvent struct {
	TaskID string `json:"task_id"`
	Round  int    `json:"round,omitempty"`
	Event  string `json:"event"`
	Branch string `json:"branch,omitempty"`
}

type patchEvent struct {
	TaskID   string `json:"task_id"`
	Round    int    `json:"round"`
	Applied  int    `json:"applied"`
	Rejected int    `json:"rejected"`
	Error    string `json:"error,omitempty"`
}

type gateEvent struct {
	TaskID string `json:"task_id"`
	Round  int    `json:"round"`
	Passed bool   `json:"passed"`
}

type doneEvent struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
	Rounds int    `json:"rounds"`
}
