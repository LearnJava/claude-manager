package session

import (
	"context"
	"strings"

	"claude-manager/internal/logger"
	"claude-manager/internal/worker"
)

// GateRunFunc stores the outcome of one run of a project's gates: whether it
// passed, the commit it ran on, and for a red run the failed command with the
// tail of its output (WASTE-TASKS.md WS-07). Wired from app.go.
type GateRunFunc func(project string, passed bool, head, summary string) error

// SetGateRuns wires the recorder. Sessions are given it only while
// [optimization] repo_suggestions is on.
func (m *SessionManager) SetGateRuns(fn GateRunFunc) {
	m.mu.Lock()
	m.gateRunFn = fn
	m.mu.Unlock()
}

// gateRunFnFor is the recorder to hand a new session, nil when the repository
// scanner is off or nothing is wired (the default: no extra git, no row).
func (m *SessionManager) gateRunFnFor(project string) GateRunFunc {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.gateRunFn == nil || m.cfg == nil || !m.cfg.Optimization.RepoSuggestions {
		return nil
	}
	return m.gateRunFn
}

// recordGateRun stores res with the commit it ran on; a failure is logged.
func (s *Session) recordGateRun(ctx context.Context, root string, res worker.GateResult) {
	if s.gateRunFn == nil {
		return
	}
	summary := ""
	if !res.Passed {
		summary = strings.TrimSpace(res.Feedback())
		if r := []rune(summary); len(r) > 1500 {
			summary = string(r[len(r)-1500:])
		}
	}
	if err := s.gateRunFn(s.ProjectName, res.Passed, headCommit(ctx, root), summary); err != nil {
		logger.L.Error("session.gate_record_failed", "project", s.ProjectName, "error", err)
	}
}
