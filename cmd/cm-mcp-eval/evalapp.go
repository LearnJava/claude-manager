package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"claude-manager/internal/analysis"
	"claude-manager/internal/config"
	"claude-manager/internal/envcheck"
	"claude-manager/internal/experience"
	"claude-manager/internal/optimization"
	"claude-manager/internal/session"
	"claude-manager/internal/store"
)

// evalApp is the part of the Claude Manager App a headless instance needs to
// serve the cm-mcp tools: the config round-trip plus thin wrappers over the
// same internal packages the real App wraps (roadmap files, experience
// tables, skills). Where the real App needs the GUI, the method says so.
type evalApp struct {
	mu   sync.Mutex
	cfg  *config.AppConfig
	path string
	mgr  *session.SessionManager
	st   *store.Store
}

var errNoGUI = errors.New("not available in the evaluation instance")

func (a *evalApp) snapshot() config.AppConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	c := *a.cfg
	c.Projects = append([]config.ProjectConfig(nil), a.cfg.Projects...)
	return c
}

func (a *evalApp) GetConfig() *config.AppConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg
}

func (a *evalApp) UpdateConfig(cfg config.AppConfig) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := config.Save(&cfg, a.path); err != nil {
		return err
	}
	a.cfg = &cfg
	a.mgr.SetConfig(&cfg)
	return nil
}

func (a *evalApp) projectPath(project string) (string, error) {
	for _, p := range a.snapshot().Projects {
		if p.Name == project && p.Path != "" {
			return p.Path, nil
		}
	}
	return "", fmt.Errorf("project %q not found", project)
}

// ProjectsApp.

func (a *evalApp) CheckEnvironment() envcheck.Report {
	return envcheck.Check(a.snapshot().Settings.ClaudePath)
}

func (a *evalApp) HasClaudeMd(projectPath string) bool {
	_, err := os.Stat(filepath.Join(projectPath, "CLAUDE.md"))
	return err == nil
}

func (a *evalApp) HasSessionProtocol(project string) (bool, error) { return a.mgr.HasProtocol(project) }
func (a *evalApp) InstallSessionProtocol(string) ([]string, error) { return nil, errNoGUI }
func (a *evalApp) GenerateClaudeMdSession(string) error            { return errNoGUI }
func (a *evalApp) InitGitRepo(string) error                        { return errNoGUI }
func (a *evalApp) MirrorProjectStatus(string) (any, error)         { return nil, errNoGUI }
func (a *evalApp) UnmirrorProject(string) error                    { return errNoGUI }
func (a *evalApp) SyncProjectMirrorsResults() (any, error)         { return nil, errNoGUI }
func (a *evalApp) MirrorStatusOf(string) any                       { return nil }

// PlanningApp.

func (a *evalApp) ApproveRoadmap(planID int64, overwrite bool) (*analysis.TaskPlan, error) {
	plan, _, _, err := a.mgr.ApproveRoadmapFiles(planID, overwrite)
	if err != nil {
		return nil, err
	}
	cfg := a.snapshot()
	for pi := range cfg.Projects {
		if cfg.Projects[pi].Name != plan.Project {
			continue
		}
		sessions := append([]config.SessionConfig(nil), cfg.Projects[pi].Sessions...)
		found := false
		for si := range sessions {
			if sessions[si].Name == "P1" {
				sessions[si].TaskSource = "STATUS-P1.md"
				found = true
			}
		}
		if !found {
			sessions = append(sessions, config.SessionConfig{
				Name: "P1", Model: "sonnet", TaskSource: "STATUS-P1.md",
				AutoRestart: true, Prompt: analysis.DefaultP1SessionPrompt,
			})
		}
		cfg.Projects[pi].Sessions = sessions
	}
	if err := a.UpdateConfig(cfg); err != nil {
		return nil, err
	}
	return plan, nil
}

func (a *evalApp) GetSessionRoadmap(project, sessionName string) (*analysis.RoadmapView, error) {
	path, err := a.projectPath(project)
	if err != nil {
		return nil, err
	}
	taskSource := ""
	for _, p := range a.snapshot().Projects {
		for _, s := range p.Sessions {
			if p.Name == project && s.Name == sessionName {
				taskSource = s.TaskSource
			}
		}
	}
	view, err := analysis.ReadRoadmap(path, taskSource)
	if errors.Is(err, analysis.ErrNoRoadmap) {
		return nil, nil
	}
	return view, err
}

func (a *evalApp) GetRoadmapTaskDetail(project, relPath string) (string, error) {
	path, err := a.projectPath(project)
	if err != nil {
		return "", err
	}
	return analysis.ReadRoadmapTaskDetail(path, relPath)
}

func (a *evalApp) GetRoadmapRowDetail(string, string, int) (string, error) { return "", errNoGUI }

// SkillsApp: candidates and the skill list are real; the model-run and file
// actions need the GUI app.

func (a *evalApp) GetSkillCandidates(project string) ([]experience.SkillCandidate, error) {
	return experience.MineProjectCandidates(a.st, project)
}

func (a *evalApp) DistillSkill(string, experience.SkillCandidate, []string, string, float64) (*store.Skill, error) {
	return nil, errNoGUI
}

func (a *evalApp) GetSkills(project string) ([]store.Skill, error)  { return a.st.ListSkills(project) }
func (a *evalApp) ApproveSkill(int64, string, bool) (string, error) { return "", errNoGUI }
func (a *evalApp) ArchiveSkill(int64) error                         { return errNoGUI }
func (a *evalApp) RestoreSkill(int64, bool) (string, error)         { return "", errNoGUI }

func (a *evalApp) GetSkillUsage(string) (map[int64]experience.SkillUsage, error) {
	return map[int64]experience.SkillUsage{}, nil
}

func (a *evalApp) GetSkillQuality(string) ([]experience.SkillEffect, error) { return nil, nil }
func (a *evalApp) SkillAutopilotOf(string) (any, error)                     { return nil, errNoGUI }
func (a *evalApp) SetSkillAutopilot(string, bool, float64) error            { return errNoGUI }

func (a *evalApp) RunSkillAutopilot(string) (experience.SkillPilotReport, error) {
	return experience.SkillPilotReport{}, errNoGUI
}

// LogsApp.

func (a *evalApp) GetProjectLogFiles(string) ([]store.LogFileInfo, error) { return nil, nil }
func (a *evalApp) ClearProjectLogs(string) error                          { return errNoGUI }
func (a *evalApp) CleanOldLogs(int) error                                 { return errNoGUI }

func (a *evalApp) ImportProjectLogs(string, string) (session.ImportStats, error) {
	return session.ImportStats{}, errNoGUI
}

func (a *evalApp) GetTopActions(project string, days int) ([]store.SignatureStat, error) {
	return a.st.TopSignatures(project, days, 0)
}

func (a *evalApp) GetActionSamples(project, sig string, limit int) ([]store.ActionRow, error) {
	return a.st.ActionSamples(project, sig, limit)
}

func (a *evalApp) GetDurationProfile(string) ([]experience.SignatureDuration, error) { return nil, nil }

func (a *evalApp) GetTokenAttribution(string, int) (experience.AttributionReport, error) {
	return experience.AttributionReport{}, nil
}

func (a *evalApp) GetWasteReport(string) (experience.WasteReport, error) {
	return experience.WasteReport{}, nil
}

func (a *evalApp) GetTaskLabels(string) (experience.LabelReport, error) {
	return experience.LabelReport{}, nil
}

func (a *evalApp) GetReferenceReport(string) (experience.ReferenceReport, error) {
	return experience.ReferenceReport{}, nil
}

func (a *evalApp) ReplayReferenceCase(string, int64, string, string) (*store.ReferenceReplay, error) {
	return nil, nil
}

func (a *evalApp) RunBenchmark(string, experience.BenchmarkSpec) (experience.BenchmarkReport, error) {
	return experience.BenchmarkReport{}, nil
}

func (a *evalApp) GetBenchmarkReport(string, int64) (experience.BenchmarkReport, error) {
	return experience.BenchmarkReport{}, nil
}

func (a *evalApp) GetModelRecommendation(string, string) (*optimization.ModelRecommendation, error) {
	return nil, nil
}

func (a *evalApp) GetAutoModelRouting() bool { return false }

// InteractionApp.

func (a *evalApp) GetPermissionCandidates(project string, days int) (*experience.CandidateSet, error) {
	stats, err := a.st.TopPermissionEvents(project, days, 0)
	if err != nil {
		return nil, err
	}
	safe, review := experience.Candidates(stats, experience.MinPermissionCount)
	return &experience.CandidateSet{Safe: safe, NeedsReview: review}, nil
}

func (a *evalApp) AddPermissionRule(project, sessionName, tool, pattern, decision string) error {
	cfg := a.snapshot()
	for pi := range cfg.Projects {
		if cfg.Projects[pi].Name != project {
			continue
		}
		sessions := append([]config.SessionConfig(nil), cfg.Projects[pi].Sessions...)
		for si := range sessions {
			if sessions[si].Name == sessionName {
				sessions[si].PermissionRules = append(append([]config.PermissionRule(nil), sessions[si].PermissionRules...),
					config.PermissionRule{Tool: tool, Pattern: pattern, Decision: decision})
				cfg.Projects[pi].Sessions = sessions
				return a.UpdateConfig(cfg)
			}
		}
	}
	return fmt.Errorf("session %s/%s not found", project, sessionName)
}
