package session

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"claude-manager/internal/gitutil"
	"claude-manager/internal/logger"
	"claude-manager/internal/worktree"
)

// Start-stage thresholds: the "still initializing" line first appears after
// initSlowFirst, then repeats every initSlowEvery; an init faster than
// initQuiet is not worth a log line. Variables so tests can shrink them.
var (
	initSlowFirst = 15 * time.Second
	initSlowEvery = 30 * time.Second
	initQuiet     = 3 * time.Second
)

// prepareWorkDir returns the directory the CLI process runs in. With
// UseWorktree it is the session's persistent pool slot
// (<project>/.claude/worktrees/<slot>), created on first use; the CLI is NOT
// given --worktree. Any failure degrades to the project root instead of
// failing the session. The result is remembered in s.workDir for the
// per-turn Hermes launches.
func (s *Session) prepareWorkDir(ctx context.Context) (dir, slot string) {
	dir = s.ProjectPath
	if s.Config.UseWorktree && s.ProjectPath != "" {
		slot = worktree.SlotName(s.Config)
		path, created, err := s.ensureSlot(ctx, slot)
		if err != nil {
			s.emitManagerLog(fmt.Sprintf("Рабочая папка не готова: %v. Сессия запущена в корне проекта.", err))
			logger.L.Warn("session.slot_failed", "id", s.ID, "slot", slot, "error", err)
			slot = ""
		} else {
			dir = path
			branch := gitutil.CurrentBranch(ctx, path)
			if branch == "" {
				branch = "detached"
			}
			msg := fmt.Sprintf("Рабочая папка: .claude/worktrees/%s (ветка %s)", slot, branch)
			if created {
				msg += fmt.Sprintf(", создана %s", time.Now().Format("15:04:05"))
			}
			s.emitManagerLog(msg)
		}
	}
	s.mu.Lock()
	prev := s.workDir
	if prev == "" {
		prev = s.prevWorkDir
	}
	s.mu.Unlock()
	note := s.workDirSwitchNote(ctx, prev, dir)
	s.mu.Lock()
	s.workDir = dir
	s.pendingWorkDirNote = note
	s.mu.Unlock()
	if s.crashRecovery && s.stateStore != nil {
		s.stateStore.UpdateWorkDir(s.ProjectName, s.Config.Name, dir)
	}
	return dir, slot
}

// workDirSwitchNote makes a use_worktree change actually move the work, not
// just the folder the CLI starts in. A resumed conversation remembers the old
// folder and would cd back into it, and a slot left on the task branch keeps
// that branch from being checked out in the project root. So: with
// use_worktree off, this session's slots are released (their branch freed,
// see worktree.Release), and the returned note — put in front of the next
// prompt — names the folder to work in and what is left to move. "" when
// nothing changed.
func (s *Session) workDirSwitchNote(ctx context.Context, prev, dir string) string {
	var notes []string
	if prev != "" && !samePath(prev, dir) {
		notes = append(notes, fmt.Sprintf("Earlier in this conversation you worked in %s; that folder is no longer yours — do not cd into it.", prev))
	}
	if !s.Config.UseWorktree && s.ProjectPath != "" {
		for _, path := range s.ownSlotPaths(prev) {
			branch, dirty, err := worktree.Release(ctx, path)
			rel := relSlot(s.ProjectPath, path)
			switch {
			case err != nil:
				logger.L.Warn("session.slot_release_failed", "id", s.ID, "slot", path, "error", err)
				s.emitManagerLog(fmt.Sprintf("Не удалось освободить ветку %s в %s: %v", branch, rel, err))
				notes = append(notes, fmt.Sprintf("Branch %s is still checked out in %s; release it there with `git -C %s switch --detach` before checking it out in %s.", branch, path, path, dir))
			case dirty:
				s.emitManagerLog(fmt.Sprintf("Worktree выключен, но в %s ветка %s с незакоммиченными правками — сессии сказано перенести их", rel, branch))
				notes = append(notes, fmt.Sprintf("%s still holds branch %s with uncommitted changes: commit them there, release the branch with `git -C %s switch --detach`, then check %s out in %s and continue there.", path, branch, path, branch, dir))
			case branch != "":
				s.emitManagerLog(fmt.Sprintf("Worktree выключен: ветка %s освобождена из %s, работа продолжается в корне проекта", branch, rel))
				notes = append(notes, fmt.Sprintf("Branch %s was released from %s (now detached). If it is your task branch, check it out in %s and continue there.", branch, path, dir))
			}
		}
	}
	if len(notes) == 0 {
		return ""
	}
	setting := "off"
	if s.Config.UseWorktree {
		setting = "on"
	}
	return fmt.Sprintf("Work folder for this run: %s (the manager's use_worktree setting is %s). Read, build, test and commit only there. ", dir, setting) +
		strings.Join(notes, " ")
}

// ownSlotPaths lists the pool slots this session may have left work in: its
// slot by the current naming, the name-based slot (an earlier task_source may
// have named it), and the previous run's folder when that was a slot.
func (s *Session) ownSlotPaths(prev string) []string {
	byName := s.Config
	byName.TaskSource = ""
	cands := []string{
		worktree.SlotPath(s.ProjectPath, worktree.SlotName(s.Config)),
		worktree.SlotPath(s.ProjectPath, worktree.SlotName(byName)),
	}
	if prev != "" && isUnder(prev, filepath.Join(s.ProjectPath, ".claude", "worktrees")) {
		cands = append(cands, prev)
	}
	var out []string
	for _, c := range cands {
		dup := false
		for _, o := range out {
			dup = dup || samePath(o, c)
		}
		if !dup {
			out = append(out, c)
		}
	}
	return out
}

func samePath(a, b string) bool {
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

func isUnder(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && !strings.HasPrefix(rel, "..")
}

func relSlot(project, path string) string {
	if rel, err := filepath.Rel(project, path); err == nil {
		return filepath.ToSlash(rel)
	}
	return path
}

func (s *Session) ensureSlot(ctx context.Context, slot string) (string, bool, error) {
	if err := gitutil.EnsureRepoWithCommit(ctx, s.ProjectPath); err != nil {
		return "", false, err
	}
	return worktree.EnsureSlot(ctx, s.ProjectPath, slot, gitutil.MainBranch(ctx, s.ProjectPath), s.emitManagerLog)
}

// runDir is the directory prepareWorkDir chose for this run.
func (s *Session) runDir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.workDir != "" {
		return s.workDir
	}
	return s.ProjectPath
}

// startWatch narrates the gap between cmd.Start() and the CLI's init event,
// which can last minutes with no output. stop must be called when the process
// ends (idempotent); gotInit when the init event arrives.
type startWatch struct {
	s       *Session
	started time.Time
	done    chan struct{}
	once    chan struct{} // closed on first stop/init
}

func (s *Session) newStartWatch(pid int) *startWatch {
	w := &startWatch{s: s, started: time.Now(), done: make(chan struct{}), once: make(chan struct{}, 1)}
	w.once <- struct{}{}
	s.emitManagerLog(fmt.Sprintf("CLI запущен (pid %d), жду инициализации…", pid))
	go func() {
		defer logger.Recover("session.start_watch", "id", s.ID)
		t := time.NewTimer(initSlowFirst)
		defer t.Stop()
		for {
			select {
			case <-w.done:
				return
			case <-t.C:
				s.emitManagerLog(fmt.Sprintf("CLI ещё инициализируется (%d с)…", int(time.Since(w.started).Round(time.Second)/time.Second)))
				t.Reset(initSlowEvery)
			}
		}
	}()
	return w
}

func (w *startWatch) finish() bool {
	select {
	case <-w.once:
		close(w.done)
		return true
	default:
		return false
	}
}

// gotInit stops the watcher and reports how long init took when it is long
// enough to matter.
func (w *startWatch) gotInit() {
	if !w.finish() {
		return
	}
	if d := time.Since(w.started); d >= initQuiet {
		w.s.emitManagerLog(fmt.Sprintf("CLI готов за %.0f с", d.Seconds()))
	}
}

func (w *startWatch) stop() { w.finish() }

func (s *Session) beginStartWatch(pid int) {
	w := s.newStartWatch(pid)
	s.mu.Lock()
	s.startW = w
	s.mu.Unlock()
}

func (s *Session) endStartWatch() {
	s.mu.Lock()
	w := s.startW
	s.startW = nil
	s.mu.Unlock()
	if w != nil {
		w.stop()
	}
}
