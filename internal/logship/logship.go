// Package logship pushes the manager's run logs of every project into one
// private git repository (one folder per host), so logs of several machines
// can be reviewed together. One pass is Ship; timers and UI live elsewhere.
package logship

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"claude-manager/internal/fsutil"
	"claude-manager/internal/proc"
	"claude-manager/internal/store"
)

const (
	cloneDirName = "cm-logs"
	branch       = "main"
	maxAttempts  = 3
	minAge       = 2 * time.Minute
	firstWindow  = 7 * 24 * time.Hour
	stampLayout  = "20060102-150405.000000"
)

// Project is a project whose run logs are shipped.
type Project struct {
	Name string
	Path string
}

// Options configure one pass.
type Options struct {
	Repo     string // URL of the logs repository (a bare repo path in tests)
	Host     string
	Build    string
	DataDir  string // manager data dir: holds the clone and logship.json
	Projects []Project
	Now      func() time.Time
}

// Result describes one pass.
type Result struct {
	At     time.Time
	Files  int
	Bytes  int64
	Pushed bool
	Error  string
}

// stampRe extracts the save time from a log file name
// (store.SaveSessionLogFile: "<session>-20261027-222114.273201.md").
var stampRe = regexp.MustCompile(`(\d{8}-\d{6}\.\d{6})\.md$`)

type shipFile struct {
	src, rel, stamp string
	size            int64
}

// Ship runs one pass. Failures are reported in Result.Error (and returned);
// marks move only after a successful push.
func Ship(ctx context.Context, opt Options) (Result, error) {
	now := time.Now
	if opt.Now != nil {
		now = opt.Now
	}
	res := Result{At: now()}
	fail := func(err error) (Result, error) {
		res.Error = err.Error()
		return res, err
	}
	if strings.TrimSpace(opt.Repo) == "" || strings.TrimSpace(opt.Host) == "" || opt.DataDir == "" {
		return fail(fmt.Errorf("logship: Repo, Host and DataDir are required"))
	}
	st, err := loadState(opt.DataDir)
	if err != nil {
		return fail(err)
	}
	clone := filepath.Join(opt.DataDir, cloneDirName)

	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if err := syncClone(ctx, opt.Repo, clone); err != nil {
			return fail(err)
		}
		files, marks, err := collect(opt, st, res.At, clone)
		if err != nil {
			return fail(err)
		}
		res.Files, res.Bytes, res.Pushed = 0, 0, false
		for _, f := range files {
			n, err := copyFile(f.src, filepath.Join(clone, filepath.FromSlash(f.rel)))
			if err != nil {
				return fail(err)
			}
			res.Files++
			res.Bytes += n
		}
		if _, err := os.Stat(filepath.Join(clone, "logs", fsutil.SafeName(opt.Host))); err == nil {
			if _, err := git(ctx, clone, "add", "-A", "logs/"+fsutil.SafeName(opt.Host)); err != nil {
				return fail(err)
			}
		}
		status, err := git(ctx, clone, "status", "--porcelain")
		if err != nil {
			return fail(err)
		}
		if strings.TrimSpace(status) != "" {
			msg := fmt.Sprintf("%s: %d logs, build %s", opt.Host, res.Files, opt.Build)
			if _, err := git(ctx, clone, "commit", "-q", "-m", msg); err != nil {
				return fail(err)
			}
			if _, err := gitLong(ctx, clone, "push", "-q", "origin", "HEAD:"+branch); err != nil {
				lastErr = err
				continue
			}
			res.Pushed = true
		} else {
			res.Files, res.Bytes = 0, 0
		}
		for p, m := range marks {
			st.Marks[p] = m
		}
		if err := saveState(opt.DataDir, st); err != nil {
			return fail(err)
		}
		return res, nil
	}
	res.Files, res.Bytes = 0, 0
	return fail(fmt.Errorf("logship: push failed after %d attempts: %w", maxAttempts, lastErr))
}

// syncClone makes clone an exact copy of origin/main (or an empty repo on
// branch main when the remote is empty).
func syncClone(ctx context.Context, repo, clone string) error {
	if _, err := os.Stat(filepath.Join(clone, ".git")); err != nil {
		if err := os.RemoveAll(clone); err != nil {
			return fmt.Errorf("logship: remove %s: %w", clone, err)
		}
		if err := os.MkdirAll(filepath.Dir(clone), 0o755); err != nil {
			return fmt.Errorf("logship: mkdir: %w", err)
		}
		if _, err := gitLong(ctx, filepath.Dir(clone), "clone", "-q", repo, filepath.Base(clone)); err != nil {
			return err
		}
	}
	// A moved repo URL must follow the setting.
	if _, err := git(ctx, clone, "remote", "set-url", "origin", repo); err != nil {
		return err
	}
	if _, err := gitLong(ctx, clone, "fetch", "-q", "--prune", "origin"); err != nil {
		return err
	}
	if _, err := git(ctx, clone, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+branch); err == nil {
		if _, err := git(ctx, clone, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
			return err
		}
		if _, err := git(ctx, clone, "reset", "-q", "--hard", "origin/"+branch); err != nil {
			return err
		}
		_, err := git(ctx, clone, "clean", "-fdq")
		return err
	}
	// Remote has no main: new or recreated. Local commits are then stale.
	if _, err := git(ctx, clone, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		if err := os.RemoveAll(clone); err != nil {
			return fmt.Errorf("logship: remove %s: %w", clone, err)
		}
		if _, err := gitLong(ctx, filepath.Dir(clone), "clone", "-q", repo, filepath.Base(clone)); err != nil {
			return err
		}
	}
	if _, err := git(ctx, clone, "symbolic-ref", "HEAD", "refs/heads/"+branch); err != nil {
		return err
	}
	_, err := git(ctx, clone, "clean", "-fdq")
	return err
}

// collect picks the files to ship and the new mark per project.
func collect(opt Options, st state, now time.Time, clone string) ([]shipFile, map[string]string, error) {
	var out []shipFile
	marks := map[string]string{}
	host := fsutil.SafeName(opt.Host)
	for _, p := range opt.Projects {
		dir := store.ProjectLogsDir(p.Path)
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue // no logs folder: skip this project only
		}
		mark, hasMark := st.Marks[p.Name]
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			if e.IsDir() || strings.HasSuffix(e.Name(), ".tmp") {
				continue
			}
			m := stampRe.FindStringSubmatch(e.Name())
			if m == nil {
				continue
			}
			stamp := m[1]
			t, err := time.ParseInLocation(stampLayout, stamp, time.Local)
			if err != nil {
				continue
			}
			if hasMark {
				if stamp <= mark {
					continue
				}
			} else if now.Sub(t) > firstWindow {
				continue
			}
			if now.Sub(t) < minAge {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			rel := fmt.Sprintf("logs/%s/%s/%s/%s", host, fsutil.SafeName(p.Name), stamp[:4]+"-"+stamp[4:6], e.Name())
			out = append(out, shipFile{src: filepath.Join(dir, e.Name()), rel: rel, stamp: stamp, size: info.Size()})
			if stamp > marks[p.Name] {
				marks[p.Name] = stamp
			}
		}
	}
	return out, marks, nil
}

func copyFile(src, dst string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return 0, fmt.Errorf("logship: mkdir: %w", err)
	}
	in, err := os.Open(src)
	if err != nil {
		return 0, fmt.Errorf("logship: open %s: %w", src, err)
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return 0, fmt.Errorf("logship: create %s: %w", dst, err)
	}
	n, err := io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, fmt.Errorf("logship: copy %s: %w", src, err)
	}
	return n, nil
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	return runGit(ctx, 2*time.Minute, dir, args...)
}

// gitLong is for commands that talk to the network.
func gitLong(ctx context.Context, dir string, args ...string) (string, error) {
	return runGit(ctx, 15*time.Minute, dir, args...)
}

// runGit runs git with a fixed identity passed per command (no git config is
// written) and never prompts for credentials.
func runGit(ctx context.Context, timeout time.Duration, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	full := append([]string{
		"-c", "user.name=claude-manager",
		"-c", "user.email=claude-manager@localhost",
		"-c", "core.autocrlf=false",
	}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	proc.HideConsole(cmd)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("logship: git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
