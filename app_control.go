package main

import "claude-manager/internal/control"

// The App must keep serving the logs-and-metrics RPCs (MCP-09).
var _ control.LogsApp = (*App)(nil)

// The App must keep serving the roadmap RPCs (MCP-10).
var _ control.PlanningApp = (*App)(nil)

// controlApp is what the control-plane server gets as its app: *App plus the
// few methods whose result types live in package main, returned as plain
// values so internal/control can serve them (control.ProjectsApp).
type controlApp struct{ *App }

func (c controlApp) MirrorProjectStatus(project string) (any, error) {
	return c.App.MirrorProject(project)
}

func (c controlApp) SyncProjectMirrorsResults() (any, error) {
	return c.App.SyncProjectMirrors()
}

func (c controlApp) MirrorStatusOf(project string) any {
	return c.App.GetProjectMirror(project)
}

// The App, wrapped as controlApp, must keep serving the skills RPCs (MCP-11).
var _ control.SkillsApp = controlApp{}

// SkillAutopilotOf is GetSkillAutopilot with its package-main result type
// returned as a plain value (control.SkillsApp).
func (c controlApp) SkillAutopilotOf(project string) (any, error) {
	return c.App.GetSkillAutopilot(project)
}

// The session RPCs (MCP-23) call these App methods instead of the bare
// manager, so the GUI's extra logic (StartProject's cache-affinity order,
// remembered model/runtime) reaches MCP too.
var _ control.SessionApp = (*App)(nil)

// The interaction RPCs go through the App as well.
var _ control.InteractionApp = (*App)(nil)

// The project RPCs get the App wrapped as controlApp.
var _ control.ProjectsApp = controlApp{}
