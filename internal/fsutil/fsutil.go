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

// ResolveCaseInsensitive case-corrects path's components relative to root,
// for a path that was typed or copy-pasted on a case-insensitive filesystem
// (Windows, default macOS) and is now being read on a case-sensitive one
// (Linux) — e.g. a configured task_source of "STATUS-P3.MD" when the real
// file on disk is "STATUS-P3.md". Read that way, the mismatch looks
// identical to a genuinely missing file.
//
// If path already exists as given, it is returned unchanged (the common
// case, and already correct on a case-insensitive filesystem — nothing
// needed resolving). Otherwise each component of path below root is checked
// case-insensitively against that directory's actual entries via a single
// os.ReadDir, walking down one level at a time. The first component with no
// case-insensitive match stops resolution and the original path is returned
// unchanged, so a genuinely missing file still reports as missing.
// Resolution never walks outside root: a path that does not resolve to
// somewhere under root (e.g. via "..") is returned unchanged.
func ResolveCaseInsensitive(root, path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}

	root = filepath.Clean(root)
	clean := filepath.Clean(path)

	rel, err := filepath.Rel(root, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}

	parts := strings.Split(rel, string(filepath.Separator))
	current := root
	for _, part := range parts {
		if part == "." || part == "" {
			continue
		}
		candidate := filepath.Join(current, part)
		if _, err := os.Stat(candidate); err == nil {
			current = candidate
			continue
		}
		entries, err := os.ReadDir(current)
		if err != nil {
			return path
		}
		matched := ""
		for _, e := range entries {
			if strings.EqualFold(e.Name(), part) {
				matched = e.Name()
				break
			}
		}
		if matched == "" {
			return path
		}
		current = filepath.Join(current, matched)
	}
	return current
}
