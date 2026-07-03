// Package worker implements the mixed-programming pipeline (MIXED-TASKS.md):
// external OpenAI-compatible models produce FIND/REPLACE patches which the
// manager validates, applies in an isolated worktree and gates locally.
package worker

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Patch is one FIND/REPLACE hunk against a single file, in the wire format
// models are briefed to emit (port of lumen-browser apply_patches.py):
//
//	### PATCH n
//	FILE <relative/path>
//	<<<FIND
//	verbatim anchor lines
//	===REPLACE
//	replacement lines
//	>>>END
type Patch struct {
	Index   int    // number from the "### PATCH n" header (sequential if absent)
	File    string // relative path from the FILE line
	Find    string // verbatim anchor, LF line endings
	Replace string // replacement text, LF line endings
}

// ParseResult carries parsed patches plus non-fatal diagnostics. Warnings
// record format breakages the parser recovered from (missing >>>END, ===END
// instead of >>>END) — they go into model feedback so the next round is clean.
type ParseResult struct {
	Patches  []Patch
	Warnings []string
}

// Parser states.
const (
	stOutside = iota // between patches; everything ignored except a header
	stWantFile
	stWantFind
	stFind
	stReplace
)

const (
	markHeader  = "### PATCH"
	markFile    = "FILE"
	markFind    = "<<<FIND"
	markReplace = "===REPLACE"
	markEnd     = ">>>END"
	markEndAlt  = "===END" // known nemotron-ultra slip, accepted with a warning
)

// ParsePatches extracts patches from raw model output. Text outside patch
// blocks (prose, markdown fences) is ignored. Recoverable breakages observed
// in real lumen rounds are spliced with a warning: a new "### PATCH" header
// while the previous REPLACE block is still open (missing >>>END), ===END in
// place of >>>END, and EOF inside a REPLACE block. Structural damage that
// makes a patch unusable (EOF inside FIND, missing FILE, empty FIND) is an
// error naming the patch and line.
func ParsePatches(text string) (*ParseResult, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	res := &ParseResult{}
	state := stOutside
	var cur Patch
	var body []string

	closePatch := func() error {
		if strings.TrimSpace(cur.Find) == "" {
			return fmt.Errorf("PATCH %d (%s): empty FIND block", cur.Index, cur.File)
		}
		cur.Replace = strings.Join(body, "\n")
		res.Patches = append(res.Patches, cur)
		body = nil
		return nil
	}

	for i, raw := range lines {
		lineNo := i + 1
		trimmed := strings.TrimSpace(raw)

		if strings.HasPrefix(trimmed, markHeader) {
			switch state {
			case stReplace:
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"PATCH %d (%s): missing %s before next patch header (line %d) — spliced",
					cur.Index, cur.File, markEnd, lineNo))
				if err := closePatch(); err != nil {
					return nil, err
				}
			case stFind:
				return nil, fmt.Errorf(
					"line %d: new patch header inside FIND block of PATCH %d (%s)",
					lineNo, cur.Index, cur.File)
			case stWantFile, stWantFind:
				return nil, fmt.Errorf(
					"line %d: PATCH %d has no %s block", lineNo, cur.Index, markFind)
			}
			cur = Patch{Index: headerIndex(trimmed, len(res.Patches)+1)}
			state = stWantFile
			continue
		}

		switch state {
		case stOutside:
			// prose / fences between patches — ignore

		case stWantFile:
			if trimmed == "" {
				continue
			}
			if !strings.HasPrefix(trimmed, markFile) {
				return nil, fmt.Errorf(
					"line %d: PATCH %d: expected %s <path>, got %q",
					lineNo, cur.Index, markFile, trimmed)
			}
			path := strings.TrimSpace(strings.TrimPrefix(trimmed, markFile))
			if path == "" {
				return nil, fmt.Errorf("line %d: PATCH %d: empty FILE path", lineNo, cur.Index)
			}
			cur.File = path
			state = stWantFind

		case stWantFind:
			if trimmed == "" {
				continue
			}
			if trimmed != markFind {
				return nil, fmt.Errorf(
					"line %d: PATCH %d (%s): expected %s, got %q",
					lineNo, cur.Index, cur.File, markFind, trimmed)
			}
			state = stFind

		case stFind:
			switch trimmed {
			case markReplace:
				cur.Find = strings.Join(body, "\n")
				body = nil
				state = stReplace
			case markEnd, markEndAlt:
				return nil, fmt.Errorf(
					"line %d: PATCH %d (%s): %s before %s — no REPLACE block",
					lineNo, cur.Index, cur.File, trimmed, markReplace)
			default:
				body = append(body, raw)
			}

		case stReplace:
			switch trimmed {
			case markEnd:
				if err := closePatch(); err != nil {
					return nil, err
				}
				state = stOutside
			case markEndAlt:
				res.Warnings = append(res.Warnings, fmt.Sprintf(
					"PATCH %d (%s): %s used instead of %s (line %d) — accepted",
					cur.Index, cur.File, markEndAlt, markEnd, lineNo))
				if err := closePatch(); err != nil {
					return nil, err
				}
				state = stOutside
			default:
				body = append(body, raw)
			}
		}
	}

	switch state {
	case stReplace:
		res.Warnings = append(res.Warnings, fmt.Sprintf(
			"PATCH %d (%s): missing %s at end of output — spliced",
			cur.Index, cur.File, markEnd))
		if err := closePatch(); err != nil {
			return nil, err
		}
	case stFind:
		return nil, fmt.Errorf(
			"PATCH %d (%s): output ends inside FIND block", cur.Index, cur.File)
	case stWantFile, stWantFind:
		return nil, fmt.Errorf(
			"PATCH %d: output ends before %s block", cur.Index, markFind)
	}

	if len(res.Patches) == 0 {
		return nil, fmt.Errorf("no patches found in output (%d lines of prose)", len(lines))
	}
	return res, nil
}

// headerIndex parses n from "### PATCH n", falling back to seq.
func headerIndex(header string, seq int) int {
	rest := strings.TrimSpace(strings.TrimPrefix(header, markHeader))
	if n, err := strconv.Atoi(strings.TrimSuffix(rest, ":")); err == nil && n > 0 {
		return n
	}
	return seq
}

// ValidatePatch checks that p.Find occurs exactly once in content, verbatim.
// On failure the error text is model feedback: it pinpoints the closest match
// and shows the exact expected/actual line pair at the first divergence
// (step37's known failure is dropping one line from the anchor).
func ValidatePatch(content string, p Patch) error {
	switch n := strings.Count(content, p.Find); {
	case n == 1:
		return nil
	case n > 1:
		return fmt.Errorf(
			"PATCH %d (%s): FIND matches %d locations, must be unique — extend the anchor with surrounding lines",
			p.Index, p.File, n)
	}
	return fmt.Errorf("PATCH %d (%s): FIND not found verbatim.\n%s",
		p.Index, p.File, diagnoseMismatch(content, p.Find))
}

// diagnoseMismatch locates the content offset where the longest prefix of
// FIND lines matches and reports the first divergence, expected vs actual.
func diagnoseMismatch(content, find string) string {
	cl := strings.Split(content, "\n")
	fl := strings.Split(find, "\n")

	bestOff, bestLen := -1, -1
	for off := range cl {
		n := 0
		for n < len(fl) && off+n < len(cl) && cl[off+n] == fl[n] {
			n++
		}
		if n > bestLen {
			bestOff, bestLen = off, n
		}
	}

	if bestLen <= 0 {
		msg := fmt.Sprintf("first FIND line not found anywhere in file: %q", fl[0])
		if ws := whitespaceHint(cl, fl[0]); ws != "" {
			msg += "\n" + ws
		}
		return msg
	}
	if bestOff+bestLen >= len(cl) {
		return fmt.Sprintf(
			"closest match at file line %d: file ends after %d matching line(s); FIND has %d more line(s)",
			bestOff+1, bestLen, len(fl)-bestLen)
	}
	expected, actual := fl[bestLen], cl[bestOff+bestLen]
	msg := fmt.Sprintf(
		"closest match at file line %d: first %d line(s) match, then diverge.\nexpected (FIND line %d): %q\nactual   (file line %d): %q",
		bestOff+1, bestLen, bestLen+1, expected, bestOff+bestLen+1, actual)
	if strings.TrimSpace(expected) == strings.TrimSpace(actual) {
		msg += "\nhint: whitespace-only mismatch — check indentation and tabs vs spaces"
	}
	return msg
}

// whitespaceHint reports when a line exists in the file but differs from the
// anchor only by leading/trailing whitespace.
func whitespaceHint(contentLines []string, findLine string) string {
	want := strings.TrimSpace(findLine)
	if want == "" {
		return ""
	}
	for i, l := range contentLines {
		if strings.TrimSpace(l) == want {
			return fmt.Sprintf(
				"hint: file line %d matches ignoring whitespace — check indentation and tabs vs spaces", i+1)
		}
	}
	return ""
}

// CheckASCIIAnchors returns a warning per patch whose FIND contains non-ASCII
// bytes. Used with WorkerConfig.ASCIIAnchorsOnly: nemotron-ultra corrupts
// cyrillic in verbatim anchors, so briefs for it must keep anchors pure ASCII.
func CheckASCIIAnchors(patches []Patch) []string {
	var warnings []string
	for _, p := range patches {
		for i, line := range strings.Split(p.Find, "\n") {
			if !isASCII(line) {
				warnings = append(warnings, fmt.Sprintf(
					"PATCH %d (%s): FIND line %d contains non-ASCII characters: %q — unsafe for ascii_anchors_only workers",
					p.Index, p.File, i+1, line))
				break
			}
		}
	}
	return warnings
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] > 127 {
			return false
		}
	}
	return true
}

// RejectedPatch pairs a patch with the reason it was not applied. Reason is
// written for model feedback (exact expected/actual diff, see ValidatePatch).
type RejectedPatch struct {
	Patch  Patch
	Reason string
}

// ApplyResult reports the outcome of ApplyPatches. Rejected patches do not
// stop the run: the round orchestrator feeds all rejections back at once.
type ApplyResult struct {
	Applied  []Patch
	Rejected []RejectedPatch
}

// ApplyPatches applies patches to files under root (a worktree). Paths are
// confined to root (the _safe_path analog): absolute paths, drive-relative
// paths and ".." escapes are rejected per patch. Sequential patches to the
// same file see each other's edits. Writes are atomic (tmp + rename), one per
// touched file, flushed only after all patches are processed. An error is
// returned only for environmental failures (unusable root, I/O on flush);
// per-patch problems land in Rejected.
func ApplyPatches(root string, patches []Patch) (*ApplyResult, error) {
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("worker: apply root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("worker: apply root %s is not a directory", root)
	}

	res := &ApplyResult{}
	contents := map[string]string{} // abs path -> current (LF-normalized) content
	hadCRLF := map[string]bool{}
	var order []string // dirty files in first-touch order

	for _, p := range patches {
		abs, err := safePath(root, p.File)
		if err != nil {
			res.Rejected = append(res.Rejected, RejectedPatch{p, fmt.Sprintf(
				"PATCH %d (%s): %v", p.Index, p.File, err)})
			continue
		}
		content, ok := contents[abs]
		if !ok {
			raw, err := os.ReadFile(abs)
			if err != nil {
				res.Rejected = append(res.Rejected, RejectedPatch{p, fmt.Sprintf(
					"PATCH %d (%s): cannot read file: %v", p.Index, p.File, err)})
				continue
			}
			content = string(raw)
			// Patch bodies are LF-normalized by the parser; normalize the file
			// the same way for matching and restore CRLF on flush.
			if strings.Contains(content, "\r\n") {
				hadCRLF[abs] = true
				content = strings.ReplaceAll(content, "\r\n", "\n")
			}
			contents[abs] = content
			order = append(order, abs)
		}
		if err := ValidatePatch(content, p); err != nil {
			res.Rejected = append(res.Rejected, RejectedPatch{p, err.Error()})
			continue
		}
		contents[abs] = strings.Replace(content, p.Find, p.Replace, 1)
		res.Applied = append(res.Applied, p)
	}

	applied := map[string]bool{}
	for _, p := range res.Applied {
		abs, _ := safePath(root, p.File)
		applied[abs] = true
	}
	for _, abs := range order {
		if !applied[abs] {
			continue // file only touched by rejected patches — leave untouched
		}
		out := contents[abs]
		if hadCRLF[abs] {
			out = strings.ReplaceAll(out, "\n", "\r\n")
		}
		if err := atomicWrite(abs, []byte(out)); err != nil {
			return nil, fmt.Errorf("worker: flush %s: %w", abs, err)
		}
	}
	return res, nil
}

// safePath resolves rel inside root, rejecting anything that escapes it.
func safePath(root, rel string) (string, error) {
	if filepath.IsAbs(rel) || filepath.VolumeName(rel) != "" {
		return "", fmt.Errorf("absolute path not allowed: %s", rel)
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes worktree: %s", rel)
	}
	return filepath.Join(root, clean), nil
}

// atomicWrite writes data to path via a tmp file in the same directory and a
// rename, so gates never observe a half-written file.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".patch-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
