package experience

import (
	"regexp"
	"strings"
	"time"

	"claude-manager/internal/store"
)

// MinPermissionCount is the default minimum number of human-decided
// occurrences before a (tool, pattern) is even considered for a suggestion —
// a single ask is not a pattern (LEARN-TASKS.md LN-04).
const MinPermissionCount = 2

// PermissionCandidate is one suggested permission rule derived from repeated
// permission_events rows: a (tool, pattern) that keeps asking and has been
// consistently allowed. Safe reports whether ClassifyPermission cleared it —
// callers put Safe candidates in an "Add rule" list and the rest under
// "needs manual review", never auto-apply either.
type PermissionCandidate struct {
	Tool       string
	Pattern    string
	Count      int
	AllowCount int
	DenyCount  int
	Safe       bool
	FirstSeen  time.Time
	LastSeen   time.Time
}

// CandidateSet is the split output of Candidates — the shape App.
// GetPermissionCandidates hands to the frontend's "Permissions" tab.
type CandidateSet struct {
	Safe        []PermissionCandidate
	NeedsReview []PermissionCandidate
}

// Candidates turns aggregated permission_events stats into suggestions,
// split into safe (cleared by ClassifyPermission) and needsReview
// (everything else that still meets the frequency/consistency bar). A stat
// with too few occurrences, or with as many/more denials than allows, is
// dropped entirely — this feature only ever proposes *allow* rules, and a
// pattern the user has been denying is not a candidate for one no matter how
// often it is asked.
func Candidates(stats []store.PermissionEventStat, minCount int) (safe, needsReview []PermissionCandidate) {
	for _, st := range stats {
		if st.Count < minCount || st.AllowCount == 0 || st.AllowCount < st.DenyCount {
			continue
		}
		cand := PermissionCandidate{
			Tool:       st.Tool,
			Pattern:    st.Pattern,
			Count:      st.Count,
			AllowCount: st.AllowCount,
			DenyCount:  st.DenyCount,
			FirstSeen:  st.FirstSeen,
			LastSeen:   st.LastSeen,
			Safe:       ClassifyPermission(st.Tool, st.Pattern),
		}
		if cand.Safe {
			safe = append(safe, cand)
		} else {
			needsReview = append(needsReview, cand)
		}
	}
	return safe, needsReview
}

// ClassifyPermission is a hard whitelist, never a heuristic score: it decides
// whether (tool, pattern) is safe to suggest for auto-allow. Read/Grep/Glob
// are always safe — none of them can mutate anything, whatever the pattern.
// Bash is safe only for an explicit whitelist of read-only commands, checked
// by classifyBashCommand. Every other tool (Edit, Write, McpTool, ...)
// requires a human decision — this app has no whitelist for anything that
// writes.
func ClassifyPermission(tool, pattern string) bool {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "read", "grep", "glob":
		return true
	case "bash", "shell":
		return classifyBashCommand(pattern)
	default:
		return false
	}
}

// dangerousBashPatterns always wins over the whitelist below: a command
// containing any of these must never be suggested for auto-allow, no matter
// how often it ran or how consistently it was approved (LEARN-TASKS.md LN-04
// "жёсткий, whitelist-подход"). Matched with word boundaries so "term" or
// "confirm" don't trip the "rm" check, and the whole command is tested before
// it is split into pipeline segments so "git log && rm -rf /" is rejected
// even though its first segment alone is whitelisted.
var dangerousBashPatterns = []*regexp.Regexp{
	regexp.MustCompile(`\brm\b`),
	regexp.MustCompile(`\bmv\b`),
	regexp.MustCompile(`>>?`), // > and >>
	regexp.MustCompile(`\bcurl\b`),
	regexp.MustCompile(`\bwget\b`),
	regexp.MustCompile(`\bssh\b`),
	regexp.MustCompile(`\bsudo\b`),
	regexp.MustCompile(`\bgit\s+push\b`),
	regexp.MustCompile(`\bgit\s+reset\b`),
	regexp.MustCompile(`\bgit\s+checkout\s+--`),
	regexp.MustCompile(`--force\b`),
}

// segmentSplit breaks a command line on shell chaining operators (&&, ||, ;,
// |) so each stage of a pipeline/chain is checked against the whitelist on
// its own — "ls && cat file" is safe only because both "ls" and "cat file"
// are, not because the string contains no blacklisted word.
var segmentSplit = regexp.MustCompile(`&&|\|\||[;|]`)

// safePrefixes are the read-only command shapes LEARN-TASKS.md LN-04 lists
// verbatim. Compound entries (["git","status"]) are matched before any bare
// "git"/"go"/... prefix would be — there is no bare entry for those utilities
// precisely because most of their subcommands (push, reset, install) are not
// safe.
var safePrefixes = [][]string{
	{"ls"}, {"cat"}, {"head"}, {"tail"}, {"sed", "-n"},
	{"grep"}, {"rg"}, {"find"},
	{"git", "status"}, {"git", "log"}, {"git", "diff"}, {"git", "show"},
	{"go", "build"}, {"go", "test"}, {"go", "vet"},
	{"cargo", "check"}, {"cargo", "build"}, {"cargo", "test"}, {"cargo", "clippy"},
	{"npm", "test"}, {"npm", "run", "build"},
}

// classifyBashCommand reports whether cmd is safe to suggest for auto-allow:
// it must contain nothing from dangerousBashPatterns, and every
// chained/piped segment must match one of safePrefixes. An empty command, or
// one that matches no whitelist entry at all, is unsafe by default — the
// classifier never guesses.
func classifyBashCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	for _, re := range dangerousBashPatterns {
		if re.MatchString(cmd) {
			return false
		}
	}
	segments := segmentSplit.Split(cmd, -1)
	if len(segments) == 0 {
		return false
	}
	for _, seg := range segments {
		if !matchesSafePrefix(strings.TrimSpace(seg)) {
			return false
		}
	}
	return true
}

// matchesSafePrefix reports whether cmd's leading tokens match one of
// safePrefixes exactly.
func matchesSafePrefix(cmd string) bool {
	tokens := strings.Fields(cmd)
	for _, prefix := range safePrefixes {
		if len(tokens) < len(prefix) {
			continue
		}
		match := true
		for i, p := range prefix {
			if tokens[i] != p {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
