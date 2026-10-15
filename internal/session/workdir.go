package session

import (
	"context"
	"fmt"
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
	s.workDir = dir
	s.mu.Unlock()
	return dir, slot
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
