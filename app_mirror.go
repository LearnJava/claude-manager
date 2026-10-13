package main

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/logger"
	"claude-manager/internal/mirror"
)

// ---- Project mirror (internal/mirror) ----
//
// A project with mirror = true gets a local copy of its settings and saved
// logs under the manager's mirror folder, re-synced on start, after every
// settings save, on a timer and from the Settings button.

// ProjectMirrorStatus is what Settings shows for one project.
type ProjectMirrorStatus struct {
	Enabled  bool   `json:"enabled"`
	Dir      string `json:"dir"`      // the project's folder inside the mirror
	LastSync string `json:"lastSync"` // RFC3339, "" before the first sync this run
	Files    int    `json:"files"`
	Error    string `json:"error"`
}

type mirrorState struct {
	mu   sync.Mutex
	last map[string]mirrorRun // by project name
	kick chan struct{}
}

type mirrorRun struct {
	at     time.Time
	result mirror.Result
}

var mirrorSt = mirrorState{last: map[string]mirrorRun{}, kick: make(chan struct{}, 1)}

// startMirrorLoop syncs once shortly after start, then on every kick (settings
// saved) and every mirror_interval_min minutes.
func (a *App) startMirrorLoop(ctx context.Context) {
	go func() {
		defer logger.Recover("mirror.loop")
		first := time.NewTimer(10 * time.Second)
		defer first.Stop()
		for {
			// Re-read the interval each round so a settings change applies.
			var tick <-chan time.Time
			var timer *time.Timer
			if a.cfg != nil && a.cfg.Settings.MirrorIntervalMin > 0 {
				timer = time.NewTimer(time.Duration(a.cfg.Settings.MirrorIntervalMin) * time.Minute)
				tick = timer.C
			}
			select {
			case <-ctx.Done():
				return
			case <-first.C:
			case <-mirrorSt.kick:
			case <-tick:
			}
			if timer != nil {
				timer.Stop()
			}
			if _, err := a.syncMirrors(ctx); err != nil {
				logger.L.Warn("mirror.sync", "error", err)
			}
		}
	}()
}

// kickMirror asks the loop for a sync without blocking.
func kickMirror() {
	select {
	case mirrorSt.kick <- struct{}{}:
	default:
	}
}

func (a *App) mirrorRoot() string {
	if a.cfg == nil {
		return mirror.DefaultRoot()
	}
	return mirror.Root(a.cfg.Settings)
}

// syncMirrors runs one sync of every opted-in project and records the results.
func (a *App) syncMirrors(ctx context.Context) ([]mirror.Result, error) {
	if a.cfg == nil {
		return nil, nil
	}
	projects := append([]config.ProjectConfig(nil), a.cfg.Projects...)
	enabled := false
	for _, p := range projects {
		enabled = enabled || p.Mirror
	}
	if !enabled {
		return nil, nil
	}
	results, err := mirror.SyncAll(ctx, a.mirrorRoot(), projects)
	now := time.Now()
	mirrorSt.mu.Lock()
	for _, r := range results {
		mirrorSt.last[r.Project] = mirrorRun{at: now, result: r}
	}
	mirrorSt.mu.Unlock()
	for _, r := range results {
		if r.Error != "" {
			logger.L.Warn("mirror.project", "project", r.Project, "error", r.Error)
		} else if r.Changed > 0 {
			logger.L.Info("mirror.project", "project", r.Project, "changed", r.Changed)
		}
	}
	return results, err
}

// MirrorProject turns on the mirror for project ("Mirror into Claude Manager")
// and syncs it right away.
func (a *App) MirrorProject(project string) (ProjectMirrorStatus, error) {
	if err := a.setProjectMirror(project, true); err != nil {
		return ProjectMirrorStatus{}, err
	}
	if _, err := a.syncMirrors(a.bgCtx()); err != nil {
		return a.GetProjectMirror(project), err
	}
	st := a.GetProjectMirror(project)
	if st.Error != "" {
		return st, fmt.Errorf("%s", st.Error)
	}
	return st, nil
}

// UnmirrorProject stops re-syncing project. The copy and its history stay in
// the mirror folder.
func (a *App) UnmirrorProject(project string) error {
	return a.setProjectMirror(project, false)
}

// SyncProjectMirrors re-syncs every mirrored project now.
func (a *App) SyncProjectMirrors() ([]mirror.Result, error) {
	return a.syncMirrors(a.bgCtx())
}

// GetProjectMirror reports project's mirror state for Settings.
func (a *App) GetProjectMirror(project string) ProjectMirrorStatus {
	st := ProjectMirrorStatus{}
	if a.cfg != nil {
		for _, p := range a.cfg.Projects {
			if p.Name == project {
				st.Enabled = p.Mirror
				st.Dir = filepath.Join(a.mirrorRoot(), mirror.DirName(p.Name))
			}
		}
	}
	mirrorSt.mu.Lock()
	run, ok := mirrorSt.last[project]
	mirrorSt.mu.Unlock()
	if ok {
		st.LastSync = run.at.Format(time.RFC3339)
		st.Files = run.result.Files
		st.Error = run.result.Error
	}
	return st
}

func (a *App) setProjectMirror(project string, on bool) error {
	if a.cfg == nil {
		return fmt.Errorf("config not loaded")
	}
	next := *a.cfg
	next.Projects = append([]config.ProjectConfig(nil), a.cfg.Projects...)
	found := false
	for i := range next.Projects {
		if next.Projects[i].Name == project {
			next.Projects[i].Mirror = on
			found = true
		}
	}
	if !found {
		return fmt.Errorf("project %q not found", project)
	}
	return a.UpdateConfig(next)
}

func (a *App) bgCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
