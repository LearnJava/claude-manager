package session

import (
	"context"
	"strings"

	"claude-manager/internal/logger"
)

// Quality labels (WASTE-TASKS.md WS-11). With [optimization] quality_labels the
// session keeps what a task started from — its text and the work folder's
// commit — across the runs it takes, and when the task ends badly for good
// (gates still red after the last rework round, or unfinished run after run
// until the loop is stopped) hands the evidence to the manager inside the
// run's end event. A task that ends well, or a run that is merely retried,
// produces nothing. Off by default: no git call is made.

// How a task ended badly; the same values as experience.OutcomeGatesRed and
// experience.OutcomeUnfinishedLoop.
const (
	LabelOutcomeGatesRed       = "gates_red"
	LabelOutcomeUnfinishedLoop = "unfinished_loop"
)

// LabelRecord is the evidence about a task that ended badly (EvtRunEnd).
type LabelRecord struct {
	TaskPtr  string
	TaskText string
	Outcome  string
	// Feedback is the last red gate output; empty when the agent's last words
	// (known to the manager) are the better evidence.
	Feedback string
	// Diff is the work folder's status and its changes against the task's
	// starting commit.
	Diff string
}

// Caps on the evidence kept in memory; the judge cuts it shorter still.
const (
	maxLabelDiffBytes = 24000
	maxLabelStatus    = 40
)

// labelCapture is the start of the task in flight; it outlives retries of the
// same pointer, unlike the reference-case capture.
type labelCapture struct {
	ptr, text, base string
}

func (s *Session) qualityLabelsOn() bool {
	return s.qualityLabelsFlag && !s.Config.IsHermes()
}

// beginLabelCapture notes the task a run starts on. Another pointer than the
// one held means the previous task is over, whichever way it ended.
func (s *Session) beginLabelCapture(ctx context.Context, pointer string) {
	if !s.qualityLabelsOn() {
		return
	}
	s.mu.Lock()
	same := s.labelCap.ptr == pointer
	s.mu.Unlock()
	if same {
		return
	}
	cap := labelCapture{}
	if pointer != "" {
		cap = labelCapture{ptr: pointer,
			text: taskSectionFromPointer(readTaskQueue(ctx, s.ProjectPath, pointerFile(pointer)), pointer)}
	}
	s.mu.Lock()
	s.labelCap = cap
	s.mu.Unlock()
}

// captureLabelBase records the work folder's commit once per task, right
// before its first run launches.
func (s *Session) captureLabelBase(ctx context.Context, dir string) {
	if !s.qualityLabelsOn() {
		return
	}
	s.mu.Lock()
	need := s.labelCap.ptr != "" && s.labelCap.base == ""
	s.mu.Unlock()
	if !need {
		return
	}
	base := headCommit(ctx, dir)
	s.mu.Lock()
	if s.labelCap.ptr != "" && s.labelCap.base == "" {
		s.labelCap.base = base
	}
	s.mu.Unlock()
}

// endLabelCapture forgets the task in flight (it closed well).
func (s *Session) endLabelCapture() {
	if !s.qualityLabelsOn() {
		return
	}
	s.mu.Lock()
	s.labelCap = labelCapture{}
	s.mu.Unlock()
}

// badEndingLabel builds the record of a task that just ended badly for good
// and forgets the capture. nil when labels are off or no task was seen.
func (s *Session) badEndingLabel(ctx context.Context, outcome, feedback string) *LabelRecord {
	if !s.qualityLabelsOn() {
		return nil
	}
	s.mu.Lock()
	cap := s.labelCap
	s.labelCap = labelCapture{}
	s.mu.Unlock()
	if cap.ptr == "" {
		return nil
	}
	return &LabelRecord{
		TaskPtr: cap.ptr, TaskText: cap.text, Outcome: outcome, Feedback: feedback,
		Diff: workFolderChanges(ctx, s.gateRoot(), cap.base),
	}
}

// workFolderChanges is `git status --short` (untracked files are where leftovers
// show) followed by `git diff <base>`; base "" means HEAD. Best effort: a git
// failure only shortens the evidence.
func workFolderChanges(ctx context.Context, dir, base string) string {
	if dir == "" {
		return ""
	}
	if base == "" {
		base = "HEAD"
	}
	var b strings.Builder
	if out, err := gitOutput(ctx, dir, "status", "--short"); err == nil {
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) > maxLabelStatus {
			lines = append(lines[:maxLabelStatus], "…")
		}
		if t := strings.TrimSpace(strings.Join(lines, "\n")); t != "" {
			b.WriteString("git status --short:\n" + t + "\n\n")
		}
	} else {
		logger.L.Debug("session.label_status_failed", "dir", dir, "error", err)
	}
	if out, err := gitOutput(ctx, dir, "diff", base); err == nil {
		if len(out) > maxLabelDiffBytes {
			out = out[:maxLabelDiffBytes] + "\n…"
		}
		b.WriteString(out)
	} else {
		logger.L.Debug("session.label_diff_failed", "dir", dir, "base", base, "error", err)
	}
	return strings.TrimSpace(b.String())
}
