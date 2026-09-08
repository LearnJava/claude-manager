package experience

import "strings"

// interpreters are commands whose first positional argument is a script file,
// not a subcommand — the script's base filename is the informative part of
// the signature (rule 5, LEARN-TASKS.md LN-02).
var interpreters = map[string]bool{
	"bash": true, "sh": true, "python": true, "node": true,
}

// multiCommandUtils are utilities whose meaning lives in the subcommand, not
// the utility name — masking `git status` and `git push` down to the same
// `git <ARG>` was the #1 signature in the first measurement run (rule 1).
var multiCommandUtils = map[string]bool{
	"git": true, "go": true, "cargo": true, "npm": true, "docker": true,
	"gh": true, "kubectl": true, "pip": true, "python": true, "wails": true,
}

// navSkip commands only change shell state and carry no signal of their own;
// a chained `cd $dir && git status` must signature as the git call, not as
// `cd <ARG>` — the single most common raw signature before this rule (20 388
// calls in the measured corpus) — see rule 2.
var navSkip = map[string]bool{
	"cd": true, "export": true, "pwd": true, "source": true, "set": true,
}

const maxBashTokens = 6

// Signature reduces one tool call to a normalized form for aggregation
// ("sig") plus the original value verbatim ("arg"). inputText is
// Step.InputText — the already-abbreviated value (Bash command / file path /
// search pattern), not the raw tool_use JSON: the markdown backend (LN-17)
// never has raw JSON to begin with. See LEARN-TASKS.md LN-02 for the rule
// table this implements.
func Signature(tool, inputText, projectPath string) (sig, arg string) {
	arg = inputText
	switch tool {
	case "Bash":
		sig = bashSignature(inputText)
	case "Read", "Edit", "Write":
		sig = tool + ":" + pathSignature(relativizePath(inputText, projectPath))
	case "Grep", "Glob":
		sig = tool + ":" + truncateRunes(inputText, 40)
	default:
		sig = tool + ":" + truncateRunes(firstWord(inputText), 30)
	}
	return sig, arg
}

// bashSignature implements the five Bash-specific rules: strip the
// navigational prefix, keep the multi-command subcommand or the interpreter's
// script basename literal, mask every other positional argument, keep flags
// (truncating `--flag=value` to `--flag`), and cap the result at 6 tokens.
func bashSignature(cmd string) string {
	seg := pickSegment(splitSegments(shellTokenize(cmd)))
	if len(seg) == 0 {
		return "Bash:"
	}
	for i, t := range seg {
		seg[i] = stripQuotes(t)
	}

	utility := seg[0]
	out := []string{utility}
	rest := seg[1:]
	idx := 0

	switch {
	case interpreters[utility]:
		// Flags before the script (rare, but `python -O script.py` exists)
		// are kept; the first non-flag token is the script itself.
		for idx < len(rest) {
			if !strings.HasPrefix(rest[idx], "-") {
				out = append(out, baseName(rest[idx]))
				idx++
				break
			}
			out = append(out, maskFlag(rest[idx]))
			idx++
		}
	case multiCommandUtils[utility]:
		for idx < len(rest) {
			if !strings.HasPrefix(rest[idx], "-") {
				out = append(out, rest[idx]) // subcommand, kept verbatim
				idx++
				break
			}
			out = append(out, maskFlag(rest[idx]))
			idx++
		}
	}

	for ; idx < len(rest); idx++ {
		t := rest[idx]
		if strings.HasPrefix(t, "-") {
			out = append(out, maskFlag(t))
		} else {
			out = append(out, "<ARG>")
		}
	}

	if len(out) > maxBashTokens {
		out = out[:maxBashTokens]
	}
	return "Bash:" + strings.Join(out, " ")
}

// maskFlag keeps a flag verbatim, except a `--flag=value` form is truncated
// to `--flag` — the value is the literal, the flag name is the action.
func maskFlag(t string) string {
	if i := strings.Index(t, "="); i >= 0 {
		return t[:i]
	}
	return t
}

// pathSignature turns a project-relative (or, if outside the project,
// as-is) path into "<first two dir segments>/*<ext>" — e.g.
// "internal/experience/signature.go" -> "internal/experience/*.go". A path
// with fewer than two directory segments contributes whatever it has; a bare
// top-level filename contributes none.
func pathSignature(relPath string) string {
	relPath = strings.ReplaceAll(relPath, "\\", "/")
	parts := strings.Split(relPath, "/")

	var dirParts []string
	if len(parts) > 1 {
		dirParts = parts[:len(parts)-1]
	}
	if len(dirParts) > 2 {
		dirParts = dirParts[:2]
	}

	ext := ""
	if i := strings.LastIndex(parts[len(parts)-1], "."); i >= 0 {
		ext = parts[len(parts)-1][i:]
	}

	var b strings.Builder
	for _, p := range dirParts {
		b.WriteString(p)
		b.WriteString("/")
	}
	b.WriteString("*")
	b.WriteString(ext)
	return b.String()
}

// relativizePath makes path relative to projectPath when it is actually
// inside it; anything else (a different drive, a path outside the project,
// an empty projectPath) is returned unchanged — an absolute machine prefix
// in the signature is useless, but forcing a foreign path into "relative"
// form would be misleading (rule 3).
func relativizePath(path, projectPath string) string {
	if projectPath == "" || path == "" {
		return path
	}
	p := strings.ReplaceAll(path, "\\", "/")
	proj := strings.ReplaceAll(projectPath, "\\", "/")
	proj = strings.TrimSuffix(proj, "/")

	if !strings.HasPrefix(strings.ToLower(p), strings.ToLower(proj)+"/") {
		return path
	}
	return p[len(proj)+1:]
}

// shellTokenize splits a shell command line into words and chain operators
// (&&, ||, |, ;), keeping single/double-quoted spans as one token (quote
// characters included, so a masked <ARG> replaces the whole quoted literal —
// callers never need to see inside it).
func shellTokenize(s string) []string {
	var tokens []string
	var buf strings.Builder
	inSingle, inDouble := false, false

	flush := func() {
		if buf.Len() > 0 {
			tokens = append(tokens, buf.String())
			buf.Reset()
		}
	}

	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case inSingle:
			buf.WriteRune(c)
			if c == '\'' {
				inSingle = false
			}
		case inDouble:
			buf.WriteRune(c)
			if c == '"' {
				inDouble = false
			}
		case c == '\'':
			buf.WriteRune(c)
			inSingle = true
		case c == '"':
			buf.WriteRune(c)
			inDouble = true
		case c == ' ' || c == '\t':
			flush()
		case c == '&' && i+1 < len(runes) && runes[i+1] == '&':
			flush()
			tokens = append(tokens, "&&")
			i++
		case c == '|' && i+1 < len(runes) && runes[i+1] == '|':
			flush()
			tokens = append(tokens, "||")
			i++
		case c == '|':
			flush()
			tokens = append(tokens, "|")
		case c == ';':
			flush()
			tokens = append(tokens, ";")
		default:
			buf.WriteRune(c)
		}
	}
	flush()
	return tokens
}

// splitSegments groups tokenized words into pipeline/chain segments,
// dropping the &&/||/|/; operators themselves.
func splitSegments(tokens []string) [][]string {
	var segments [][]string
	var cur []string
	for _, t := range tokens {
		switch t {
		case "&&", "||", "|", ";":
			if len(cur) > 0 {
				segments = append(segments, cur)
				cur = nil
			}
		default:
			cur = append(cur, t)
		}
	}
	if len(cur) > 0 {
		segments = append(segments, cur)
	}
	return segments
}

// pickSegment returns the first segment whose command is not a pure
// navigation/env command (rule 2). If every segment is navigational (a bare
// `cd dir`), it falls back to the first segment rather than producing an
// empty signature.
func pickSegment(segments [][]string) []string {
	for _, seg := range segments {
		if len(seg) == 0 {
			continue
		}
		if !navSkip[seg[0]] {
			return seg
		}
	}
	if len(segments) > 0 {
		return segments[0]
	}
	return nil
}

// baseName returns the filename component of a path, accepting both '/' and
// '\' separators regardless of the host OS — signatures must be
// deterministic across the Windows and Linux machines this app runs on.
func baseName(s string) string {
	s = strings.TrimRight(s, "/\\")
	if i := strings.LastIndexAny(s, "/\\"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// stripQuotes removes a single matching pair of surrounding quotes — the
// shellTokenize output keeps them so a masked <ARG> replaces the whole
// literal, but a preserved token (an interpreter's script path) must not
// carry them into the signature.
func stripQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

func firstWord(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t\n"); i >= 0 {
		return s[:i]
	}
	return s
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
