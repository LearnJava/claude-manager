package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"claude-manager/internal/config"
)

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newRepo(t *testing.T) string {
	t.Helper()
	d := t.TempDir()
	git(t, d, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(d, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, d, "add", "-A")
	git(t, d, "commit", "-m", "init")
	return d
}

func TestSlotName(t *testing.T) {
	re := regexp.MustCompile(`^[a-z0-9-]+$`)
	cases := []struct {
		cfg  config.SessionConfig
		want string
	}{
		{config.SessionConfig{Name: "X", WorktreeName: "  mine "}, "mine"},
		{config.SessionConfig{Name: "X", TaskSource: "STATUS-P3.md"}, "p3-work"},
		{config.SessionConfig{Name: "X", TaskSource: "status-p12.MD"}, "p12-work"},
		{config.SessionConfig{Name: "Chat"}, "chat-work"},
		{config.SessionConfig{Name: "Developer 1"}, "developer-1-work"},
		{config.SessionConfig{Name: " Мой  --Seс "}, "se-work"},
		{config.SessionConfig{Name: "Dev", TaskSource: "OTHER.md"}, "dev-work"},
	}
	for _, c := range cases {
		got := SlotName(c.cfg)
		if got != c.want {
			t.Errorf("SlotName(%+v) = %q, want %q", c.cfg, got, c.want)
		}
		if !re.MatchString(got) {
			t.Errorf("%q fails slot pattern", got)
		}
	}
}

func countExclude(t *testing.T, d string) int {
	t.Helper()
	ex, _ := os.ReadFile(filepath.Join(d, ".git", "info", "exclude"))
	return strings.Count(string(ex), "/.claude/worktrees/")
}

func TestEnsureSlotCreateAndReuse(t *testing.T) {
	d := newRepo(t)
	var msgs []string
	p, created, err := EnsureSlot(context.Background(), d, "p1-work", "main", func(s string) { msgs = append(msgs, s) })
	if err != nil || !created {
		t.Fatalf("create: %v created=%v", err, created)
	}
	if p != SlotPath(d, "p1-work") || len(msgs) != 1 {
		t.Fatalf("path=%s msgs=%v", p, msgs)
	}
	// the slot moves to another branch; a repeat call must leave it there
	git(t, p, "checkout", "-b", "feature")
	_, created, err = EnsureSlot(context.Background(), d, "p1-work", "main", nil)
	if err != nil || created {
		t.Fatalf("reuse: %v created=%v", err, created)
	}
	if b := git(t, p, "rev-parse", "--abbrev-ref", "HEAD"); b != "feature" {
		t.Fatalf("branch = %s", b)
	}
	if n := countExclude(t, d); n != 1 {
		t.Fatalf("exclude entries = %d", n)
	}
	if _, _, err := EnsureSlot(context.Background(), d, "p2-work", "main", nil); err != nil {
		t.Fatal(err)
	}
	if n := countExclude(t, d); n != 1 {
		t.Fatalf("exclude entries after 2nd slot = %d", n)
	}
}

func interrupt(t *testing.T, p string) string {
	t.Helper()
	gd := git(t, p, "rev-parse", "--absolute-git-dir")
	_ = os.Remove(filepath.Join(gd, "index"))
	lock := filepath.Join(gd, "index.lock")
	if err := os.WriteFile(lock, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return lock
}

func TestEnsureSlotRepairsInterruptedCheckout(t *testing.T) {
	d := newRepo(t)
	p, _, err := EnsureSlot(context.Background(), d, "s", "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	lock := interrupt(t, p)
	var msgs []string
	if _, created, err := EnsureSlot(context.Background(), d, "s", "main", func(s string) { msgs = append(msgs, s) }); err != nil || created {
		t.Fatalf("repair: %v created=%v", err, created)
	}
	if _, err := os.Stat(lock); err == nil {
		t.Fatal("index.lock still there")
	}
	if len(msgs) != 1 {
		t.Fatalf("msgs=%v", msgs)
	}
	if _, err := os.Stat(filepath.Join(p, "a.txt")); err != nil {
		t.Fatal("checkout not restored")
	}
}

func TestEnsureSlotInterruptedWithOwnCommitsErrors(t *testing.T) {
	d := newRepo(t)
	p, _, err := EnsureSlot(context.Background(), d, "s", "main", nil)
	if err != nil {
		t.Fatal(err)
	}
	git(t, p, "checkout", "-b", "work")
	if err := os.WriteFile(filepath.Join(p, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, p, "add", "-A")
	git(t, p, "commit", "-m", "own")
	lock := interrupt(t, p)
	if _, _, err := EnsureSlot(context.Background(), d, "s", "main", nil); err == nil {
		t.Fatal("expected error")
	}
	if _, err := os.Stat(lock); err != nil {
		t.Fatal("index.lock was removed")
	}
	if _, err := os.Stat(filepath.Join(p, "b.txt")); err != nil {
		t.Fatal("file gone")
	}
}

func TestEnsureSlotNonWorktreeDir(t *testing.T) {
	d := newRepo(t)
	p := SlotPath(d, "s")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(p, "keep.txt")
	if err := os.WriteFile(keep, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := EnsureSlot(context.Background(), d, "s", "main", nil)
	if err == nil || !strings.Contains(err.Error(), "orphaned") {
		t.Fatalf("err = %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("folder was touched")
	}
}
