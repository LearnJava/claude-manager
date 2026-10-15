package control

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"claude-manager/internal/experience"
	"claude-manager/internal/store"
)

// SkillsApp is the part of *App the experience-layer RPCs need (MCP-11): the
// skill store, the distiller and the autopilot all live in the GUI app. A
// control server without it (playwright-server, tests) reports these methods
// as unavailable. The autopilot state type lives in package main, so the app
// hands it over as a plain value (SkillAutopilotOf).
type SkillsApp interface {
	GetSkillCandidates(project string) ([]experience.SkillCandidate, error)
	DistillSkill(project string, candidate experience.SkillCandidate, gates []string, model string, minScore float64) (*store.Skill, error)
	GetSkills(project string) ([]store.Skill, error)
	ApproveSkill(id int64, md string, overwrite bool) (string, error)
	ArchiveSkill(id int64) error
	RestoreSkill(id int64, overwrite bool) (string, error)
	GetSkillUsage(project string) (map[int64]experience.SkillUsage, error)
	GetSkillQuality(project string) ([]experience.SkillEffect, error)
	SkillAutopilotOf(project string) (any, error)
	SetSkillAutopilot(project string, enabled bool, dailyBudgetUSD float64) error
	RunSkillAutopilot(project string) (experience.SkillPilotReport, error)
}

// parseSkillID reads a skill id sent as a number or a numeric string.
func parseSkillID(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("skill_id is required: take the ID of a skill from get_skills or distill_skill")
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		n, convErr := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if convErr == nil {
			return n, nil
		}
		return 0, fmt.Errorf("skill_id %q is not a number: take the ID of a skill from get_skills, e.g. 7", s)
	}
	return 0, fmt.Errorf("skill_id has unsupported type %s: pass the ID as a string or a number", string(raw))
}

// registerSkillsRPCs adds the experience-layer methods (MCP-11). They never
// switch a feature on behind its flag: with [optimization] experience_tracking
// or a project's auto_skills off, the underlying methods answer with an empty
// list or an error, which the tools explain.
func (s *Server) registerSkillsRPCs(reg map[string]handler) {
	sapp, _ := s.app.(SkillsApp)
	// withApp registers a handler that is unavailable without the app.
	withApp := func(method, what string, h func(app SkillsApp, p json.RawMessage) (any, error)) {
		reg[method] = func(p json.RawMessage) (any, error) {
			if sapp == nil {
				return nil, fmt.Errorf("%s needs the Claude Manager app (not available in this control server)", what)
			}
			return h(sapp, p)
		}
	}
	withProject := func(method, what string, call func(app SkillsApp, project string) (any, error)) {
		withApp(method, what, func(app SkillsApp, p json.RawMessage) (any, error) {
			var a projectArg
			if err := json.Unmarshal(p, &a); err != nil {
				return nil, err
			}
			return call(app, a.Project)
		})
	}

	withProject("GetSkillCandidates", "reading skill candidates", func(app SkillsApp, project string) (any, error) {
		return app.GetSkillCandidates(project)
	})
	withProject("GetSkills", "reading skills", func(app SkillsApp, project string) (any, error) {
		return app.GetSkills(project)
	})
	withProject("GetSkillUsage", "reading skill usage", func(app SkillsApp, project string) (any, error) {
		return app.GetSkillUsage(project)
	})
	withProject("GetSkillQuality", "reading skill quality", func(app SkillsApp, project string) (any, error) {
		return app.GetSkillQuality(project)
	})
	withProject("GetSkillAutopilot", "reading the skill autopilot", func(app SkillsApp, project string) (any, error) {
		return app.SkillAutopilotOf(project)
	})
	withProject("RunSkillAutopilot", "running the skill autopilot", func(app SkillsApp, project string) (any, error) {
		return app.RunSkillAutopilot(project)
	})

	withApp("DistillSkill", "distilling a skill", func(app SkillsApp, p json.RawMessage) (any, error) {
		var a struct {
			Project   string                     `json:"project"`
			Candidate *experience.SkillCandidate `json:"candidate"`
			Sig       []string                   `json:"sig"`
			Gates     []string                   `json:"gates"`
			Model     string                     `json:"model"`
			MinScore  float64                    `json:"min_score"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if a.Project == "" {
			return nil, fmt.Errorf("distill skill: project is required")
		}
		cand, err := pickCandidate(app, a.Project, a.Candidate, a.Sig)
		if err != nil {
			return nil, err
		}
		gates := a.Gates
		if len(gates) == 0 {
			gates = s.projectGates(a.Project)
		}
		return app.DistillSkill(a.Project, cand, gates, a.Model, a.MinScore)
	})

	withApp("ApproveSkill", "approving a skill", func(app SkillsApp, p json.RawMessage) (any, error) {
		var a struct {
			Project   string          `json:"project"`
			SkillID   json.RawMessage `json:"skill_id"`
			MD        string          `json:"md"`
			Overwrite bool            `json:"overwrite"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		id, err := parseSkillID(a.SkillID)
		if err != nil {
			return nil, err
		}
		md := a.MD
		if strings.TrimSpace(md) == "" {
			// No edited text: approve the draft as distilled.
			if a.Project == "" {
				return nil, fmt.Errorf("approve skill: pass md (the text to write) or project, so the stored draft can be used")
			}
			skills, err := app.GetSkills(a.Project)
			if err != nil {
				return nil, err
			}
			for _, sk := range skills {
				if sk.ID == id {
					md = sk.MD
				}
			}
			if strings.TrimSpace(md) == "" {
				return nil, fmt.Errorf("skill %d has no draft text in project %q: check the ID with get_skills", id, a.Project)
			}
		}
		path, err := app.ApproveSkill(id, md, a.Overwrite)
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": path}, nil
	})

	withApp("ArchiveSkill", "archiving a skill", func(app SkillsApp, p json.RawMessage) (any, error) {
		var a struct {
			SkillID json.RawMessage `json:"skill_id"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		id, err := parseSkillID(a.SkillID)
		if err != nil {
			return nil, err
		}
		return nil, app.ArchiveSkill(id)
	})

	withApp("RestoreSkill", "restoring a skill", func(app SkillsApp, p json.RawMessage) (any, error) {
		var a struct {
			SkillID   json.RawMessage `json:"skill_id"`
			Overwrite bool            `json:"overwrite"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		id, err := parseSkillID(a.SkillID)
		if err != nil {
			return nil, err
		}
		path, err := app.RestoreSkill(id, a.Overwrite)
		if err != nil {
			return nil, err
		}
		return map[string]any{"path": path}, nil
	})

	withApp("SetSkillAutopilot", "setting the skill autopilot", func(app SkillsApp, p json.RawMessage) (any, error) {
		var a struct {
			Project        string  `json:"project"`
			Enabled        bool    `json:"enabled"`
			DailyBudgetUSD float64 `json:"daily_budget_usd"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if a.Project == "" {
			return nil, fmt.Errorf("set skill autopilot: project is required")
		}
		return nil, app.SetSkillAutopilot(a.Project, a.Enabled, a.DailyBudgetUSD)
	})
}

// pickCandidate returns the candidate to distil: the one sent whole, or the
// mined candidate whose signature sequence equals sig.
func pickCandidate(app SkillsApp, project string, given *experience.SkillCandidate, sig []string) (experience.SkillCandidate, error) {
	if given != nil {
		return *given, nil
	}
	if len(sig) == 0 {
		return experience.SkillCandidate{}, fmt.Errorf("distill skill: pass sig (the signature sequence of a candidate of get_skill_candidates)")
	}
	all, err := app.GetSkillCandidates(project)
	if err != nil {
		return experience.SkillCandidate{}, err
	}
	for _, c := range all {
		if strings.Join(c.Sig, "\x00") == strings.Join(sig, "\x00") {
			return c, nil
		}
	}
	return experience.SkillCandidate{}, fmt.Errorf("no skill candidate with signature %v in project %q: candidates change as runs accumulate, list them again with get_skill_candidates", sig, project)
}

// projectGates are the project's own gate commands, nil when it has none.
func (s *Server) projectGates(project string) []string {
	cfg := s.app.GetConfig()
	if cfg == nil {
		return nil
	}
	for _, p := range cfg.Projects {
		if p.Name == project {
			return p.Gates
		}
	}
	return nil
}
