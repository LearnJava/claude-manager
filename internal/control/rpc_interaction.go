package control

import (
	"encoding/json"
	"fmt"

	"claude-manager/internal/experience"
	"claude-manager/internal/permission"
	"claude-manager/internal/session"
)

// InteractionApp is the part of *App the interaction RPCs need beyond the
// manager: the experience store (permission candidates) and the config
// round-trip that appends a permission rule. A control server without it
// (playwright-server, tests) reports these methods as unavailable.
type InteractionApp interface {
	GetPermissionCandidates(project string, days int) (*experience.CandidateSet, error)
	AddPermissionRule(project, sessionName, tool, pattern, decision string) error
}

// pendingFor returns the permission requests and questions waiting on a human,
// limited to one session when id is not empty. Both lists are non-nil.
func (s *Server) pendingFor(id string) ([]permission.PermissionRequest, []session.QuestionInfo) {
	perms := []permission.PermissionRequest{}
	for _, r := range s.manager.GetPendingPermissions() {
		if id == "" || r.SessionID == id {
			perms = append(perms, r)
		}
	}
	questions := []session.QuestionInfo{}
	for _, q := range s.manager.GetPendingQuestions() {
		if id == "" || q.SessionID == id {
			questions = append(questions, q)
		}
	}
	return perms, questions
}

// registerInteractionRPCs adds the questions / permissions / plans methods of
// the interaction area (MCP-07).
func (s *Server) registerInteractionRPCs(reg map[string]handler) {
	m := s.manager
	iapp, _ := s.app.(InteractionApp)
	needApp := func(what string) error {
		return fmt.Errorf("%s needs the Claude Manager app (not available in this control server)", what)
	}

	reg["GetPendingQuestions"] = func(p json.RawMessage) (any, error) {
		var a struct{ ID string }
		if len(p) > 0 {
			if err := json.Unmarshal(p, &a); err != nil {
				return nil, err
			}
		}
		_, qs := s.pendingFor(a.ID)
		return qs, nil
	}

	reg["AnswerQuestion"] = func(p json.RawMessage) (any, error) {
		var a struct {
			ID         string `json:"id"`
			QuestionID string `json:"question_id"`
			Answer     string `json:"answer"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		return nil, m.AnswerQuestion(a.ID, a.QuestionID, a.Answer)
	}

	// GetPending is the "what waits for a human" summary across all kinds.
	reg["GetPending"] = func(p json.RawMessage) (any, error) {
		var a struct{ ID string }
		if len(p) > 0 {
			if err := json.Unmarshal(p, &a); err != nil {
				return nil, err
			}
		}
		perms, qs := s.pendingFor(a.ID)
		return map[string]any{"permissions": perms, "questions": qs}, nil
	}

	reg["GetPermissionCandidates"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
			Days    int    `json:"days"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if iapp == nil {
			return nil, needApp("permission candidates")
		}
		return iapp.GetPermissionCandidates(a.Project, a.Days)
	}

	reg["AddPermissionRule"] = func(p json.RawMessage) (any, error) {
		var a struct{ Project, Session, Tool, Pattern, Decision string }
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if a.Decision != "allow" && a.Decision != "deny" {
			return nil, fmt.Errorf("decision %q is not valid: use allow or deny", a.Decision)
		}
		if a.Tool == "" {
			return nil, fmt.Errorf("tool is required, e.g. Bash")
		}
		if iapp == nil {
			return nil, needApp("adding a permission rule")
		}
		return nil, iapp.AddPermissionRule(a.Project, a.Session, a.Tool, a.Pattern, a.Decision)
	}
}
