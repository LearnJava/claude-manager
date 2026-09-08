package experience

import (
	"bufio"
	"os"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// ParseLogFile parses one auto-saved markdown session log — the exact format
// store.RenderExport(..., "md") produces — into a Trajectory, the same shape
// Read/ReadFrom (LN-01) produce from a JSONL transcript. One file is one
// completed run (the log is saved once, at task/run completion), so unlike
// the JSONL backend there is no incremental offset to resume from.
//
// What is available and what is not (LEARN-TASKS.md LN-17): the full
// sequence of tool calls with their abbreviated input, each tool_result's
// size (via the following entry's message length), error entries, and the
// session name/time from the file itself. Not available: token usage per
// step, tool_use_id, an is_error flag tied to a specific call before it
// closes, or a run_id — callers must tolerate zero values in those fields
// exactly as for the JSONL backend.
func ParseLogFile(path string) (Trajectory, error) {
	f, err := os.Open(path)
	if err != nil {
		return Trajectory{}, err
	}
	defer f.Close()

	var traj Trajectory
	var day time.Time // date carried only by the "_Saved ..." line; entries carry time-of-day only
	var queue []int   // FIFO of pending StepToolUse indices awaiting their tool_result/error (rule 1)
	pendingTool := false

	pop := func() (int, bool) {
		if len(queue) == 0 {
			return 0, false
		}
		idx := queue[0]
		queue = queue[1:]
		return idx, true
	}

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			pendingTool = false
			continue
		}

		if m := mdHeaderRe.FindStringSubmatch(line); m != nil {
			traj.SessionID = m[1]
			pendingTool = false
			continue
		}
		if m := mdSavedRe.FindStringSubmatch(line); m != nil {
			if t, err := time.Parse(time.RFC3339, m[1]); err == nil {
				day = t
			}
			pendingTool = false
			continue
		}
		if pendingTool {
			pendingTool = false
			if m := mdToolRe.FindStringSubmatch(line); m != nil {
				last := &traj.Steps[len(traj.Steps)-1]
				last.ToolName = m[1]
				last.InputText = m[2]
				continue
			}
			// Missing "  - tool:" sub-line (a hand-edited or truncated file) —
			// fall through and parse this line as its own entry instead of
			// dropping it; the tool step above is left with no ToolName.
		}

		m := mdEntryRe.FindStringSubmatch(line)
		if m == nil {
			traj.Skipped++
			continue
		}
		ts := combineTime(day, m[1])
		level, message := m[2], m[3]

		switch level {
		case "tool":
			traj.Steps = append(traj.Steps, Step{
				Index: len(traj.Steps),
				Time:  ts,
				Kind:  StepToolUse,
				// ToolName/InputText are filled from the "  - tool:" sub-line
				// that RenderExport always emits right after a "tool" entry.
			})
			queue = append(queue, len(traj.Steps)-1)
			pendingTool = true

		case "tool_result", "error":
			// Both close the pending call — error additionally marks it
			// failed (LN-17: a failed command is logged as "error", not
			// "tool_result"). An error with no pending call (a process-level
			// failure unrelated to any tool) has nothing to attach to.
			if idx, ok := pop(); ok {
				step := &traj.Steps[idx]
				step.ResultText = message
				step.ResultChars = utf8.RuneCountInString(message)
				step.ResultIsError = level == "error"
				step.ResultTime = ts
			}

		case "text":
			traj.Steps = append(traj.Steps, Step{Index: len(traj.Steps), Time: ts, Kind: StepText, InputText: message})
			queue = queue[:0]

		case "thinking":
			traj.Steps = append(traj.Steps, Step{Index: len(traj.Steps), Time: ts, Kind: StepThinking, InputText: message})

		case "user", "result":
			// The replayed prompt / the turn's final result — neither is a
			// step (mirrors JSONL processUser dropping the plain-string
			// shape), both end the current turn.
			queue = queue[:0]

		case "system":
			// tool_progress heartbeats (raw JSON, see LEARN-TASKS.md LN-17
			// "побочная находка") and other administrative notices form no
			// step and must not break an in-flight call's queue position.

		default:
			// Unrecognised level — tolerate forward compatibility silently.
		}
	}
	if err := sc.Err(); err != nil {
		return traj, err
	}
	return traj, nil
}

var (
	mdHeaderRe = regexp.MustCompile(`^# Session log — (.+)$`)
	mdSavedRe  = regexp.MustCompile(`^_Saved ([^,]+), \d+ entries\._$`)
	mdEntryRe  = regexp.MustCompile("^- `(\\d{2}:\\d{2}:\\d{2})` \\*\\*(\\w+)\\*\\* (.*)$")
	mdToolRe   = regexp.MustCompile("^  - tool: `([^`]*)` (.*)$")
)

// combineTime folds an HH:MM:SS entry time onto the calendar date of the
// file's "_Saved ..." timestamp — the only date the markdown format carries.
// A run that crosses midnight (rare — one file is one completed run) ends up
// with a same-day timestamp for its last entries; nothing downstream (LN-11,
// LN-16, LN-18) depends on cross-midnight ordering within a single file.
func combineTime(day time.Time, hms string) time.Time {
	if day.IsZero() {
		return time.Time{}
	}
	t, err := time.Parse("15:04:05", hms)
	if err != nil {
		return day
	}
	return time.Date(day.Year(), day.Month(), day.Day(), t.Hour(), t.Minute(), t.Second(), 0, day.Location())
}
