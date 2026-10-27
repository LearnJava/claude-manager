package experience

import (
	"strings"
	"time"

	"claude-manager/internal/logger"
	"claude-manager/internal/optimization"
	"claude-manager/internal/store"
)

// Loop memory (WASTE-TASKS.md WS-04). The in-session detector forgets a loop
// when its window moves on; this keeps every detected loop in loop_events,
// finds out afterwards — from the indexed action_signatures of the run —
// which different successful action got the session out of it, and hands that
// back the next time the same call loops.

// LoopMemory is the optimization.LoopMemory of one session.
type LoopMemory struct {
	st          *store.Store
	project     string
	session     string
	projectPath string
	runID       func() int64
	now         func() time.Time
}

// NewLoopMemory binds the history to a session. runID reports the run in
// flight (0 when unknown).
func NewLoopMemory(st *store.Store, project, session, projectPath string, runID func() int64) *LoopMemory {
	return &LoopMemory{st: st, project: project, session: session, projectPath: projectPath,
		runID: runID, now: func() time.Time { return time.Now().UTC() }}
}

// LoopSig is the action_signatures signature of a call as the loop detector
// keys it (optimization.NormalizeInput): the range of a Read and the edited
// text of an Edit are not part of what the indexer signs.
func LoopSig(tool, input, projectPath string) string {
	switch tool {
	case "Read", "Edit":
		if i := strings.Index(input, " @"); i >= 0 {
			input = input[:i]
		}
		if i := strings.Index(input, " | "); i >= 0 {
			input = input[:i]
		}
	}
	sig, _ := Signature(tool, input, projectPath)
	return sig
}

// Hint implements optimization.LoopMemory: what broke the latest earlier loop
// on the same call, or "" when none is known.
func (m *LoopMemory) Hint(tool, input string) string {
	if m == nil || m.st == nil {
		return ""
	}
	prev, err := m.st.LastResolvedLoop(m.project, LoopSig(tool, input, m.projectPath))
	if err != nil {
		logger.L.Warn("experience.loop_hint_failed", "project", m.project, "error", err)
		return ""
	}
	if prev == nil {
		return ""
	}
	return optimization.PastLoopHint(prev.ResolvedBy, prev.Timestamp, m.now())
}

// Record implements optimization.LoopMemory.
func (m *LoopMemory) Record(hit *optimization.LoopDetected) {
	if m == nil || m.st == nil || hit == nil {
		return
	}
	var runID int64
	if m.runID != nil {
		runID = m.runID()
	}
	err := m.st.InsertLoopEvent(&store.LoopEvent{
		Project: m.project, Session: m.session, RunID: runID,
		Sig: LoopSig(hit.Tool, hit.Input, m.projectPath), Tool: hit.Tool, Arg: hit.Input,
		Count: hit.Count, Far: hit.Far, Timestamp: m.now(),
	})
	if err != nil {
		logger.L.Warn("experience.loop_record_failed", "project", m.project, "error", err)
	}
}

// ResolveRunLoops fills resolved_by of a finished run's loops: the first
// successful action after the loop whose signature differs from the loop's.
// A loop the run never got out of stays unresolved and is never offered as a
// hint. Returns the number of events resolved.
func ResolveRunLoops(st *store.Store, runID int64) (int, error) {
	if st == nil || runID == 0 {
		return 0, nil
	}
	events, err := st.UnresolvedLoopEvents(runID)
	if err != nil || len(events) == 0 {
		return 0, err
	}
	actions, err := st.ActionsForRun(runID)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, ev := range events {
		by := firstWayOut(ev, actions)
		if by == "" {
			continue
		}
		if err := st.SetLoopResolved(ev.ID, by); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func firstWayOut(ev *store.LoopEvent, actions []*store.ActionRow) string {
	for _, a := range actions {
		if a.Timestamp.Before(ev.Timestamp) || a.IsError || a.Sig == ev.Sig {
			continue
		}
		by := a.Tool
		if a.Arg != "" {
			by += " " + a.Arg
		} else if a.Sig != "" {
			by += " " + a.Sig
		}
		return by
	}
	return ""
}
