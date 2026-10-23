package control

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"claude-manager/internal/store"
)

// FailuresApp is the part of *App the failure-rule RPCs need (LEARN-TASKS.md
// LN-38). The rule view type lives in package main, so the app hands it over
// as a plain value (FailureRulesOf).
type FailuresApp interface {
	FailureRulesOf(project string) (any, error)
	DraftFailureRules(project string, limit int) ([]store.FailureRule, error)
	AcceptFailureRule(id int64, ruleText string) error
	DismissFailureRule(id int64) error
}

// parseRuleID reads a rule id sent as a number or a numeric string.
func parseRuleID(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 {
		return 0, fmt.Errorf("rule_id is required: take the id of an item of get_failure_rules")
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err == nil {
		return n, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			return n, nil
		}
		return 0, fmt.Errorf("rule_id %q is not a number: take the id of an item of get_failure_rules, e.g. 4", s)
	}
	return 0, fmt.Errorf("rule_id has unsupported type %s: pass the id as a string or a number", string(raw))
}

// registerFailuresRPCs adds the failure-rule methods. Nothing here writes
// into a project except AcceptFailureRule, the explicit "accept".
func (s *Server) registerFailuresRPCs(reg map[string]handler) {
	fapp, _ := s.app.(FailuresApp)
	withApp := func(method, what string, h func(app FailuresApp, p json.RawMessage) (any, error)) {
		reg[method] = func(p json.RawMessage) (any, error) {
			if fapp == nil {
				return nil, fmt.Errorf("%s needs the Claude Manager app (not available in this control server)", what)
			}
			return h(fapp, p)
		}
	}

	withApp("GetFailureRules", "reading failure rules", func(app FailuresApp, p json.RawMessage) (any, error) {
		var a projectArg
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		return app.FailureRulesOf(a.Project)
	})
	withApp("DraftFailureRules", "drafting failure rules", func(app FailuresApp, p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
			Limit   int    `json:"limit"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if a.Project == "" {
			return nil, fmt.Errorf("draft failure rules: project is required")
		}
		return app.DraftFailureRules(a.Project, a.Limit)
	})
	withApp("AcceptFailureRule", "accepting a failure rule", func(app FailuresApp, p json.RawMessage) (any, error) {
		var a struct {
			RuleID json.RawMessage `json:"rule_id"`
			Text   string          `json:"text"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		id, err := parseRuleID(a.RuleID)
		if err != nil {
			return nil, err
		}
		return nil, app.AcceptFailureRule(id, a.Text)
	})
	withApp("DismissFailureRule", "dismissing a failure rule", func(app FailuresApp, p json.RawMessage) (any, error) {
		var a struct {
			RuleID json.RawMessage `json:"rule_id"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		id, err := parseRuleID(a.RuleID)
		if err != nil {
			return nil, err
		}
		return nil, app.DismissFailureRule(id)
	})
}
