package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/worktree"
)

func initTempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"},
		{"-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	return dir
}

func runSlotSession(t *testing.T, project string) []string {
	t.Helper()
	claudePath := buildFakeclaudeForSession(t)
	scenarioDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "scenarios"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKECLAUDE_SCENARIO", scenarioDir)
	var mu sync.Mutex
	var logs []string
	s := New(Params{
		ID: "it/P1", ProjectName: "it", ProjectPath: project, ClaudePath: claudePath,
		Config: config.SessionConfig{
			Name: "P1", Model: "sonnet", PermissionMode: "bypassPermissions",
			AutoRestart: true, MaxTasks: 1, Prompt: "happy-path", UseWorktree: true,
		},
		OnEvent: func(_ string, ev SessionEvent) {
			if ev.Type == EvtLog && ev.Entry != nil {
				mu.Lock()
				logs = append(logs, ev.Entry.Message)
				mu.Unlock()
			}
		},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s.Run(ctx)
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), logs...)
}

func TestRun_UsesPersistentSlotAndNarratesStart(t *testing.T) {
	project := initTempRepo(t)
	slot := worktree.SlotName(config.SessionConfig{Name: "P1"})

	logs := strings.Join(runSlotSession(t, project), "\n")
	for _, want := range []string{"Готовлю промпт", "Рабочая папка: .claude/worktrees/" + slot, "CLI запущен (pid"} {
		if !strings.Contains(logs, want) {
			t.Errorf("feed lacks %q:\n%s", want, logs)
		}
	}
	if !strings.Contains(logs, "создана") {
		t.Errorf("first start should mention creation:\n%s", logs)
	}
	if _, err := os.Stat(worktree.SlotPath(project, slot)); err != nil {
		t.Fatalf("slot folder missing: %v", err)
	}

	// Second start reuses the slot: no new folders under .claude/worktrees.
	logs = strings.Join(runSlotSession(t, project), "\n")
	if strings.Contains(logs, "создана") {
		t.Errorf("second start must reuse the slot:\n%s", logs)
	}
	entries, _ := os.ReadDir(filepath.Join(project, ".claude", "worktrees"))
	if len(entries) != 1 {
		t.Errorf("worktrees entries = %d, want 1", len(entries))
	}
}

func TestPrepareWorkDir_FailureFallsBackToProjectRoot(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".claude", "worktrees"), 0o755); err != nil {
		t.Fatal(err)
	}
	slot := worktree.SlotName(config.SessionConfig{Name: "P1"})
	// A plain folder that is not a worktree: EnsureSlot must refuse it.
	if err := os.MkdirAll(worktree.SlotPath(project, slot), 0o755); err != nil {
		t.Fatal(err)
	}
	var msgs []string
	s := New(Params{
		ProjectPath: project,
		Config:      config.SessionConfig{Name: "P1", UseWorktree: true},
		OnEvent: func(_ string, ev SessionEvent) {
			if ev.Type == EvtLog && ev.Entry != nil {
				msgs = append(msgs, ev.Entry.Message)
			}
		},
	})
	dir, _ := s.prepareWorkDir(context.Background())
	if dir != project {
		t.Errorf("dir = %q, want project root", dir)
	}
	if !strings.Contains(strings.Join(msgs, "\n"), "Рабочая папка не готова") {
		t.Errorf("no failure notice: %v", msgs)
	}
}

func TestStartWatch_SlowInitLinesStopOnInit(t *testing.T) {
	oldF, oldE, oldQ := initSlowFirst, initSlowEvery, initQuiet
	initSlowFirst, initSlowEvery, initQuiet = 30*time.Millisecond, 30*time.Millisecond, 10*time.Millisecond
	defer func() { initSlowFirst, initSlowEvery, initQuiet = oldF, oldE, oldQ }()

	var mu sync.Mutex
	var msgs []string
	s := New(Params{OnEvent: func(_ string, ev SessionEvent) {
		if ev.Type == EvtLog && ev.Entry != nil {
			mu.Lock()
			msgs = append(msgs, ev.Entry.Message)
			mu.Unlock()
		}
	}})
	w := s.newStartWatch(42)
	time.Sleep(120 * time.Millisecond)
	w.gotInit()
	mu.Lock()
	n := len(msgs)
	mu.Unlock()
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(msgs) != n {
		t.Errorf("watcher kept logging after init: %v", msgs)
	}
	joined := strings.Join(msgs, "\n")
	if !strings.Contains(joined, "pid 42") || !strings.Contains(joined, "ещё инициализируется") || !strings.Contains(joined, "CLI готов за") {
		t.Errorf("missing stage lines:\n%s", joined)
	}
}

func TestSlotConflict(t *testing.T) {
	m := &SessionManager{sessions: map[string]*managedSession{}}
	cfg := config.SessionConfig{Name: "P1", UseWorktree: true, WorktreeName: "shared"}
	other := New(Params{ProjectPath: "x", Config: config.SessionConfig{Name: "P2", UseWorktree: true, WorktreeName: "shared"}})
	other.setStatus(config.StatusWorking)
	m.sessions["it/P2"] = &managedSession{project: "it", name: "P2", session: other}
	err := m.slotConflictLocked("it", "it/P1", &cfg)
	if err == nil || !strings.Contains(err.Error(), "слот shared занят сессией P2") {
		t.Errorf("err = %v", err)
	}
	other.setStatus(config.StatusIdle)
	if err := m.slotConflictLocked("it", "it/P1", &cfg); err != nil {
		t.Errorf("idle holder must not block: %v", err)
	}
}

func TestClassifyStderr_ClaudeConversationNotFound(t *testing.T) {
	f := ClassifyStderrLine("No conversation found with session ID: 1234")
	if f == nil || f.Kind != KindSessionNotFound {
		t.Errorf("got %+v, want KindSessionNotFound", f)
	}
}
