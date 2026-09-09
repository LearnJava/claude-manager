package experience

import (
	"fmt"
	"strings"

	"claude-manager/internal/analysis"
)

// MaxHandoffSteps caps how many of the interrupted run's most recent
// transcript steps feed the handoff distillation (LEARN-TASKS.md LN-15) —
// enough to reconstruct the tail of the conversation without re-inflating
// the very prefix a context-restart exists to avoid.
const MaxHandoffSteps = 40

// HandoffMarker opens the rendered handoff text sent as the first user turn
// of a fresh, non-resumed process (see RenderHandoffPrompt) — a fixed string
// so callers (and tests) can detect a handoff-driven restart the same way
// the context-primer block is detected by its own marker.
const HandoffMarker = "--- Session handoff (context restart) ---"

// recentStepLines renders the tail of steps (oldest of the tail first) as
// one-line strings for the distillation prompt: a tool call becomes
// "ToolName: <one-line input>", prose is passed through as-is. Empty/failed
// transcript reads simply produce no lines — RecentSteps is best-effort.
func recentStepLines(steps []Step, n int) []string {
	if len(steps) > n {
		steps = steps[len(steps)-n:]
	}
	lines := make([]string, 0, len(steps))
	for _, st := range steps {
		switch st.Kind {
		case StepToolUse:
			lines = append(lines, fmt.Sprintf("%s: %s", st.ToolName, st.InputText))
		case StepText, StepThinking:
			if strings.TrimSpace(st.InputText) != "" {
				lines = append(lines, st.InputText)
			}
		}
	}
	return lines
}

// BuildHandoffInput reads the tail of the interrupted CLI session's own
// transcript (LN-01, best-effort — a backend that never wrote one, e.g.
// fakeclaude in tests, simply yields no RecentSteps) and combines it with the
// live TodoWrite state into analysis.HandoffInput, the material
// analysis.GenerateHandoff distills into a compact recap (LEARN-TASKS.md
// LN-15).
func BuildHandoffInput(transcriptsRoot, projectPath, cliSessionID, taskDesc string, todos []string) analysis.HandoffInput {
	in := analysis.HandoffInput{
		TaskPtr: taskDesc,
		Todos:   todos,
	}
	path, err := FindTranscript(transcriptsRoot, projectPath, cliSessionID)
	if err != nil {
		return in
	}
	traj, err := Read(path)
	if err != nil {
		return in
	}
	in.RecentSteps = recentStepLines(traj.Steps, MaxHandoffSteps)
	return in
}

// RenderHandoffPrompt formats a distilled HandoffResult into the text sent as
// the first user turn of the fresh, non-resumed process. Never empty as long
// as res is non-nil — even a distillation with nothing but "done"/"remaining"
// still forms a usable handoff.
func RenderHandoffPrompt(res *analysis.HandoffResult) string {
	if res == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(HandoffMarker + "\n")
	if strings.TrimSpace(res.Done) != "" {
		b.WriteString("Done so far: " + res.Done + "\n")
	}
	if strings.TrimSpace(res.Remaining) != "" {
		b.WriteString("Remaining: " + res.Remaining + "\n")
	}
	if len(res.Decisions) > 0 {
		b.WriteString("Decisions already made:\n")
		for _, d := range res.Decisions {
			b.WriteString("- " + d + "\n")
		}
	}
	if len(res.FilesChanged) > 0 {
		b.WriteString("Files already touched:\n")
		for _, f := range res.FilesChanged {
			b.WriteString("- " + f + "\n")
		}
	}
	b.WriteString("--- end handoff ---\n\n")
	b.WriteString("The previous process for this same task was restarted because its context " +
		"window filled up. Continue the task from the state above — do not restart it from " +
		"scratch, and do not assume any conversation history beyond this handoff.")
	return b.String()
}
