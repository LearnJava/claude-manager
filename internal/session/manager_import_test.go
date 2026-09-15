package session

import (
	"path/filepath"
	"testing"
)

// stubLogImporter returns a fixed ImportStats and records every (dir,
// project, projectPath) it was called with — the ImportLogsFunc counterpart
// of stubSkillDistiller.
func stubLogImporter(stats ImportStats) (ImportLogsFunc, *[][3]string) {
	var calls [][3]string
	fn := func(dir, project, projectPath string, onProgress func(processed, total int)) (ImportStats, error) {
		calls = append(calls, [3]string{dir, project, projectPath})
		if onProgress != nil {
			onProgress(1, 1)
		}
		return stats, nil
	}
	return fn, &calls
}

func TestImportProjectLogsDisabledByDefault(t *testing.T) {
	m := newTestManager(t) // ExperienceTracking is false by default
	importFn, _ := stubLogImporter(ImportStats{Files: 1})
	m.SetLogImporter(importFn)

	if _, err := m.ImportProjectLogs("lumen", ""); err == nil {
		t.Fatal("expected error when experience_tracking is disabled")
	}
}

func TestImportProjectLogsNoImporterWired(t *testing.T) {
	m := newTestManager(t)
	m.cfg.Optimization.ExperienceTracking = true

	if _, err := m.ImportProjectLogs("lumen", ""); err == nil {
		t.Fatal("expected error when no importer is wired")
	}
}

func TestImportProjectLogsUnknownProject(t *testing.T) {
	m := newTestManager(t)
	m.cfg.Optimization.ExperienceTracking = true
	importFn, _ := stubLogImporter(ImportStats{})
	m.SetLogImporter(importFn)

	if _, err := m.ImportProjectLogs("no-such", ""); err == nil {
		t.Fatal("expected error for unknown project")
	}
}

func TestImportProjectLogsDefaultDir(t *testing.T) {
	m := newTestManager(t)
	m.cfg.Optimization.ExperienceTracking = true
	importFn, calls := stubLogImporter(ImportStats{Files: 3, Runs: 2, Actions: 10, Skipped: 1})
	m.SetLogImporter(importFn)

	var projPath string
	for _, p := range m.cfg.Projects {
		if p.Name == "lumen" {
			projPath = p.Path
		}
	}

	stats, err := m.ImportProjectLogs("lumen", "")
	if err != nil {
		t.Fatalf("ImportProjectLogs: %v", err)
	}
	if stats.Files != 3 || stats.Runs != 2 || stats.Actions != 10 || stats.Skipped != 1 {
		t.Errorf("unexpected stats: %+v", stats)
	}
	if len(*calls) != 1 {
		t.Fatalf("expected 1 importer call, got %d", len(*calls))
	}
	got := (*calls)[0]
	wantDir := filepath.Join(projPath, ".claude-manager", "logs")
	if got[0] != wantDir || got[1] != "lumen" || got[2] != projPath {
		t.Errorf("unexpected call args: %v (want dir=%q)", got, wantDir)
	}
}

func TestImportProjectLogsExplicitDir(t *testing.T) {
	m := newTestManager(t)
	m.cfg.Optimization.ExperienceTracking = true
	importFn, calls := stubLogImporter(ImportStats{})
	m.SetLogImporter(importFn)

	if _, err := m.ImportProjectLogs("lumen", "/some/brought/corpus"); err != nil {
		t.Fatalf("ImportProjectLogs: %v", err)
	}
	if len(*calls) != 1 || (*calls)[0][0] != "/some/brought/corpus" {
		t.Errorf("expected explicit dir to be passed through unchanged, got %v", *calls)
	}
}
