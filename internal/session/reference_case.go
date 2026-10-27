package session

import (
	"context"
	"strconv"
	"strings"
	"time"

	"claude-manager/internal/logger"
)

// Reference cases (WASTE-TASKS.md WS-06). With [optimization] reference_cases
// the session notes what a task started from — its text and the work folder's
// commit — and, when the task closes well, hands both to the manager together
// with the end commit as a RefCaseRecord. The manager adds what it measured
// (tokens, time, model) and stores it. Off by default: no git call is made.

// RefCaseRecord is what a closed task reports about itself (EvtTaskDone).
type RefCaseRecord struct {
	TaskPtr      string
	TaskText     string
	BaseCommit   string
	ResultCommit string
	Gates        []string
	// Rounds is the number of gate rework rounds the task took (0 = green first time).
	Rounds int
}

// maxRefTaskRunes caps a stored task text.
const maxRefTaskRunes = 12000

// refCapture is the start of the task in flight.
type refCapture struct {
	ptr  string
	text string
	base string
}

// refCasesOn reports whether this session records reference cases: the flag,
// and a Claude work folder (Hermes keeps its own turn loop).
func (s *Session) refCasesOn() bool {
	return s.refCasesFlag && !s.Config.IsHermes()
}

// beginReferenceCase notes the task a run starts on (pointer is the queue's
// top pointer). A second start on the same pointer means the task spans
// several runs (a slice, a crash retry, a rate-limit pause): its tokens and
// start would be partial, so it is no longer a candidate.
func (s *Session) beginReferenceCase(ctx context.Context, pointer string) {
	if !s.refCasesOn() {
		return
	}
	s.mu.Lock()
	prev := s.refCap
	s.mu.Unlock()
	switch {
	case pointer == "":
		s.mu.Lock()
		s.refCap, s.refSpanned = refCapture{}, ""
		s.mu.Unlock()
	case prev.ptr == pointer || s.refSpanned == pointer:
		s.mu.Lock()
		s.refCap, s.refSpanned = refCapture{}, pointer
		s.mu.Unlock()
	default:
		text := taskSectionFromPointer(readTaskQueue(ctx, s.ProjectPath, pointerFile(pointer)), pointer)
		s.mu.Lock()
		s.refCap, s.refSpanned = refCapture{ptr: pointer, text: text}, ""
		s.mu.Unlock()
	}
}

// captureRefBase records the work folder's commit once per task, right before
// the first run of the task launches.
func (s *Session) captureRefBase(ctx context.Context, dir string) {
	if !s.refCasesOn() {
		return
	}
	s.mu.Lock()
	need := s.refCap.ptr != "" && s.refCap.base == ""
	s.mu.Unlock()
	if !need {
		return
	}
	base := headCommit(ctx, dir)
	s.mu.Lock()
	if s.refCap.ptr != "" && s.refCap.base == "" {
		s.refCap.base = base
	}
	s.mu.Unlock()
}

// finishReferenceCase builds the record of a task that just closed (nil when
// the session does not record or saw no start for it) and ends the capture.
func (s *Session) finishReferenceCase(ctx context.Context, rounds int) *RefCaseRecord {
	if !s.refCasesOn() {
		return nil
	}
	s.mu.Lock()
	cap := s.refCap
	s.refCap, s.refSpanned = refCapture{}, ""
	s.mu.Unlock()
	if cap.ptr == "" || cap.text == "" || cap.base == "" {
		return nil
	}
	return &RefCaseRecord{
		TaskPtr: cap.ptr, TaskText: cap.text, BaseCommit: cap.base,
		ResultCommit: headCommit(ctx, s.gateRoot()), Gates: s.gates, Rounds: rounds,
	}
}

// headCommit returns HEAD of the repository at dir, "" when git cannot say.
func headCommit(ctx context.Context, dir string) string {
	if dir == "" {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := gitOutput(cctx, dir, "rev-parse", "HEAD")
	if err != nil {
		logger.L.Debug("session.refcase_head_failed", "dir", dir, "error", err)
		return ""
	}
	return strings.TrimSpace(out)
}

// pointerFile is the file part of a `<file>:<line>` pointer.
func pointerFile(pointer string) string {
	if i := strings.LastIndex(pointer, ":"); i > 0 {
		return pointer[:i]
	}
	return pointer
}

// taskSectionFromPointer returns the task a `<file>:<line>` pointer points to:
// the heading at that line (or the nearest one above it) down to the next
// heading of the same or a higher level. "" when the pointer or file is not
// usable.
func taskSectionFromPointer(content, pointer string) string {
	i := strings.LastIndex(pointer, ":")
	if i <= 0 || content == "" {
		return ""
	}
	line, err := strconv.Atoi(pointer[i+1:])
	if err != nil || line < 1 {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if line > len(lines) {
		return ""
	}
	start := line - 1
	for start >= 0 && headingLevel(lines[start]) == 0 {
		start--
	}
	if start < 0 {
		return ""
	}
	level := headingLevel(lines[start])
	end := len(lines)
	inFence := false
	for j := start + 1; j < len(lines); j++ {
		if strings.HasPrefix(strings.TrimSpace(lines[j]), "```") {
			inFence = !inFence
		}
		if l := headingLevel(lines[j]); !inFence && l != 0 && l <= level {
			end = j
			break
		}
	}
	return truncateRunes(strings.TrimSpace(strings.Join(lines[start:end], "\n")), maxRefTaskRunes)
}

// headingLevel is the number of leading '#' of a markdown heading line, 0 if
// the line is not one.
func headingLevel(line string) int {
	n := 0
	for n < len(line) && line[n] == '#' {
		n++
	}
	if n == 0 || n > 6 || n >= len(line) || line[n] != ' ' {
		return 0
	}
	return n
}
