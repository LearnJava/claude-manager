package permission

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"claude-manager/internal/config"
)

// PermissionRequest is a permission request emitted by Claude CLI.
// Mirrors the fields described in PLAN.md section 16.3.
type PermissionRequest struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id"`
	Timestamp    time.Time `json:"timestamp"`
	Tool         string    `json:"tool"`         // "Bash", "Edit", "Write", "McpTool"
	Description  string    `json:"description"`  // What Claude wants to do
	Command      string    `json:"command"`      // For Bash: full command
	FilePath     string    `json:"file_path"`    // For Edit/Write: file path
	RiskLevel    string    `json:"risk_level"`   // "low", "medium", "high"
	WaitingSince time.Time `json:"waiting_since"`
}

// Pattern returns the relevant string to match against a PermissionRule pattern.
// For Bash → Command, for Edit/Write → FilePath, otherwise → Description.
func (r PermissionRequest) Pattern() string {
	switch strings.ToLower(r.Tool) {
	case "bash", "shell":
		return r.Command
	case "edit", "write", "read":
		return r.FilePath
	default:
		if r.Command != "" {
			return r.Command
		}
		if r.FilePath != "" {
			return r.FilePath
		}
		return r.Description
	}
}

// PermissionResponse is the user/auto decision delivered back through the manager.
// Decision values include: "allow", "deny", "allow_session", "allow_similar",
// "allow_always", "deny_always".
type PermissionResponse struct {
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"`
}

// CLIDecision translates a high-level decision into the literal "allow"/"deny"
// string expected by the Claude CLI on stdin.
func CLIDecision(decision string) string {
	switch decision {
	case "allow", "allow_session", "allow_similar", "allow_always":
		return "allow"
	case "deny", "deny_always":
		return "deny"
	default:
		return decision
	}
}

// MatchRule reports whether rule applies to req. The rule's tool ("*" wildcard
// matches every tool) and pattern (filepath.Match glob; "" or "*" matches any)
// are evaluated against the request's tool and Pattern() value.
func MatchRule(rule config.PermissionRule, req PermissionRequest) bool {
	if !toolMatches(rule.Tool, req.Tool) {
		return false
	}
	return patternMatches(rule.Pattern, req.Pattern())
}

// toolMatches reports whether ruleTool matches reqTool. Empty rule tool and "*"
// match any tool. Comparison is case-insensitive.
func toolMatches(ruleTool, reqTool string) bool {
	ruleTool = strings.TrimSpace(ruleTool)
	if ruleTool == "" || ruleTool == "*" {
		return true
	}
	return strings.EqualFold(ruleTool, reqTool)
}

// patternMatches reports whether the glob pattern matches target. Empty/"*"
// patterns match anything. For path-like targets, if the pattern contains no
// path separators we also try matching the basename so that "*.go" matches
// "src/main.go".
func patternMatches(pattern, target string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if ok, err := filepath.Match(pattern, target); err == nil && ok {
		return true
	}
	if !strings.ContainsAny(pattern, "/\\") &&
		strings.ContainsAny(target, "/\\") {
		if ok, err := filepath.Match(pattern, filepath.Base(target)); err == nil && ok {
			return true
		}
	}
	return false
}

// MatchRules walks rules in order and returns the decision of the first match
// (along with the matched rule). If none match, ok is false.
func MatchRules(rules []config.PermissionRule, req PermissionRequest) (decision string, matched config.PermissionRule, ok bool) {
	for _, rule := range rules {
		if MatchRule(rule, req) {
			return rule.Decision, rule, true
		}
	}
	return "", config.PermissionRule{}, false
}

// RuntimeRuleSet holds session-scoped or "allow similar" rules that live only
// in memory. Safe for concurrent use.
type RuntimeRuleSet struct {
	mu    sync.RWMutex
	rules []config.PermissionRule
}

// NewRuntimeRuleSet returns an empty rule set.
func NewRuntimeRuleSet() *RuntimeRuleSet {
	return &RuntimeRuleSet{}
}

// Add registers a new rule. Duplicate (tool, pattern, decision) triples are
// silently de-duplicated.
func (s *RuntimeRuleSet) Add(tool, pattern, decision string) {
	rule := config.PermissionRule{Tool: tool, Pattern: pattern, Decision: decision}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, existing := range s.rules {
		if existing.Tool == rule.Tool && existing.Pattern == rule.Pattern && existing.Decision == rule.Decision {
			return
		}
	}
	s.rules = append(s.rules, rule)
}

// Match returns the decision of the first matching runtime rule.
func (s *RuntimeRuleSet) Match(req PermissionRequest) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	decision, _, ok := MatchRules(s.rules, req)
	return decision, ok
}

// Allows is a convenience: true iff a runtime rule matches with decision "allow".
func (s *RuntimeRuleSet) Allows(req PermissionRequest) bool {
	decision, ok := s.Match(req)
	return ok && decision == "allow"
}

// Denies is a convenience: true iff a runtime rule matches with decision "deny".
func (s *RuntimeRuleSet) Denies(req PermissionRequest) bool {
	decision, ok := s.Match(req)
	return ok && decision == "deny"
}

// All returns a copy of all currently registered runtime rules.
func (s *RuntimeRuleSet) All() []config.PermissionRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]config.PermissionRule, len(s.rules))
	copy(out, s.rules)
	return out
}

// Len returns the number of runtime rules.
func (s *RuntimeRuleSet) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.rules)
}

// Clear removes all runtime rules.
func (s *RuntimeRuleSet) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = nil
}
