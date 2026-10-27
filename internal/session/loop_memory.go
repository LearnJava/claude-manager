package session

import (
	"claude-manager/internal/logger"
	"claude-manager/internal/optimization"
)

// LoopMemoryFactory builds the loop history of one session (WASTE-TASKS.md
// WS-04): project and session name identify it, runID reports the run in
// flight (0 before the run row exists). Declared here and wired from app.go
// to internal/experience for the usual import-cycle reason (see
// ActionIndexFunc).
type LoopMemoryFactory func(project, sessionName, projectPath string, runID func() int64) optimization.LoopMemory

// LoopResolveFunc fills in what broke each loop of a finished run, from the
// run's now-indexed action_signatures. Returns the number of events resolved.
type LoopResolveFunc func(runID int64) (int, error)

// SetLoopMemory wires the loop history. Pass nil factory to disable it; even
// wired, nothing is built or stored unless [optimization] loop_memory is on.
func (m *SessionManager) SetLoopMemory(newMemory LoopMemoryFactory, resolve LoopResolveFunc) {
	m.mu.Lock()
	m.loopMemoryFn = newMemory
	m.loopResolveFn = resolve
	m.mu.Unlock()
}

// loopMemoryFor returns the history for a new session, or nil (feature off).
func (m *SessionManager) loopMemoryFor(ms *managedSession, project, name, projectPath string) optimization.LoopMemory {
	m.mu.Lock()
	fn := m.loopMemoryFn
	on := m.cfg != nil && m.cfg.Optimization.LoopMemory
	m.mu.Unlock()
	if fn == nil || !on {
		return nil
	}
	return fn(project, name, projectPath, func() int64 {
		ms.mu.Lock()
		defer ms.mu.Unlock()
		return ms.runID
	})
}

// resolveLoops runs after a run's transcript was indexed.
func (m *SessionManager) resolveLoops(id string, runID int64) {
	m.mu.Lock()
	fn := m.loopResolveFn
	on := m.cfg != nil && m.cfg.Optimization.LoopMemory
	m.mu.Unlock()
	if fn == nil || !on || runID == 0 {
		return
	}
	n, err := fn(runID)
	if err != nil {
		logger.L.Error("session.loop_resolve_failed", "id", id, "run_id", runID, "error", err)
		return
	}
	if n > 0 {
		logger.L.Info("session.loop_resolved", "id", id, "run_id", runID, "events", n)
	}
}
