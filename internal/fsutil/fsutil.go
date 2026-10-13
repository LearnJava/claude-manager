// Package fsutil holds small filesystem helpers with no internal imports, so
// packages that would otherwise import-cycle each other (internal/session and
// internal/analysis both read task_source/roadmap files by a user-typed path)
// can depend on it instead of duplicating this logic.
package fsutil

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveCaseInsensitive returns path with each component below root spelled
// the way it is on disk, for a path typed or copy-pasted in another case —
// e.g. a configured task_source of "STATUS-P3.MD" when the real file is
// "STATUS-P3.md". On a case-sensitive filesystem (Linux) the mistyped path
// does not open at all; on a case-insensitive one (Windows, default macOS)
// it opens, so os.Stat cannot be trusted to mean "spelled right" — and every
// case-sensitive consumer still misses it: `git show origin/main:STATUS-P3.MD`
// fails on any OS, git tree paths being case-sensitive. So the walk runs on
// every OS.
//
// Each component below root is looked up among its directory's actual
// entries (one os.ReadDir per level): an exact match wins — on a
// case-sensitive filesystem "a.md" and "A.md" can both exist — otherwise the
// first case-insensitive one. The first component with no match stops
// resolution and the original path is returned unchanged, so a genuinely
// missing file still reports as missing; a path that needed no correction is
// returned unchanged too, byte for byte. Resolution never walks outside root:
// a path that does not resolve to somewhere under root (e.g. via "..") is
// returned unchanged.
func ResolveCaseInsensitive(root, path string) string {
	root = filepath.Clean(root)
	clean := filepath.Clean(path)

	rel, err := filepath.Rel(root, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}

	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return path
		}
		matched := ""
		for _, e := range entries {
			if e.Name() == part {
				matched = part
				break
			}
			if matched == "" && strings.EqualFold(e.Name(), part) {
				matched = e.Name()
			}
		}
		if matched == "" {
			return path
		}
		current = filepath.Join(current, matched)
	}
	if current == clean {
		return path
	}
	return current
}
