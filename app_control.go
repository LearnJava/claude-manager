package main

import "claude-manager/internal/control"

// The App must keep serving the logs-and-metrics RPCs (MCP-09).
var _ control.LogsApp = (*App)(nil)

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
