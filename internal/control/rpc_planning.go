package control

import (
	"encoding/json"
	"fmt"

	"claude-manager/internal/analysis"
)

// PlanningApp is the part of *App the roadmap RPCs need beyond the manager:
// approving a roadmap also edits the config (the P1 session), and the roadmap
// views resolve a project's folder. A control server without it
// (playwright-server, tests) reports these methods as unavailable.
type PlanningApp interface {
	ApproveRoadmap(planID int64, overwrite bool) (*analysis.TaskPlan, error)
	GetSessionRoadmap(project, sessionName string) (*analysis.RoadmapView, error)
	GetRoadmapTaskDetail(project, relPath string) (string, error)
	GetRoadmapRowDetail(project, roadmapFile string, line int) (string, error)
}

// registerPlanningRPCs adds the roadmap methods (MCP-10); the preflight and
// mixed-programming ones live in buildRegistry next to the older plan RPCs.
func (s *Server) registerPlanningRPCs(reg map[string]handler) {
	m := s.manager
	papp, _ := s.app.(PlanningApp)
	needApp := func(what string) error {
		return fmt.Errorf("%s needs the Claude Manager app (not available in this control server)", what)
	}

	reg["GenerateRoadmap"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
			Idea    string `json:"idea"`
			Model   string `json:"model"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if a.Project == "" || a.Idea == "" {
			return nil, fmt.Errorf("generate roadmap: project and idea are both required")
		}
		return m.GenerateRoadmap(a.Project, a.Idea, a.Model)
	}

	reg["GetLatestDraftRoadmap"] = func(p json.RawMessage) (any, error) {
		var a projectArg
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		return m.GetLatestDraftRoadmap(a.Project)
	}

	reg["ApproveRoadmap"] = func(p json.RawMessage) (any, error) {
		var a struct {
			PlanID    json.RawMessage `json:"plan_id"`
			Overwrite bool            `json:"overwrite"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		id, err := parsePlanID(a.PlanID)
		if err != nil {
			return nil, err
		}
		if papp == nil {
			return nil, needApp("approving a roadmap")
		}
		return papp.ApproveRoadmap(id, a.Overwrite)
	}

	reg["GetSessionRoadmap"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
			Session string `json:"session"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if papp == nil {
			return nil, needApp("reading a roadmap")
		}
		return papp.GetSessionRoadmap(a.Project, a.Session)
	}

	reg["GetRoadmapTaskDetail"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
			RelPath string `json:"rel_path"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if papp == nil {
			return nil, needApp("reading a roadmap task")
		}
		return papp.GetRoadmapTaskDetail(a.Project, a.RelPath)
	}

	reg["GetRoadmapRowDetail"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project     string `json:"project"`
			RoadmapFile string `json:"roadmap_file"`
			Line        int    `json:"line"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if papp == nil {
			return nil, needApp("reading a roadmap row")
		}
		return papp.GetRoadmapRowDetail(a.Project, a.RoadmapFile, a.Line)
	}
}
