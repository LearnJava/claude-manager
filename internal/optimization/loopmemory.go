package optimization

import (
	"fmt"
	"strings"
	"time"
)

// LoopMemory is a session's view of the loop history ([optimization]
// loop_memory, WASTE-TASKS.md WS-04). The manager supplies an implementation
// bound to the session's project, name and run; nil means the feature is off.
type LoopMemory interface {
	// Hint returns what broke an earlier loop on the same call, ready to be
	// shown to the agent, or "" when no such loop is known.
	Hint(tool, input string) string
	// Record stores a detected loop.
	Record(hit *LoopDetected)
}

// PastLoopHint builds the hint for a loop that was broken before. resolvedBy
// is "<tool> <arg>" of the action that broke it, when is when that loop was
// seen. It returns "" when resolvedBy is empty.
func PastLoopHint(resolvedBy string, when, now time.Time) string {
	resolvedBy = strings.TrimSpace(resolvedBy)
	if resolvedBy == "" {
		return ""
	}
	return fmt.Sprintf("You have been stuck on this exact step before (%s). What got past it last time: %s.",
		agoText(now.Sub(when)), clip(resolvedBy, 200))
}

func agoText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "a moment ago"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	}
	return fmt.Sprintf("%d days ago", int(d.Hours()/24))
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
