package suggest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var testNow = time.Now()

func gitEnv(t *testing.T, dir string, when time.Time, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Dir = dir
	d := when.Format(time.RFC3339)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+d, "GIT_COMMITTER_DATE="+d)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir string, daysAgo int, msg string) string {
	t.Helper()
	when := testNow.Add(-time.Duration(daysAgo) * 24 * time.Hour)
	gitEnv(t, dir, when, "add", "-A")
	gitEnv(t, dir, when, "commit", "-q", "-m", msg)
	return gitEnv(t, dir, when, "rev-parse", "HEAD")
}

func hasKind(list []Suggestion, k Kind) *Suggestion {
	for i := range list {
		if list[i].Kind == k {
			return &list[i]
		}
	}
	return nil
}

func count(list []Suggestion, k Kind) int {
	n := 0
	for _, s := range list {
		if s.Kind == k {
			n++
		}
	}
	return n
}

// prepared builds a repository with one case of every kind.
func prepared(t *testing.T) (root, head string) {
	t.Helper()
	root = t.TempDir()
	gitEnv(t, root, testNow, "init", "-q", "-b", "master")
	// A stale TODO (200 days) and a fresh one.
	write(t, root, "old.go", "package x\n\n// TODO: handle the empty case\nfunc a() {}\n")
	write(t, root, "docs/arch.md", "arch v1\n")
	write(t, root, "app.go", "package main\n")
	write(t, root, "TASKS.md", "# Tasks\n\n## T-1: first\nbody\n")
	write(t, root, "STATUS.md", "TASKS.md:3\nTASKS.md:4\nGONE.md:2\n")
	commit(t, root, 200, "init")
	// Documentation touched 100 days ago, code 50 days ago: drift.
	write(t, root, "docs/arch.md", "arch v2\n")
	commit(t, root, 100, "docs")
	write(t, root, "app.go", "package main\n\nfunc New() {}\n")
	commit(t, root, 50, "feat: new binding")
	write(t, root, "fresh.go", "package x\n\n// TODO: later\n")
	head = commit(t, root, 1, "fresh todo")
	// An unmerged branch whose last commit is 30 days old.
	gitEnv(t, root, testNow, "checkout", "-q", "-b", "p1-old")
	write(t, root, "wip.go", "package x\n")
	commit(t, root, 30, "wip")
	gitEnv(t, root, testNow, "checkout", "-q", "master")
	// A linked worktree with an old uncommitted change.
	wt := filepath.Join(t.TempDir(), "wt")
	gitEnv(t, root, testNow, "worktree", "add", "-q", "-b", "p2-slot", wt)
	write(t, wt, "scratch.txt", "left over")
	old := testNow.Add(-20 * 24 * time.Hour)
	if err := os.Chtimes(filepath.Join(wt, "scratch.txt"), old, old); err != nil {
		t.Fatal(err)
	}
	return root, head
}

func opts(root, head string) Options {
	return Options{
		Root:       root,
		QueueFiles: []string{"STATUS.md"},
		DocSync:    []string{"app.go => docs/arch.md", "nonexistent.go => docs/arch.md"},
		LastGate:   &GateRun{Passed: false, Head: head, Summary: "go test ./...\nFAIL x", At: testNow},
		Now:        testNow,
	}
}

func TestScanFindsEveryKind(t *testing.T) {
	root, head := prepared(t)
	got, err := Scan(context.Background(), opts(root, head))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range Kinds {
		if hasKind(got, k) == nil {
			t.Errorf("kind %s not found in %+v", k, got)
		}
	}
	// Only the 200-day TODO is stale, not the fresh one.
	if n := count(got, KindTodo); n != 1 {
		t.Errorf("stale todos = %d, want 1", n)
	} else if s := hasKind(got, KindTodo); !strings.Contains(s.Detail, "old.go:3") {
		t.Errorf("todo detail = %q", s.Detail)
	}
	// Pointers: line 4 of TASKS.md is not a heading and GONE.md is missing; line 3 is fine.
	if n := count(got, KindPointer); n != 2 {
		t.Errorf("broken pointers = %d, want 2: %+v", n, got)
	}
	if s := hasKind(got, KindDocSync); s == nil || !strings.Contains(s.Detail, "feat: new binding") {
		t.Errorf("doc drift = %+v", s)
	}
	if n := count(got, KindDocSync); n != 1 {
		t.Errorf("doc drift count = %d (a rule on a path with no commits must be silent)", n)
	}
	if s := hasKind(got, KindBranch); s == nil || !strings.Contains(s.Title, "p1-old") {
		t.Errorf("branch = %+v", s)
	}
	if n := count(got, KindBranch); n != 1 {
		t.Errorf("branches = %d, want 1 (p2-slot is merged/at master)", n)
	}
	if s := hasKind(got, KindFolder); s == nil || !strings.Contains(s.Detail, "scratch") && !strings.Contains(s.Title, "wt") {
		t.Errorf("folder = %+v", s)
	}
}

func TestScanFreshWorktreeIsNotAbandoned(t *testing.T) {
	root, head := prepared(t)
	o := opts(root, head)
	// "Now" a day after the scratch file's mtime shifted: make the file fresh.
	wts, _ := git(context.Background(), root, "worktree", "list", "--porcelain")
	for _, l := range lines(wts) {
		if p, ok := strings.CutPrefix(l, "worktree "); ok && strings.HasSuffix(filepath.ToSlash(p), "/wt") {
			now := time.Now()
			_ = os.Chtimes(filepath.Join(p, "scratch.txt"), now, now)
		}
	}
	got, _ := Scan(context.Background(), o)
	if n := count(got, KindFolder); n != 0 {
		t.Errorf("a folder with fresh changes was suggested: %+v", got)
	}
}

func TestScanGateRules(t *testing.T) {
	root := t.TempDir()
	gitEnv(t, root, testNow, "init", "-q", "-b", "master")
	write(t, root, "a.go", "package x\n")
	head := commit(t, root, 1, "init")
	ctx := context.Background()
	red := func() Options {
		return Options{Root: root, Now: testNow, LastGate: &GateRun{Head: head, Summary: "FAIL", At: testNow}}
	}

	if got, _ := Scan(ctx, red()); count(got, KindGates) != 1 {
		t.Errorf("red gates on the branch tip = %+v", got)
	}
	o := red()
	o.LastGate.Passed = true
	if got, _ := Scan(ctx, o); count(got, KindGates) != 0 {
		t.Error("green gates were suggested")
	}
	o = red()
	o.LastGate = nil
	if got, _ := Scan(ctx, o); count(got, KindGates) != 0 {
		t.Error("no gate record must give no suggestion")
	}
	o = red()
	o.LastGate.Head = ""
	if got, _ := Scan(ctx, o); count(got, KindGates) != 1 {
		t.Error("red gates with an unknown commit must be suggested")
	}

	// A commit on top of the failing one: it may have fixed things.
	write(t, root, "fix.go", "package x\n")
	commit(t, root, 0, "fix")
	if got, _ := Scan(ctx, red()); count(got, KindGates) != 0 {
		t.Error("red gates suggested although a commit was added since")
	}
}

func TestScanCleanRepoIsEmptyNotError(t *testing.T) {
	root := t.TempDir()
	gitEnv(t, root, testNow, "init", "-q", "-b", "master")
	write(t, root, "main.go", "package main\n")
	write(t, root, "STATUS.md", "")
	commit(t, root, 1, "init")
	got, err := Scan(context.Background(), Options{Root: root, QueueFiles: []string{"STATUS.md", "MISSING.md"},
		DocSync: []string{"main.go => README.md"}, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Errorf("clean repo = %#v, want an empty non-nil list", got)
	}
}

func TestScanNotARepository(t *testing.T) {
	root := t.TempDir()
	write(t, root, "STATUS.md", "NOPE.md:1\n")
	got, err := Scan(context.Background(), Options{Root: root, QueueFiles: []string{"STATUS.md"},
		DocSync: []string{"a => b"}, Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Kind != KindPointer {
		t.Errorf("non-git folder = %+v, want only the broken pointer", got)
	}
}

func TestAcceptWritesTasksAndQueue(t *testing.T) {
	root, head := prepared(t)
	ctx := context.Background()
	o := opts(root, head)
	got, _ := Scan(ctx, o)
	if len(got) < 5 {
		t.Fatalf("scan = %d suggestions", len(got))
	}

	// Nothing is written until Accept is called.
	if _, err := os.Stat(filepath.Join(root, TaskFile)); err == nil {
		t.Fatal("scan wrote the task file")
	}
	queueBefore, _ := os.ReadFile(filepath.Join(root, "STATUS.md"))

	picked := []Suggestion{got[0], got[1]}
	ptrs, err := Accept(root, "STATUS.md", picked)
	if err != nil {
		t.Fatal(err)
	}
	if len(ptrs) != 2 {
		t.Fatalf("pointers = %v", ptrs)
	}
	// Every pointer lands on its section's heading.
	taskLines := strings.Split(readFile(t, root, TaskFile), "\n")
	for i, p := range ptrs {
		n, err := strconv.Atoi(p[strings.LastIndex(p, ":")+1:])
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(taskLines[n-1], "## SG-") || !strings.Contains(taskLines[n-1], picked[i].Title) {
			t.Errorf("pointer %s -> %q", p, taskLines[n-1])
		}
		if !strings.Contains(strings.Join(taskLines, "\n"), "<!-- suggest:"+picked[i].Key+" -->") {
			t.Errorf("key of %s missing", picked[i].Key)
		}
	}
	q := readFile(t, root, "STATUS.md")
	if !strings.HasPrefix(q, string(queueBefore)) || !strings.HasSuffix(q, strings.Join(ptrs, "\n")+"\n") {
		t.Errorf("queue = %q", q)
	}

	// A rescan does not offer the accepted ones again, and the new pointers are valid.
	again, _ := Scan(ctx, o)
	if len(again) != len(got)-2 {
		t.Errorf("rescan = %d, want %d", len(again), len(got)-2)
	}
	for _, s := range again {
		if s.Key == picked[0].Key || s.Key == picked[1].Key {
			t.Errorf("accepted suggestion offered again: %+v", s)
		}
	}
	if count(again, KindPointer) != count(got, KindPointer) && picked[0].Kind != KindPointer && picked[1].Kind != KindPointer {
		t.Error("accepting added a broken pointer")
	}

	// Accepting the same suggestion twice appends nothing.
	if p2, err := Accept(root, "STATUS.md", picked[:1]); err != nil || len(p2) != 0 {
		t.Errorf("second accept = %v, %v", p2, err)
	}
	// IDs continue.
	ptrs3, err := Accept(root, "STATUS.md", []Suggestion{got[2]})
	if err != nil || len(ptrs3) != 1 {
		t.Fatalf("third accept = %v, %v", ptrs3, err)
	}
	if !strings.Contains(readFile(t, root, TaskFile), "## SG-03:") {
		t.Error("ids do not continue")
	}
}

func TestAcceptRejectsBadQueueAndEmpty(t *testing.T) {
	root := t.TempDir()
	s := []Suggestion{{Kind: KindTodo, Key: "stale_todo:aa", Title: "t", Detail: "d", Done: "x"}}
	for _, bad := range []string{"", "../x.md", "/etc/passwd", `a\b.md`, "C:/x.md"} {
		if _, err := Accept(root, bad, s); err == nil {
			t.Errorf("queue %q accepted", bad)
		}
	}
	if p, err := Accept(root, "STATUS.md", nil); err != nil || p != nil {
		t.Errorf("empty accept = %v, %v", p, err)
	}
	if _, err := os.Stat(filepath.Join(root, TaskFile)); err == nil {
		t.Error("task file created without anything picked")
	}
	// A queue that does not exist yet is created.
	if p, err := Accept(root, "STATUS-P9.md", s); err != nil || len(p) != 1 {
		t.Errorf("new queue = %v, %v", p, err)
	}
}

func readFile(t *testing.T, root, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
