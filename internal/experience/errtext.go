package experience

import (
	"regexp"
	"strings"
)

// errLineMaxRunes caps the stored error line (LEARN-TASKS.md LN-35).
const errLineMaxRunes = 200

var exitCodeLine = regexp.MustCompile(`^(?:Error:\s*)?Exit code \d+\s*$`)

// errMarkers are the substrings (matched case-insensitively) that make a line
// "the" error line of a multi-line tool output.
var errMarkers = []string{"error", "fatal", "failed", "panic", "not found", "denied", "cannot", "isolated"}

// ErrorLine reduces a failed tool result to the one meaningful line of the
// error, at most 200 runes. Measured on 270 errors from CLI transcripts: 152
// of them begin with a bare "Exit code N" line, which says nothing about the
// cause, so it is skipped; the rest are <tool_use_error> wrappers, Python
// tracebacks and shell output. Rules, in order: strip <tool_use_error> tags;
// skip a leading "Exit code N"; for a traceback take the last non-empty line
// (the exception itself); otherwise the first of the first 20 non-empty lines
// that contains an error marker, else the first non-empty line; with nothing
// left, the original "Exit code N" line.
//
// Only this one line is ever persisted — tool output is not duplicated into
// SQLite (see the privacy note on SkillSample).
func ErrorLine(text string) string {
	text = strings.ReplaceAll(text, "<tool_use_error>", "")
	text = strings.ReplaceAll(text, "</tool_use_error>", "")

	var lines []string
	exitLine := ""
	for _, l := range strings.Split(text, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		if len(lines) == 0 && exitLine == "" && exitCodeLine.MatchString(l) {
			exitLine = l
			continue
		}
		lines = append(lines, l)
	}
	if len(lines) == 0 {
		return capRunes(exitLine)
	}
	if strings.HasPrefix(lines[0], "Traceback (most recent call last):") {
		return capRunes(lines[len(lines)-1])
	}
	head := lines
	if len(head) > 20 {
		head = head[:20]
	}
	for _, l := range head {
		low := strings.ToLower(l)
		for _, m := range errMarkers {
			if strings.Contains(low, m) {
				return capRunes(l)
			}
		}
	}
	return capRunes(lines[0])
}

func capRunes(s string) string {
	r := []rune(s)
	if len(r) > errLineMaxRunes {
		return string(r[:errLineMaxRunes])
	}
	return s
}
