package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/suggest"
)

// suggestMu serializes scans with accepts: an accept rewrites the task file and
// a queue, and a scan reads them to tell what was already added.
var suggestMu sync.Mutex

// SuggestionsState is GetSuggestions' answer. Enabled mirrors [optimization]
// repo_suggestions; with it off nothing was scanned. QueueFiles are the
// project's task_source files — the queues AcceptSuggestions may append to.
type SuggestionsState struct {
	Enabled    bool
	Items      []suggest.Suggestion
	QueueFiles []string
	TaskFile   string
}

func (a *App) suggestEnabled() error {
	if a.cfg == nil || !a.cfg.Optimization.RepoSuggestions {
		return fmt.Errorf("repository suggestions are off: set [optimization] repo_suggestions = true")
	}
	return nil
}

// projectQueueFiles lists the distinct non-empty task sources of a project's
// sessions, in order.
func projectQueueFiles(p *config.ProjectConfig) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range p.Sessions {
		ts := strings.TrimSpace(s.TaskSource)
		if ts != "" && !seen[ts] {
			seen[ts] = true
			out = append(out, ts)
		}
	}
	return out
}

func (a *App) suggestProject(project string) (*config.ProjectConfig, error) {
	if a.cfg == nil {
		return nil, fmt.Errorf("no config loaded")
	}
	for i := range a.cfg.Projects {
		if a.cfg.Projects[i].Name == project {
			if a.cfg.Projects[i].Path == "" {
				return nil, fmt.Errorf("project %q has no folder configured", project)
			}
			return &a.cfg.Projects[i], nil
		}
	}
	return nil, fmt.Errorf("project %q not found", project)
}

func (a *App) scanSuggestions(ctx context.Context, p *config.ProjectConfig) ([]suggest.Suggestion, error) {
	o := suggest.Options{
		Root:       p.Path,
		QueueFiles: projectQueueFiles(p),
		DocSync:    p.DocSync,
		StaleDays:  a.cfg.Optimization.SuggestStaleDays,
	}
	if a.store != nil {
		if g, err := a.store.LastGateRun(p.Name); err == nil && g != nil {
			o.LastGate = &suggest.GateRun{Passed: g.Passed, Head: g.Head, Summary: g.Summary, At: g.TS}
		}
	}
	return suggest.Scan(ctx, o)
}

// GetSuggestions scans a project for work that keeps being postponed — stale
// TODO/FIXME, red gates, code/doc drift, broken queue pointers, abandoned
// branches and work folders — and returns it as a checklist (WASTE-TASKS.md
// WS-07). Read only; no model is called. Empty and Enabled=false unless
// [optimization] repo_suggestions is on.
func (a *App) GetSuggestions(project string) (*SuggestionsState, error) {
	out := &SuggestionsState{Items: []suggest.Suggestion{}, QueueFiles: []string{}, TaskFile: suggest.TaskFile}
	if a.cfg == nil || !a.cfg.Optimization.RepoSuggestions {
		return out, nil
	}
	p, err := a.suggestProject(project)
	if err != nil {
		return nil, err
	}
	out.Enabled = true
	out.QueueFiles = projectQueueFiles(p)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	suggestMu.Lock()
	defer suggestMu.Unlock()
	if out.Items, err = a.scanSuggestions(ctx, p); err != nil {
		return nil, err
	}
	return out, nil
}

// AcceptSuggestions writes the suggestions with the given keys as tasks into
// SUGGESTED-TASKS.md and appends their pointers to queueFile (one of the
// project's task sources). The keys come from the user's ticks; the text is
// taken from a fresh scan, never from the caller. Returns the pointers added.
func (a *App) AcceptSuggestions(project, queueFile string, keys []string) ([]string, error) {
	if err := a.suggestEnabled(); err != nil {
		return nil, err
	}
	p, err := a.suggestProject(project)
	if err != nil {
		return nil, err
	}
	queueFile = strings.TrimSpace(queueFile)
	allowed := false
	for _, q := range projectQueueFiles(p) {
		allowed = allowed || q == queueFile
	}
	if !allowed {
		return nil, fmt.Errorf("%q is not a task source of project %q", queueFile, project)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	suggestMu.Lock()
	defer suggestMu.Unlock()
	found, err := a.scanSuggestions(ctx, p)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, k := range keys {
		want[k] = true
	}
	var picked []suggest.Suggestion
	for _, s := range found {
		if want[s.Key] {
			picked = append(picked, s)
		}
	}
	return suggest.Accept(p.Path, queueFile, picked)
}
