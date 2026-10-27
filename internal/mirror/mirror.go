// Package mirror keeps a local, read-only copy of each opted-in project's
// manager settings, Claude Code settings and saved run logs in one folder the
// manager owns (by default <claude-manager checkout>/projects). An agent
// working in the claude-manager folder can then find every project, see how it
// is configured and read its run logs without knowing where the project lives.
//
// The project folder stays the source of truth: Sync only copies from it,
// never into it. The mirror folder is its own git repository with no remote;
// every sync that changes something is committed there so the history of a
// project's settings and logs is kept. Nothing is ever pushed.
package mirror

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/BurntSushi/toml"

	"claude-manager/internal/config"
	"claude-manager/internal/fsutil"
	"claude-manager/internal/proc"
)

// FilesDir is the subfolder of a project's mirror that holds the copied files
// under their project-relative paths.
const FilesDir = "files"

// ManifestFile describes one mirrored project: name, source path, file list.
const ManifestFile = "PROJECT.md"

// EffectiveConfigFile is the project's configuration as the manager runs it
// (global entry merged with the folder overlay, defaults applied).
const EffectiveConfigFile = "effective-config.toml"

// maxFileSize skips files larger than this; settings and run logs are far
// smaller, anything bigger is not what the mirror is for.
const maxFileSize = 5 << 20

// mirroredFiles are copied when present, relative to the project folder.
var mirroredFiles = []string{
	"CLAUDE.md",
	"CLAUDE.local.md",
	".mcp.json",
	".claude/settings.json",
	".claude/settings.local.json",
	".claude-manager/config.toml",
	".claude-manager/config.local.toml",
	".claude-manager/journal.md",
}

// mirroredDirs are copied recursively when present.
var mirroredDirs = []string{
	".claude/hooks",
	".claude/skills",
	".claude/agents",
	".claude/commands",
	".claude-manager/logs",
	".claude-manager/archived-skills",
}

// mu serializes syncs: the timer, a settings save and the UI button may all
// fire at once, and they share one git repository.
var mu sync.Mutex

// Result reports one project's sync.
type Result struct {
	Project string `json:"project"`
	Dir     string `json:"dir"`
	Files   int    `json:"files"`   // files in the mirror after the sync
	Changed int    `json:"changed"` // files written or removed by this sync
	Error   string `json:"error,omitempty"`
}

// DefaultRoot is the mirror folder used when settings leave mirror_dir empty:
// <checkout>/projects when the running binary sits in a claude-manager
// checkout's build/bin (how run-claude-manager.bat builds it), otherwise
// ~/.claude-manager/projects.
func DefaultRoot() string {
	if exe, err := os.Executable(); err == nil {
		if root := checkoutFromExe(exe); root != "" {
			return filepath.Join(root, "projects")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".claude-manager", "projects")
	}
	return filepath.Join(home, ".claude-manager", "projects")
}

// Root resolves the configured mirror folder.
func Root(s config.GlobalSettings) string {
	if s.MirrorDir != "" {
		return s.MirrorDir
	}
	return DefaultRoot()
}

// checkoutFromExe returns the claude-manager checkout that exe was built into
// (<checkout>/build/bin/<exe>), or "" when exe lives anywhere else.
func checkoutFromExe(exe string) string {
	bin := filepath.Dir(exe)
	build := filepath.Dir(bin)
	if !strings.EqualFold(filepath.Base(bin), "bin") || !strings.EqualFold(filepath.Base(build), "build") {
		return ""
	}
	root := filepath.Dir(build)
	if _, err := os.Stat(filepath.Join(root, "wails.json")); err != nil {
		return ""
	}
	return root
}

// DirName maps a project name to its folder under the mirror root.
func DirName(project string) string {
	return fsutil.SafeName(project)
}

// SyncAll copies every project with Mirror set into root, rewrites the index
// README, and commits the result to root's local repository when anything
// changed. A failing project is reported in its Result and does not stop the
// others; the returned error is for the repository itself.
func SyncAll(ctx context.Context, root string, projects []config.ProjectConfig) ([]Result, error) {
	mu.Lock()
	defer mu.Unlock()

	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("mirror: create %s: %w", root, err)
	}
	if err := ensureRepo(ctx, root); err != nil {
		return nil, err
	}

	var results []Result
	var mirrored []config.ProjectConfig
	for _, p := range projects {
		if !p.Mirror {
			continue
		}
		mirrored = append(mirrored, p)
		r, err := syncProject(root, p)
		if err != nil {
			r.Error = err.Error()
		}
		results = append(results, r)
	}
	if err := writeIfChanged(filepath.Join(root, "README.md"), renderIndex(mirrored)); err != nil {
		return results, err
	}
	if err := commit(ctx, root, commitMessage(results)); err != nil {
		return results, err
	}
	return results, nil
}

// syncProject makes root/<project>/ match the project's current files.
func syncProject(root string, p config.ProjectConfig) (Result, error) {
	dir := filepath.Join(root, DirName(p.Name))
	r := Result{Project: p.Name, Dir: dir}
	if p.Path == "" {
		return r, fmt.Errorf("project %q has no folder", p.Name)
	}
	if info, err := os.Stat(p.Path); err != nil || !info.IsDir() {
		return r, fmt.Errorf("project folder %s is not accessible", p.Path)
	}

	want, err := collect(p)
	if err != nil {
		return r, err
	}

	filesRoot := filepath.Join(dir, FilesDir)
	have := map[string]bool{}
	_ = filepath.WalkDir(filesRoot, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if rel, err := filepath.Rel(filesRoot, path); err == nil {
				have[filepath.ToSlash(rel)] = true
			}
		}
		return nil
	})

	rels := make([]string, 0, len(want))
	for rel := range want {
		rels = append(rels, rel)
	}
	sort.Strings(rels)

	for _, rel := range rels {
		data, err := os.ReadFile(want[rel])
		if err != nil {
			continue // vanished between the walk and the read
		}
		dst := filepath.Join(filesRoot, filepath.FromSlash(rel))
		changed, err := writeChanged(dst, data)
		if err != nil {
			return r, err
		}
		if changed {
			r.Changed++
		}
		delete(have, rel)
	}
	// Whatever is left was deleted in the project: the mirror is a copy, and
	// the old content stays in the mirror repository's history.
	for rel := range have {
		if err := os.Remove(filepath.Join(filesRoot, filepath.FromSlash(rel))); err == nil {
			r.Changed++
		}
	}
	removeEmptyDirs(filesRoot)
	r.Files = len(rels)

	var cfgBuf bytes.Buffer
	cfgBuf.WriteString("# The project's configuration as claude-manager runs it: the global\n")
	cfgBuf.WriteString("# [[project]] entry merged with the folder overlay, defaults applied.\n")
	cfgBuf.WriteString("# Edit the real files, not this copy (see PROJECT.md).\n\n")
	if err := toml.NewEncoder(&cfgBuf).Encode(p); err != nil {
		return r, fmt.Errorf("mirror: encode config: %w", err)
	}
	for name, data := range map[string][]byte{
		EffectiveConfigFile: cfgBuf.Bytes(),
		ManifestFile:        renderManifest(p, rels),
	} {
		changed, err := writeChanged(filepath.Join(dir, name), data)
		if err != nil {
			return r, err
		}
		if changed {
			r.Changed++
		}
	}
	return r, nil
}

// collect maps each project-relative slash path to mirror onto its source file.
func collect(p config.ProjectConfig) (map[string]string, error) {
	out := map[string]string{}
	add := func(rel string) {
		src := filepath.Join(p.Path, filepath.FromSlash(rel))
		if info, err := os.Stat(src); err == nil && info.Mode().IsRegular() && info.Size() <= maxFileSize {
			out[rel] = src
		}
	}
	for _, rel := range mirroredFiles {
		add(rel)
	}
	// Task queues the project's sessions work from show what they are doing.
	for _, s := range p.Sessions {
		if rel, ok := insideProject(p.Path, s.TaskSource); ok {
			add(rel)
		}
	}
	for _, relDir := range mirroredDirs {
		base := filepath.Join(p.Path, filepath.FromSlash(relDir))
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			if d.IsDir() {
				return nil
			}
			if rel, err := filepath.Rel(p.Path, path); err == nil {
				add(filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("mirror: walk %s: %w", base, err)
		}
	}
	return out, nil
}

// insideProject turns a session's task_source into a project-relative slash
// path, rejecting empty, absolute-elsewhere and ..-escaping values.
func insideProject(projectPath, source string) (string, bool) {
	if source == "" {
		return "", false
	}
	abs := source
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(projectPath, source)
	}
	rel, err := filepath.Rel(projectPath, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func renderManifest(p config.ProjectConfig, rels []string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", p.Name)
	fmt.Fprintf(&b, "- Project folder: `%s`\n", p.Path)
	b.WriteString("- This is a read-only copy kept by claude-manager. Fix settings in the project folder\n")
	b.WriteString("  (paths below are relative to it) or in claude-manager Settings, not here.\n")
	fmt.Fprintf(&b, "- `%s` — the configuration as the manager runs it (global entry + `.claude-manager/` overlay).\n", EffectiveConfigFile)
	fmt.Fprintf(&b, "- `%s/` — copies of the files below; history: `git log -- %s/`.\n\n", FilesDir, DirName(p.Name))
	if len(p.Sessions) > 0 {
		b.WriteString("## Sessions\n\n")
		for _, s := range p.Sessions {
			fmt.Fprintf(&b, "- %s — model %s, permission %s", s.Name, orDash(s.Model), orDash(s.PermissionMode))
			if s.TaskSource != "" {
				fmt.Fprintf(&b, ", queue `%s`", s.TaskSource)
			}
			if s.AutoRestart {
				b.WriteString(", auto-restart")
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("## Files\n\n")
	if len(rels) == 0 {
		b.WriteString("(none found)\n")
	}
	for _, rel := range rels {
		fmt.Fprintf(&b, "- `%s`\n", rel)
	}
	return []byte(b.String())
}

func renderIndex(projects []config.ProjectConfig) []byte {
	var b strings.Builder
	b.WriteString("# Mirrored projects\n\n")
	b.WriteString("Local copies of the settings and saved run logs of projects managed by\n")
	b.WriteString("claude-manager (Settings → Projects → \"Mirror into Claude Manager\").\n")
	b.WriteString("Re-synced automatically and committed to this folder's own git repository,\n")
	b.WriteString("which has no remote. Read-only: the project folder is the source of truth.\n\n")
	b.WriteString("| Project | Folder | Mirror |\n|---|---|---|\n")
	for _, p := range projects {
		fmt.Fprintf(&b, "| %s | `%s` | [%s/](./%s/%s) |\n", p.Name, p.Path, DirName(p.Name), DirName(p.Name), ManifestFile)
	}
	return []byte(b.String())
}

func commitMessage(results []Result) string {
	var parts []string
	for _, r := range results {
		if r.Changed > 0 {
			parts = append(parts, fmt.Sprintf("%s (%d)", r.Project, r.Changed))
		}
	}
	if len(parts) == 0 {
		return "sync"
	}
	return "sync: " + strings.Join(parts, ", ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// writeChanged writes data to path unless it already holds exactly that.
func writeChanged(path string, data []byte) (bool, error) {
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("mirror: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return false, fmt.Errorf("mirror: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("mirror: %w", err)
	}
	return true, nil
}

func writeIfChanged(path string, data []byte) error {
	_, err := writeChanged(path, data)
	return err
}

// removeEmptyDirs deletes directories left empty under root (deepest first).
func removeEmptyDirs(root string) {
	var dirs []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() && path != root {
			dirs = append(dirs, path)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i]) // fails, harmlessly, when not empty
	}
}

// ensureRepo makes root its own git repository. A plain "inside a work tree"
// check is not enough: the default root sits inside the claude-manager
// checkout, whose repository would answer for it.
func ensureRepo(ctx context.Context, root string) error {
	if _, err := os.Stat(filepath.Join(root, ".git")); err == nil {
		return nil
	}
	if _, err := git(ctx, root, "init", "-q"); err != nil {
		return err
	}
	return nil
}

// commit stages everything under root and commits it when anything changed.
func commit(ctx context.Context, root, msg string) error {
	if _, err := git(ctx, root, "add", "-A", "."); err != nil {
		return err
	}
	out, err := git(ctx, root, "status", "--porcelain")
	if err != nil {
		return err
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	_, err = git(ctx, root, "commit", "-q", "-m", msg)
	return err
}

// git runs git in root with a fixed local identity, passed per command so no
// git config is written. core.autocrlf=false stores the copies byte-for-byte.
func git(ctx context.Context, root string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	full := append([]string{
		"-c", "user.name=claude-manager",
		"-c", "user.email=claude-manager@localhost",
		"-c", "core.autocrlf=false",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	proc.HideConsole(cmd)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("mirror: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
