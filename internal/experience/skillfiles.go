package experience

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ErrSkillFileExists is returned by WriteSkillFile when
// <project>/.claude/skills/<name>/SKILL.md already exists and overwrite is
// false — mirrors analysis.ErrRoadmapFilesExist: a skill file is
// repo-editable, and a second approval must never silently clobber it.
// app.go surfaces this as PlanReview.svelte-style "already exists —
// overwrite?" banner (LEARN-TASKS.md LN-10), never window.confirm().
var ErrSkillFileExists = errors.New("experience: skill file already exists")

// skillNameRe is LEARN-TASKS.md LN-10's constraint on an approved skill's
// directory name: "[a-z0-9-]" only. This alone already rules out any
// path-traversal name ("." and "/" are outside the class), but
// SkillFilePath additionally runs the computed path through confinedPath as
// defense in depth.
var skillNameRe = regexp.MustCompile(`^[a-z0-9-]+$`)

// ValidSkillName reports whether name is safe to use as a skill directory
// name — non-empty and restricted to lowercase letters, digits and hyphens.
func ValidSkillName(name string) bool {
	return name != "" && skillNameRe.MatchString(name)
}

// SkillFilePath returns the path a skill named name would be written to
// under projectPath, without touching the filesystem — used to check for an
// existing file before showing the overwrite banner.
func SkillFilePath(projectPath, name string) (string, error) {
	if !ValidSkillName(name) {
		return "", fmt.Errorf("experience: invalid skill name %q", name)
	}
	return confinedSkillPath(projectPath, filepath.Join(".claude", "skills", name, "SKILL.md"))
}

// WriteSkillFile atomically writes md to
// <projectPath>/.claude/skills/<name>/SKILL.md (tmp -> rename), refusing to
// overwrite an existing file unless overwrite is true. Unlike the auto-saved
// logs/journal elsewhere in this app, the file is never gitignored — skills
// are meant to be committed and shared (LEARN-TASKS.md LN-10).
func WriteSkillFile(projectPath, name, md string, overwrite bool) (string, error) {
	path, err := SkillFilePath(projectPath, name)
	if err != nil {
		return "", err
	}
	if !overwrite {
		if _, statErr := os.Stat(path); statErr == nil {
			return "", ErrSkillFileExists
		}
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("experience: mkdir %s: %w", dir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(md), 0o644); err != nil {
		return "", fmt.Errorf("experience: write skill file: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return "", fmt.Errorf("experience: rename skill file: %w", err)
	}
	return path, nil
}

// confinedSkillPath resolves rel against root and rejects anything that
// would escape it — a duplicate of internal/analysis/roadmapview.go's
// confinedPath rather than an import: that package doesn't import
// internal/experience (would cycle with transcript.go's internal/session
// import elsewhere in this package), and this one small function isn't
// worth a shared package for.
func confinedSkillPath(root, rel string) (string, error) {
	if strings.TrimSpace(rel) == "" {
		return "", errors.New("experience: empty path")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	full, err := filepath.Abs(filepath.Join(absRoot, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	relPath, err := filepath.Rel(absRoot, full)
	if err != nil {
		return "", err
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("experience: path escapes the project: %s", rel)
	}
	return full, nil
}
