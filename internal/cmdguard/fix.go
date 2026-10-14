// Package cmdguard repairs shell commands that are certain to fail for a
// mechanical reason before the Bash tool runs them. The manager installs it
// into every Claude session as a PreToolUse hook (`claude-manager hook
// cmdguard`, see hook.go) via --settings, limited by the CLI to Bash calls
// with a `cd .…` subcommand (HookCondition) so it costs no process start on
// any other call.
//
// Only rewrites, never refusals: a refused call costs the model the same
// round trip as the failure it prevents, so a deny hook saves nothing. Each
// rule fires only where the original command cannot work, which the replay
// of 95k Bash calls from saved run logs confirmed (docs/design-decisions.md
// "Command Guard"); anything uncertain is left alone.
package cmdguard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Rule names reported in the hook's reason and the guard log.
const (
	RuleQuotedTrailingBackslash = "quoted-windows-path-trailing-backslash"
	RuleUnquotedWindowsPath     = "unquoted-windows-path"
	RuleCdFromProjectRoot       = "cd-relative-to-project-root"
)

// FixCommand returns cmd with every applicable rule applied and the names of
// the rules that changed it (nil when cmd is returned unchanged). cwd is the
// Bash tool's current directory, projectDir the session's project root;
// either may be empty, which disables the rule that needs it.
func FixCommand(cmd, cwd, projectDir string) (string, []string) {
	var rules []string
	if out, ok := fixWindowsPaths(cmd); ok {
		if out.quoted {
			rules = append(rules, RuleQuotedTrailingBackslash)
		}
		if out.unquoted {
			rules = append(rules, RuleUnquotedWindowsPath)
		}
		cmd = out.cmd
	}
	if out, ok := fixLeadingCd(cmd, cwd, projectDir); ok {
		rules = append(rules, RuleCdFromProjectRoot)
		cmd = out
	}
	return cmd, rules
}

type pathFix struct {
	cmd              string
	quoted, unquoted bool
}

// fixWindowsPaths rewrites the two Windows-path forms bash always mangles:
//
//   - "D:\dir\"  — the final backslash escapes the closing quote, so the
//     string runs on to the next quote or to EOF ("unexpected EOF while
//     looking for matching").
//   - D:\dir\file unquoted — bash drops every backslash (D:dirfile).
//
// Both become forward-slash paths, which Git Bash and Windows programs
// accept. A lexer tracks quotes, comments and heredoc bodies so only real
// shell words are touched.
func fixWindowsPaths(s string) (pathFix, bool) {
	var b strings.Builder
	res := pathFix{}
	var heredocs []heredoc
	i := 0
	atWordStart := true
	for i < len(s) {
		c := s[i]
		switch {
		case c == '\n':
			b.WriteByte(c)
			i++
			atWordStart = true
			if len(heredocs) > 0 {
				end := skipHeredocBodies(s, i, heredocs)
				b.WriteString(s[i:end])
				i = end
				heredocs = nil
			}
		case c == '\\':
			if i+1 < len(s) {
				b.WriteString(s[i : i+2])
				i += 2
			} else {
				b.WriteByte(c)
				i++
			}
			atWordStart = false
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				b.WriteString(s[i:])
				i = len(s)
			} else {
				b.WriteString(s[i : i+end+2])
				i += end + 2
			}
			atWordStart = false
		case c == '"':
			if fixed, n, ok := quotedWindowsPath(s[i:]); ok {
				b.WriteString(fixed)
				i += n
				res.quoted = true
			} else {
				n := doubleQuotedLen(s[i:])
				b.WriteString(s[i : i+n])
				i += n
			}
			atWordStart = false
		case c == '#' && atWordStart:
			end := strings.IndexByte(s[i:], '\n')
			if end < 0 {
				end = len(s) - i
			}
			b.WriteString(s[i : i+end])
			i += end
		case c == '<' && strings.HasPrefix(s[i:], "<<") && !strings.HasPrefix(s[i:], "<<<"):
			if h, n, ok := parseHeredoc(s[i:]); ok {
				heredocs = append(heredocs, h)
				b.WriteString(s[i : i+n])
				i += n
			} else {
				b.WriteString("<<")
				i += 2
			}
			atWordStart = false
		case atWordStart && isDriveStart(s[i:]):
			word := unquotedWord(s[i:])
			// A trailing backslash before a space is an escaped space
			// (D:\Program\ Files): the word is not a plain path, leave it.
			if strings.HasSuffix(word, `\`) && i+len(word) < len(s) && s[i+len(word)] == ' ' {
				b.WriteString(word)
			} else {
				b.WriteString(slashRuns(word))
				res.unquoted = true
			}
			i += len(word)
			atWordStart = false
		default:
			b.WriteByte(c)
			i++
			atWordStart = strings.IndexByte(" \t;&|()=", c) >= 0
		}
	}
	res.cmd = b.String()
	return res, res.quoted || res.unquoted
}

var driveStart = regexp.MustCompile(`^[A-Za-z]:\\`)

func isDriveStart(s string) bool { return driveStart.MatchString(s) }

// unquotedWord returns the leading run of s that bash reads as one unquoted
// word (no quotes, expansions or operators inside).
func unquotedWord(s string) string {
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(" \t\n\"'|;&<>()$`", s[i]) >= 0 {
			return s[:i]
		}
	}
	return s
}

var backslashRun = regexp.MustCompile(`\\+`)

func slashRuns(s string) string { return backslashRun.ReplaceAllString(s, "/") }

// quotedWindowsPath handles a double-quoted string starting at s[0] whose
// content begins with a drive path. It reports a fix only when the string is
// broken: the first quote that ends a shell word is preceded by an odd number
// of backslashes, i.e. bash reads it as an escaped quote. n is the length of
// s consumed, up to and including that quote.
func quotedWindowsPath(s string) (fixed string, n int, ok bool) {
	if !isDriveStart(s[1:]) {
		return "", 0, false
	}
	for k := 1; k < len(s); k++ {
		if s[k] == '\n' || s[k] == '$' || s[k] == '`' {
			return "", 0, false
		}
		if s[k] != '"' {
			continue
		}
		if k+1 < len(s) && strings.IndexByte(" \t\n|;&)><", s[k+1]) < 0 {
			continue
		}
		bs := 0
		for j := k - 1; j > 0 && s[j] == '\\'; j-- {
			bs++
		}
		if bs%2 == 0 {
			return "", 0, false // closes normally (no or escaped backslash)
		}
		return `"` + slashRuns(s[1:k]) + `"`, k + 1, true
	}
	return "", 0, false
}

// doubleQuotedLen returns the length of the double-quoted string at s[0],
// honouring backslash escapes; an unterminated string runs to the end.
func doubleQuotedLen(s string) int {
	for k := 1; k < len(s); k++ {
		switch s[k] {
		case '\\':
			k++
		case '"':
			return k + 1
		}
	}
	return len(s)
}

type heredoc struct {
	delim     string
	stripTabs bool
}

var heredocStart = regexp.MustCompile(`^<<(-?)[ \t]*(?:'([^'\n]+)'|"([^"\n]+)"|\\?([A-Za-z_][A-Za-z0-9_]*))`)

func parseHeredoc(s string) (heredoc, int, bool) {
	m := heredocStart.FindStringSubmatch(s)
	if m == nil {
		return heredoc{}, 0, false
	}
	return heredoc{delim: m[2] + m[3] + m[4], stripTabs: m[1] == "-"}, len(m[0]), true
}

// skipHeredocBodies returns the index just past the bodies of hs, which
// start at s[i], one after another in order.
func skipHeredocBodies(s string, i int, hs []heredoc) int {
	for _, h := range hs {
		for i < len(s) {
			end := strings.IndexByte(s[i:], '\n')
			line := s[i:]
			next := len(s)
			if end >= 0 {
				line = s[i : i+end]
				next = i + end + 1
			}
			i = next
			if h.stripTabs {
				line = strings.TrimLeft(line, "\t")
			}
			if strings.TrimRight(line, "\r") == h.delim {
				break
			}
		}
	}
	return i
}

var leadingCd = regexp.MustCompile(`^(\s*)cd[ \t]+(?:"([^"$` + "`" + `\\\n]+)"|'([^'\n]+)'|([^\s;&|"'$` + "`" + `\\~-][^\s;&|"'$` + "`" + `\\]*))[ \t]*(&&|;|\n|$)`)

// fixLeadingCd rewrites a command that starts with `cd <relative dir>` when
// the directory does not exist under cwd but does under the project root —
// the model re-entering a worktree it already is in, or assuming the root as
// cwd. The cd becomes absolute, which is what the model's own retry does.
// Only the first segment is checked: a later cd may target a directory an
// earlier segment creates.
func fixLeadingCd(cmd, cwd, projectDir string) (string, bool) {
	if cwd == "" || projectDir == "" {
		return "", false
	}
	m := leadingCd.FindStringSubmatchIndex(cmd)
	if m == nil {
		return "", false
	}
	var rel string
	for g := 2; g <= 4; g++ {
		if m[2*g] >= 0 {
			rel = cmd[m[2*g]:m[2*g+1]]
		}
	}
	if rel == "" || filepath.IsAbs(rel) || strings.HasPrefix(rel, "/") || isDriveStart(rel+`\`) {
		return "", false
	}
	if isDir(filepath.Join(cwd, rel)) {
		return "", false
	}
	target := filepath.Join(projectDir, rel)
	if !isDir(target) {
		return "", false
	}
	// Group 1 is leading whitespace; group 5 the separator after the dir.
	return cmd[:m[3]] + `cd "` + filepath.ToSlash(target) + `" ` + cmd[m[10]:], true
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}
