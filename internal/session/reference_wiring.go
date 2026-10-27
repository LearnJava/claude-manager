package session

import (
	"time"

	"claude-manager/internal/logger"
)

// ReferenceCaseInfo is a closed task together with what the manager measured
// for it: input+output tokens (cache excluded — the figure a one-shot replay
// reports too) and the wall-clock time of its run.
type ReferenceCaseInfo struct {
	Project string
	RefCaseRecord
	Tokens      int64
	DurationSec int
	Model       string
	Effort      string
}

// ReferenceCaseFunc stores one reference case. Wired from app.go to
// internal/experience for the usual import-cycle reason (see ActionIndexFunc).
type ReferenceCaseFunc func(info ReferenceCaseInfo) error

// SetReferenceCases wires the recorder of WASTE-TASKS.md WS-06. Nothing is
// captured or stored unless [optimization] reference_cases is on.
func (m *SessionManager) SetReferenceCases(fn ReferenceCaseFunc) {
	m.mu.Lock()
	m.refCaseFn = fn
	m.mu.Unlock()
}

// referenceCaseInfo snapshots the run's measurements for a closed task; it
// must run before finishRun closes the run. nil when nothing is to be stored.
func (m *SessionManager) referenceCaseInfo(ms *managedSession, rec *RefCaseRecord) *ReferenceCaseInfo {
	if rec == nil {
		return nil
	}
	m.mu.Lock()
	fn := m.refCaseFn
	on := m.cfg != nil && m.cfg.Optimization.ReferenceCases
	m.mu.Unlock()
	if fn == nil || !on {
		return nil
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	info := &ReferenceCaseInfo{
		Project:       ms.project,
		RefCaseRecord: *rec,
		Tokens:        ms.inputTokens + ms.outputTokens,
		Model:         ms.runModel,
		Effort:        ms.session.Config.Effort,
	}
	if info.Model == "" {
		info.Model = ms.session.Config.Model
	}
	if !ms.runStartedAt.IsZero() {
		info.DurationSec = int(time.Since(ms.runStartedAt).Seconds())
	}
	return info
}

// recordReferenceCase stores the snapshot; a failure is logged, never fatal.
func (m *SessionManager) recordReferenceCase(info *ReferenceCaseInfo) {
	if info == nil {
		return
	}
	m.mu.Lock()
	fn := m.refCaseFn
	m.mu.Unlock()
	if fn == nil {
		return
	}
	if err := fn(*info); err != nil {
		logger.L.Error("session.refcase_record_failed", "project", info.Project, "task", info.TaskPtr, "error", err)
	}
}
