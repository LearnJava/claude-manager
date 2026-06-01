package permission

import (
	"sync"
	"testing"

	"claude-manager/internal/config"
)

func TestPermissionRequestPattern(t *testing.T) {
	cases := []struct {
		name string
		req  PermissionRequest
		want string
	}{
		{"bash uses command", PermissionRequest{Tool: "Bash", Command: "npm test", FilePath: "ignore"}, "npm test"},
		{"shell uses command", PermissionRequest{Tool: "shell", Command: "ls -la"}, "ls -la"},
		{"edit uses file path", PermissionRequest{Tool: "Edit", FilePath: "src/app.go", Command: "ignore"}, "src/app.go"},
		{"write uses file path", PermissionRequest{Tool: "Write", FilePath: "main.go"}, "main.go"},
		{"read uses file path", PermissionRequest{Tool: "Read", FilePath: "README.md"}, "README.md"},
		{"unknown tool falls back to command", PermissionRequest{Tool: "McpTool", Command: "remote_call"}, "remote_call"},
		{"unknown tool then file path", PermissionRequest{Tool: "McpTool", FilePath: "/etc/hosts"}, "/etc/hosts"},
		{"unknown tool then description", PermissionRequest{Tool: "McpTool", Description: "desc"}, "desc"},
		{"empty tool", PermissionRequest{Tool: "", Command: "x"}, "x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.req.Pattern(); got != c.want {
				t.Fatalf("Pattern() = %q, want %q", got, c.want)
			}
		})
	}
}

func TestCLIDecision(t *testing.T) {
	cases := map[string]string{
		"allow":          "allow",
		"allow_session":  "allow",
		"allow_similar":  "allow",
		"allow_always":   "allow",
		"deny":           "deny",
		"deny_always":    "deny",
		"":               "",
		"weird":          "weird",
	}
	for in, want := range cases {
		t.Run(in, func(t *testing.T) {
			if got := CLIDecision(in); got != want {
				t.Fatalf("CLIDecision(%q) = %q, want %q", in, got, want)
			}
		})
	}
}

func TestMatchRuleToolWildcard(t *testing.T) {
	req := PermissionRequest{Tool: "Bash", Command: "anything"}
	rule := config.PermissionRule{Tool: "*", Pattern: "*", Decision: "allow"}
	if !MatchRule(rule, req) {
		t.Fatalf("expected wildcard rule to match")
	}
}

func TestMatchRuleEmptyToolMatchesAny(t *testing.T) {
	req := PermissionRequest{Tool: "Write", FilePath: "main.go"}
	rule := config.PermissionRule{Tool: "", Pattern: "*.go", Decision: "allow"}
	if !MatchRule(rule, req) {
		t.Fatalf("empty rule.Tool should match any tool")
	}
}

func TestMatchRuleCaseInsensitiveTool(t *testing.T) {
	req := PermissionRequest{Tool: "BASH", Command: "ls"}
	rule := config.PermissionRule{Tool: "bash", Pattern: "*", Decision: "allow"}
	if !MatchRule(rule, req) {
		t.Fatalf("tool comparison should be case-insensitive")
	}
}

func TestMatchRuleBashGlob(t *testing.T) {
	req := PermissionRequest{Tool: "Bash", Command: "cargo build --release"}
	cases := []struct {
		pattern string
		want    bool
	}{
		{"cargo build*", true},
		{"cargo *", true},
		{"npm *", false},
		{"cargo build --release", true},
		{"cargo build --debug", false},
	}
	for _, c := range cases {
		t.Run(c.pattern, func(t *testing.T) {
			rule := config.PermissionRule{Tool: "Bash", Pattern: c.pattern, Decision: "allow"}
			if got := MatchRule(rule, req); got != c.want {
				t.Fatalf("MatchRule(pattern=%q) = %v, want %v", c.pattern, got, c.want)
			}
		})
	}
}

func TestMatchRuleEditPathBasenameFallback(t *testing.T) {
	req := PermissionRequest{Tool: "Write", FilePath: "src/internal/main.go"}
	rule := config.PermissionRule{Tool: "Write", Pattern: "*.go", Decision: "allow"}
	if !MatchRule(rule, req) {
		t.Fatalf("expected basename fallback to match *.go for nested file")
	}
}

func TestMatchRuleEditPathBasenameFallbackNoMatch(t *testing.T) {
	req := PermissionRequest{Tool: "Write", FilePath: "src/main.py"}
	rule := config.PermissionRule{Tool: "Write", Pattern: "*.go", Decision: "allow"}
	if MatchRule(rule, req) {
		t.Fatalf("python file should not match *.go")
	}
}

func TestMatchRulePathPatternWithSeparator(t *testing.T) {
	req := PermissionRequest{Tool: "Edit", FilePath: "src/main.go"}
	rule := config.PermissionRule{Tool: "Edit", Pattern: "src/*.go", Decision: "allow"}
	if !MatchRule(rule, req) {
		t.Fatalf("pattern with separator should match exact path")
	}
}

func TestMatchRuleWrongTool(t *testing.T) {
	req := PermissionRequest{Tool: "Edit", FilePath: "main.go"}
	rule := config.PermissionRule{Tool: "Bash", Pattern: "*", Decision: "allow"}
	if MatchRule(rule, req) {
		t.Fatalf("rule for Bash should not match Edit request")
	}
}

func TestMatchRuleInvalidPattern(t *testing.T) {
	req := PermissionRequest{Tool: "Bash", Command: "ls"}
	rule := config.PermissionRule{Tool: "Bash", Pattern: "[bad", Decision: "allow"}
	if MatchRule(rule, req) {
		t.Fatalf("malformed pattern should not panic and should not match")
	}
}

func TestMatchRulesOrderAndShortCircuit(t *testing.T) {
	rules := []config.PermissionRule{
		{Tool: "Bash", Pattern: "git push*", Decision: "ask"},
		{Tool: "Bash", Pattern: "*", Decision: "allow"},
	}
	req := PermissionRequest{Tool: "Bash", Command: "git push origin main"}
	decision, matched, ok := MatchRules(rules, req)
	if !ok {
		t.Fatalf("expected a match")
	}
	if decision != "ask" {
		t.Fatalf("first matching rule should win — got %q, want %q", decision, "ask")
	}
	if matched.Pattern != "git push*" {
		t.Fatalf("matched rule pattern = %q, want %q", matched.Pattern, "git push*")
	}
}

func TestMatchRulesNoMatch(t *testing.T) {
	rules := []config.PermissionRule{
		{Tool: "Bash", Pattern: "npm *", Decision: "allow"},
	}
	req := PermissionRequest{Tool: "Bash", Command: "rm -rf /"}
	if _, _, ok := MatchRules(rules, req); ok {
		t.Fatalf("expected no match")
	}
}

func TestMatchRulesEmpty(t *testing.T) {
	req := PermissionRequest{Tool: "Bash", Command: "ls"}
	if _, _, ok := MatchRules(nil, req); ok {
		t.Fatalf("nil rules should yield no match")
	}
}

func TestRuntimeRuleSetAddAndMatch(t *testing.T) {
	s := NewRuntimeRuleSet()
	if s.Len() != 0 {
		t.Fatalf("new set should be empty")
	}
	s.Add("Bash", "cargo *", "allow")
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1", s.Len())
	}
	req := PermissionRequest{Tool: "Bash", Command: "cargo test"}
	dec, ok := s.Match(req)
	if !ok || dec != "allow" {
		t.Fatalf("Match = (%q,%v), want (allow,true)", dec, ok)
	}
	if !s.Allows(req) {
		t.Fatalf("Allows should be true")
	}
	if s.Denies(req) {
		t.Fatalf("Denies should be false")
	}
}

func TestRuntimeRuleSetDenies(t *testing.T) {
	s := NewRuntimeRuleSet()
	s.Add("Bash", "rm *", "deny")
	req := PermissionRequest{Tool: "Bash", Command: "rm -rf /"}
	if !s.Denies(req) {
		t.Fatalf("expected Denies=true")
	}
	if s.Allows(req) {
		t.Fatalf("expected Allows=false")
	}
}

func TestRuntimeRuleSetNoMatch(t *testing.T) {
	s := NewRuntimeRuleSet()
	s.Add("Bash", "cargo *", "allow")
	req := PermissionRequest{Tool: "Bash", Command: "ls"}
	if _, ok := s.Match(req); ok {
		t.Fatalf("expected no match")
	}
	if s.Allows(req) {
		t.Fatalf("Allows should be false")
	}
}

func TestRuntimeRuleSetDeduplicates(t *testing.T) {
	s := NewRuntimeRuleSet()
	s.Add("Bash", "ls", "allow")
	s.Add("Bash", "ls", "allow")
	s.Add("Bash", "ls", "allow")
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1 (duplicates should be ignored)", s.Len())
	}
	// Different decision is a different rule.
	s.Add("Bash", "ls", "deny")
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2 (different decision is a distinct rule)", s.Len())
	}
}

func TestRuntimeRuleSetAllAndClear(t *testing.T) {
	s := NewRuntimeRuleSet()
	s.Add("Bash", "ls", "allow")
	s.Add("Edit", "*.go", "allow")
	all := s.All()
	if len(all) != 2 {
		t.Fatalf("All() length = %d, want 2", len(all))
	}
	// Mutating returned slice must not affect the set.
	all[0].Pattern = "mutated"
	if got := s.All()[0].Pattern; got == "mutated" {
		t.Fatalf("All() must return a copy")
	}
	s.Clear()
	if s.Len() != 0 {
		t.Fatalf("Clear should empty the set")
	}
}

func TestRuntimeRuleSetConcurrent(t *testing.T) {
	s := NewRuntimeRuleSet()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			s.Add("Bash", "p", "allow")
			_ = s.Len()
		}(i)
		go func(i int) {
			defer wg.Done()
			_, _ = s.Match(PermissionRequest{Tool: "Bash", Command: "p"})
			_ = s.All()
		}(i)
	}
	wg.Wait()
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1 after dedup", s.Len())
	}
}
