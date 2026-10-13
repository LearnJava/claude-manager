package session

import (
	"math"

	"claude-manager/internal/logger"
	"claude-manager/internal/store"
)

// Adaptive --max-turns (PLAN.md 20.3.5, [optimization] max_turns_auto_adjust).
//
// The cap is only applied once the session has enough finished runs to
// judge its real appetite, and it never goes below max_turns_default: it
// exists to stop a run that loops far past anything this session has ever
// needed, not to trim normal work. A run that hits it ends with
// error_max_turns, which the Run loop already treats as "unfinished" — an
// autonomous session simply continues the task in its next run.
const (
	maxTurnsHistoryRuns = 20  // how many recent runs are looked at
	maxTurnsMinSamples  = 5   // fewer finished runs than this → no cap
	maxTurnsHeadroom    = 1.6 // cap = worst observed run × headroom
)

// adaptiveMaxTurns returns the --max-turns value for the next launch of
// project/name, or 0 for "no cap". Reads the live config, so a change made
// in the UI applies to the next launch without an app restart.
func (m *SessionManager) adaptiveMaxTurns(project, name string) int {
	m.mu.Lock()
	opt := m.cfg.Optimization
	m.mu.Unlock()
	if !opt.MaxTurnsAutoAdjust || m.store == nil {
		return 0
	}
	runs, err := m.store.ListRuns(project, name, maxTurnsHistoryRuns)
	if err != nil {
		logger.L.Warn("session.max_turns.history_failed", "project", project, "session", name, "err", err)
		return 0
	}
	return maxTurnsFromHistory(runs, opt.MaxTurnsDefault)
}

// maxTurnsFromHistory is the pure part of adaptiveMaxTurns: worst observed
// turn count of finished runs × maxTurnsHeadroom, floored at floor.
func maxTurnsFromHistory(runs []*store.SessionRun, floor int) int {
	worst, samples := 0, 0
	for _, r := range runs {
		if r == nil || r.NumTurns <= 0 || r.Status == "" || r.Status == "running" {
			continue
		}
		samples++
		if r.NumTurns > worst {
			worst = r.NumTurns
		}
	}
	if samples < maxTurnsMinSamples {
		return 0
	}
	limit := int(math.Ceil(float64(worst) * maxTurnsHeadroom))
	if limit < floor {
		limit = floor
	}
	return limit
}

// excludeDynamicSystemPrompt reports whether launches should pass
// --exclude-dynamic-system-prompt-sections (PLAN.md 20.3.2): cwd, env and
// git status move from the system prompt into the first user message, so
// the system prompt is byte-identical across sessions and the prompt cache
// hits. Live config read, like adaptiveMaxTurns.
func (m *SessionManager) excludeDynamicSystemPrompt() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg.Optimization.ExcludeDynamicSystemPrompt
}
