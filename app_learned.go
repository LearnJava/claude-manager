package main

import (
	"fmt"
	"sync"
	"time"

	"claude-manager/internal/learned"
)

// ---- Learned files (internal/learned, WASTE-TASKS.md WS-09) ----
//
// The database stays the working source of truth for skills, rules and bans;
// <project>/.claude-manager/learned/ holds them as plain files to commit,
// revert with git and carry to another machine. GetLearnedFiles only compares;
// ApplyLearnedFiles writes into the project, and only for the items the user
// ticked.

// learnedMu serializes comparisons and applies of lesson files.
var learnedMu sync.Mutex

// LearnedItem is one lesson in GetLearnedFiles' answer. Action is one of
// learned.Action*; DBStatus / FileStatus are the lesson's status on each side
// ("" = absent there).
type LearnedItem struct {
	Kind       string
	Key        string
	Title      string
	Action     string
	DBStatus   string
	FileStatus string
	Detail     string
}

// LearnedState is GetLearnedFiles' answer. Enabled mirrors [optimization]
// learned_files; with it off nothing was read. Problems lists lesson files that
// could not be read, "name: reason".
type LearnedState struct {
	Enabled  bool
	Dir      string
	Items    []LearnedItem
	Problems []string
}

// LearnedPick is one ticked item. Resolution ("file" or "db") settles a
// conflict, where the two sides differ and neither is known to be the newer.
type LearnedPick struct {
	Kind       string
	Key        string
	Resolution string
}

// LearnedResult reports ApplyLearnedFiles line by line.
type LearnedResult struct {
	Done   []string
	Failed []string
}

func (a *App) learnedEnabled() error {
	if a.cfg == nil || !a.cfg.Optimization.LearnedFiles {
		return fmt.Errorf("learned files are off: set [optimization] learned_files = true")
	}
	if a.store == nil {
		return fmt.Errorf("no database")
	}
	return nil
}

// GetLearnedFiles compares the project's lesson files with the lessons in the
// database and lists what differs: lessons to export, files to import, files
// whose removal retires a lesson, and conflicts. Reads files and the database;
// writes nothing into the project. Empty and Enabled=false unless [optimization]
// learned_files is on.
func (a *App) GetLearnedFiles(project string) (*LearnedState, error) {
	out := &LearnedState{Dir: learned.Dir, Items: []LearnedItem{}, Problems: []string{}}
	if a.cfg == nil || !a.cfg.Optimization.LearnedFiles {
		return out, nil
	}
	if err := a.learnedEnabled(); err != nil {
		return nil, err
	}
	p, err := a.suggestProject(project)
	if err != nil {
		return nil, err
	}
	out.Enabled = true
	learnedMu.Lock()
	defer learnedMu.Unlock()
	st, err := learned.Load(a.store, project, p.Path, time.Now())
	if err != nil {
		return nil, err
	}
	for _, it := range st.Items {
		if it.Action == learned.ActionSame {
			continue
		}
		out.Items = append(out.Items, LearnedItem{Kind: it.Kind, Key: it.Key, Title: it.Title, Action: it.Action,
			DBStatus: it.DBStatus, FileStatus: it.FileStatus, Detail: it.Detail})
	}
	out.Problems = append(out.Problems, st.Problems...)
	return out, nil
}

// ApplyLearnedFiles carries out the ticked items: writes or removes lesson
// files in the project, or imports / retires lessons in the database. The plan
// is recomputed here; the picks only name items. A conflict without a
// resolution is reported in Failed and left alone. Excluded from MCP: it
// writes into the user's repository only on a tick in the UI.
func (a *App) ApplyLearnedFiles(project string, picks []LearnedPick) (*LearnedResult, error) {
	if err := a.learnedEnabled(); err != nil {
		return nil, err
	}
	p, err := a.suggestProject(project)
	if err != nil {
		return nil, err
	}
	ps := make([]learned.Pick, 0, len(picks))
	for _, x := range picks {
		ps = append(ps, learned.Pick{Kind: x.Kind, Key: x.Key, Resolution: x.Resolution})
	}
	learnedMu.Lock()
	defer learnedMu.Unlock()
	r, err := learned.Apply(a.store, project, p.Path, ps, time.Now())
	if err != nil {
		return nil, err
	}
	return &LearnedResult{Done: append([]string{}, r.Done...), Failed: append([]string{}, r.Failed...)}, nil
}
