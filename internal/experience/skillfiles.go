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

// archivedSkillsDir is where a switched-off skill's folder is moved, under
// the project's own gitignored .claude-manager/ (LEARN-TASKS.md LN-24): out
// of .claude/skills so the CLI no longer offers it, but kept so a restore
// brings back the exact file — including any hand edits made after it was
// applied.
const archivedSkillsDir = ".claude-manager/archived-skills"

// ArchivedSkillPath returns where skill id named name is kept while
// archived. The id suffix keeps two rows sharing a name (a re-distillation)
// from overwriting each other's archived copy.
func ArchivedSkillPath(projectPath, name string, id int64) (string, error) {
	if !ValidSkillName(name) {
		return "", fmt.Errorf("experience: invalid skill name %q", name)
	}
	return confinedSkillPath(projectPath, fmt.Sprintf("%s/%s-%d", archivedSkillsDir, name, id))
}

// ArchiveSkillFile moves <project>/.claude/skills/<name>/ to the archive
// (ArchivedSkillPath), so the CLI stops seeing the skill. A skill folder that
// is already gone (never applied, or deleted by hand) is not an error —
// archiving must still be able to record the decision. An existing archived
// copy for the same id is replaced.
func ArchiveSkillFile(projectPath, name string, id int64) error {
	file, err := SkillFilePath(projectPath, name)
	if err != nil {
		return err
	}
	src := filepath.Dir(file)
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil
	}
	dst, err := ArchivedSkillPath(projectPath, name, id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("experience: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("experience: clear archived copy: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("experience: archive skill: %w", err)
	}
	return nil
}

// RestoreSkillFile moves an archived skill folder back to
// <project>/.claude/skills/<name>/. When there is no archived copy (the row
// was archived before archiving moved files, or the copy was deleted) md is
// written instead. Refuses to replace a skill folder that already exists
// unless overwrite is true (ErrSkillFileExists), exactly like
// WriteSkillFile. Returns the SKILL.md path.
func RestoreSkillFile(projectPath, name string, id int64, md string, overwrite bool) (string, error) {
	file, err := SkillFilePath(projectPath, name)
	if err != nil {
		return "", err
	}
	src, err := ArchivedSkillPath(projectPath, name, id)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filepath.Join(src, "SKILL.md")); err != nil {
		return WriteSkillFile(projectPath, name, md, overwrite)
	}
	dst := filepath.Dir(file)
	if _, err := os.Stat(dst); err == nil {
		if !overwrite {
			return "", ErrSkillFileExists
		}
		if err := os.RemoveAll(dst); err != nil {
			return "", fmt.Errorf("experience: replace skill folder: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", fmt.Errorf("experience: mkdir %s: %w", filepath.Dir(dst), err)
	}
	if err := os.Rename(src, dst); err != nil {
		return "", fmt.Errorf("experience: restore skill: %w", err)
	}
	return file, nil
}

// ExistingSkill is one skill already present in a project's .claude/skills
// folder — whoever wrote it (the user, a protocol install, the autopilot).
type ExistingSkill struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// ListProjectSkills reads every <project>/.claude/skills/*/SKILL.md and
// returns its folder name and frontmatter description — the autopilot's
// reviewer must see the whole library, not just the rows this app produced,
// to judge whether a draft duplicates something. Unreadable entries are
// skipped.
func ListProjectSkills(projectPath string) []ExistingSkill {
	dir := filepath.Join(projectPath, ".claude", "skills")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []ExistingSkill
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name(), "SKILL.md"))
		if err != nil {
			continue
		}
		out = append(out, ExistingSkill{Name: e.Name(), Description: frontmatterField(string(data), "description")})
	}
	return out
}

// frontmatterField returns a single-line `key: value` from a markdown file's
// leading YAML frontmatter, unquoted; "" when absent.
func frontmatterField(md, key string) string {
	md = strings.TrimPrefix(md, "\ufeff")
	if !strings.HasPrefix(md, "---") {
		return ""
	}
	lines := strings.Split(md, "\n")
	for _, l := range lines[1:] {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "---" {
			break
		}
		if v, ok := strings.CutPrefix(l, key+":"); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}
