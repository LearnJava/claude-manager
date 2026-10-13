package session

import (
	"strings"

	"claude-manager/internal/analysis"
	"claude-manager/internal/config"
)

// Pre-flight gate on session start (PLAN.md §17.2, §17.6). The global
// preflight_analysis switch and the per-session preflight mode decide
// whether a start from the UI first runs the analyst on the session's
// prompt:
//
//	never  — start at once (also whenever the global switch is off)
//	auto   — analyse; a single-session task starts at once when
//	         preflight_auto_approve_single is on, anything else is shown
//	         to the user as a plan
//	always — analyse and always show the plan
//
// Only a session that works on its own prompt is analysed: a queue-driven
// session (task_source / auto_restart) takes a new task each run, and a
// session with an empty prompt has nothing to analyse. StartSession itself
// never blocks on this — RPC/MCP and StartProject callers start directly;
// the UI calls SessionPreflight first (Sidebar.svelte beginStart).

// PreflightMode returns the effective mode for a session start.
func PreflightMode(globalOn bool, sc config.SessionConfig) string {
	if !globalOn || strings.TrimSpace(sc.Prompt) == "" || sc.TaskSource != "" || sc.AutoRestart {
		return "never"
	}
	switch sc.Preflight {
	case "always", "never":
		return sc.Preflight
	default:
		return "auto"
	}
}

// SessionPreflight runs the analysis a start of project/name calls for and
// returns the draft plan the user should review, or nil when the session
// should simply start (mode never, or auto with an auto-approved
// single-session verdict).
func (m *SessionManager) SessionPreflight(project, name string) (*analysis.TaskPlan, error) {
	_, sc, err := m.findConfig(project, name)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	globalOn := m.cfg.Settings.PreflightAnalysis
	autoApprove := m.cfg.Settings.PreflightAutoApproveSingle
	m.mu.Unlock()

	mode := PreflightMode(globalOn, *sc)
	if mode == "never" {
		return nil, nil
	}
	plan, err := m.RunPreflight(project, sc.Prompt)
	if err != nil {
		return nil, err
	}
	if mode == "auto" && autoApprove && plan.Analysis.Feasibility.SingleSession {
		return nil, nil
	}
	return plan, nil
}
