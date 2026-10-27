package logship

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newBare(t *testing.T) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "remote.git")
	run(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", bare)
	return bare
}

// writeLog creates a run log saved at t and returns its file name.
func writeLog(t *testing.T, project string, at time.Time) string {
	t.Helper()
	dir := filepath.Join(project, ".claude-manager", "logs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	name := "p_s1-" + at.Format(stampLayout) + ".md"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("log "+name), 0o644); err != nil {
		t.Fatal(err)
	}
	return name
}

type env struct {
	bare, data, proj string
	now              time.Time
}

func newEnv(t *testing.T) *env {
	return &env{bare: newBare(t), data: t.TempDir(), proj: t.TempDir(), now: time.Date(2026, 10, 27, 23, 0, 0, 0, time.Local)}
}

func (e *env) ship(t *testing.T) (Result, error) {
	return Ship(context.Background(), Options{
		Repo: e.bare, Host: "pc1", Build: "abc123", DataDir: e.data,
		Projects: []Project{{Name: "proj", Path: e.proj}, {Name: "missing", Path: filepath.Join(e.proj, "nope")}},
		Now:      func() time.Time { return e.now },
	})
}

func (e *env) remoteFiles(t *testing.T) string {
	var out []string
	for _, l := range strings.Split(run(t, e.bare, "ls-tree", "-r", "--name-only", "main"), "\n") {
		if !strings.HasSuffix(l, "machine.md") {
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}

func (e *env) commits(t *testing.T) string {
	return run(t, e.bare, "rev-list", "--count", "main")
}

func TestShipFirstPassAndNoop(t *testing.T) {
	e := newEnv(t)
	name := writeLog(t, e.proj, e.now.Add(-time.Hour))
	res, err := e.ship(t)
	if err != nil || !res.Pushed || res.Files != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	want := "logs/pc1/proj/2026-10/" + name
	if got := e.remoteFiles(t); got != want {
		t.Fatalf("remote=%q want %q", got, want)
	}
	if msg := run(t, e.bare, "log", "-1", "--format=%s", "main"); msg != "pc1: 1 logs, build abc123" {
		t.Fatalf("msg=%q", msg)
	}
	res, err = e.ship(t)
	if err != nil || res.Pushed || res.Files != 0 {
		t.Fatalf("second res=%+v err=%v", res, err)
	}
	if n := e.commits(t); n != "1" {
		t.Fatalf("commits=%s", n)
	}
}

func TestShipSkipsYoungAndTmp(t *testing.T) {
	e := newEnv(t)
	young := writeLog(t, e.proj, e.now.Add(-time.Minute))
	tmp := filepath.Join(e.proj, ".claude-manager", "logs", "x-"+e.now.Add(-time.Hour).Format(stampLayout)+".md.tmp")
	if err := os.WriteFile(tmp, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := e.ship(t)
	if err != nil || res.Files != 0 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	e.now = e.now.Add(5 * time.Minute)
	res, err = e.ship(t)
	if err != nil || !res.Pushed || res.Files != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := e.remoteFiles(t); !strings.HasSuffix(got, young) || strings.Contains(got, ".tmp") {
		t.Fatalf("remote=%q", got)
	}
}

func TestShipFirstWindowSevenDays(t *testing.T) {
	e := newEnv(t)
	writeLog(t, e.proj, e.now.Add(-8*24*time.Hour))
	fresh := writeLog(t, e.proj, e.now.Add(-6*24*time.Hour))
	if _, err := e.ship(t); err != nil {
		t.Fatal(err)
	}
	if got := e.remoteFiles(t); !strings.HasSuffix(got, fresh) || strings.Count(got, "\n") != 0 {
		t.Fatalf("remote=%q", got)
	}
}

func TestShipKeepsOtherHostFiles(t *testing.T) {
	e := newEnv(t)
	writeLog(t, e.proj, e.now.Add(-time.Hour))
	if _, err := e.ship(t); err != nil {
		t.Fatal(err)
	}
	// Another computer pushes while ours already has a stale clone.
	other := filepath.Join(t.TempDir(), "other")
	run(t, filepath.Dir(other), "clone", "-q", e.bare, other)
	if err := os.MkdirAll(filepath.Join(other, "logs", "pc2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "logs", "pc2", "a.md"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, other, "add", "-A")
	run(t, other, "commit", "-q", "-m", "pc2")
	run(t, other, "push", "-q", "origin", "HEAD:main")

	writeLog(t, e.proj, e.now.Add(-30*time.Minute))
	res, err := e.ship(t)
	if err != nil || !res.Pushed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	got := e.remoteFiles(t)
	if !strings.Contains(got, "logs/pc2/a.md") || strings.Count(got, "logs/pc1/") != 2 {
		t.Fatalf("remote=%q", got)
	}
}

func TestShipRecreatedRemoteOnlyNewerThanMark(t *testing.T) {
	e := newEnv(t)
	writeLog(t, e.proj, e.now.Add(-2*time.Hour))
	if _, err := e.ship(t); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(e.bare); err != nil {
		t.Fatal(err)
	}
	run(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", e.bare)
	newer := writeLog(t, e.proj, e.now.Add(-time.Hour))
	res, err := e.ship(t)
	if err != nil || !res.Pushed || res.Files != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if got := e.remoteFiles(t); !strings.HasSuffix(got, newer) || strings.Count(got, "\n") != 0 {
		t.Fatalf("remote=%q", got)
	}
}

func TestShipRemoteDownKeepsMarks(t *testing.T) {
	e := newEnv(t)
	writeLog(t, e.proj, e.now.Add(-time.Hour))
	good := e.bare
	e.bare = filepath.Join(t.TempDir(), "does-not-exist.git")
	res, err := e.ship(t)
	if err == nil || res.Error == "" || res.Pushed {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	if _, serr := os.Stat(filepath.Join(e.data, stateFile)); serr == nil {
		t.Fatal("marks written after a failed pass")
	}
	e.bare = good
	res, err = e.ship(t)
	if err != nil || !res.Pushed || res.Files != 1 {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}
