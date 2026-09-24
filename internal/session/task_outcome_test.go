package session

import (
	"context"
	"database/sql"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestClassifyTaskOutcome(t *testing.T) {
	cases := []struct {
		name          string
		before, after string
		marker        bool
		want          string
	}{
		{"pointer left the queue", "BUGS.md:291", "ROADMAP.md:331", false, OutcomeClosed},
		{"queue emptied", "BUGS.md:291", "", true, OutcomeClosed},
		{"same pointer, marker: a slice", "ROADMAP.md:331", "ROADMAP.md:331", true, OutcomeSlice},
		{"same pointer, no marker: unfinished", "ROADMAP.md:331", "ROADMAP.md:331", false, OutcomeUnfinished},
		// No queue to consult keeps the pre-existing behaviour: every clean
		// exit counts as a done task.
		{"no queue, no marker", "", "", false, OutcomeClosed},
		{"no queue, marker", "", "", true, OutcomeClosed},
	}
	for _, c := range cases {
		if got := classifyTaskOutcome(c.before, c.after, c.marker); got != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// The queue is read off origin/<main>, where a developer session lands it
// from its worktree slot — not off the root checkout, which may lag behind.
func TestReadTaskQueue_PrefersIntegrationBranch(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	base := t.TempDir()
	origin := filepath.Join(base, "origin.git")
	root := filepath.Join(base, "root")
	other := filepath.Join(base, "other")
	git(t, base, "init", "-q", "--bare", "-b", "main", origin)
	git(t, base, "clone", "-q", origin, root)
	os.WriteFile(filepath.Join(root, "STATUS-P6.md"), []byte("# queue\nBUGS.md:291\nROADMAP.md:331\n"), 0o644)
	git(t, root, "add", ".")
	git(t, root, "commit", "-q", "-m", "queue")
	git(t, root, "push", "-q", "origin", "HEAD:main")

	// Another clone closes BUGS.md:291 and pushes; root's file is stale.
	git(t, base, "clone", "-q", origin, other)
	os.WriteFile(filepath.Join(other, "STATUS-P6.md"), []byte("# queue\nROADMAP.md:331\n"), 0o644)
	git(t, other, "commit", "-q", "-am", "close")
	git(t, other, "push", "-q", "origin", "HEAD:main")

	if got := firstTaskPointer(readTaskQueue(context.Background(), root, "STATUS-P6.md")); got != "ROADMAP.md:331" {
		t.Fatalf("pointer = %q, want ROADMAP.md:331 (from origin/main, not the stale checkout)", got)
	}

	// No remote: falls back to the file on disk.
	plain := t.TempDir()
	os.WriteFile(filepath.Join(plain, "Q.md"), []byte("A.md:1\n"), 0o644)
	if got := firstTaskPointer(readTaskQueue(context.Background(), plain, "Q.md")); got != "A.md:1" {
		t.Fatalf("fallback pointer = %q, want A.md:1", got)
	}
}

func TestHermesTurnHitStepLimit(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "state.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT, role TEXT, content TEXT)`); err != nil {
		t.Fatal(err)
	}
	ins := func(conv, role, content string) {
		if _, err := db.Exec(`INSERT INTO messages (session_id, role, content) VALUES (?,?,?)`, conv, role, content); err != nil {
			t.Fatal(err)
		}
	}
	// Cut off: the fixed request, then the model's summary.
	ins("cut", "user", "do PERF-14")
	ins("cut", "assistant", "")
	ins("cut", "tool", "ok")
	ins("cut", "user", hermesMaxIterationsRequest+" Please provide a final response summarizing…")
	ins("cut", "assistant", "PERF-14 не закончен")
	// Measured on the real cut-off run: a background job finished after the
	// summary, and Hermes injected its notice as a user message.
	ins("cut", "user", hermesBackgroundNotice+"proc_e3588a4180a5 completed normally (exit code 0).")
	ins("cut", "assistant", "Прогон DOM-тестов ничего не проверил")
	// Finished normally.
	ins("done", "user", "do BUG-1118")
	ins("done", "tool", "ok")
	ins("done", "assistant", "BUG-1118 закрыт")
	// Hit the limit on an earlier turn, finished the later one: not cut off.
	ins("later", "user", hermesMaxIterationsRequest)
	ins("later", "assistant", "summary")
	ins("later", "user", "continue")
	ins("later", "assistant", "done")
	db.Close()

	for conv, want := range map[string]bool{"cut": true, "done": false, "later": false, "missing": false} {
		if got := hermesTurnHitStepLimit(dbPath, conv); got != want {
			t.Errorf("%s: got %v, want %v", conv, got, want)
		}
	}
	if hermesTurnHitStepLimit(filepath.Join(t.TempDir(), "nope.db"), "cut") {
		t.Error("missing state.db must report false")
	}
}

func TestHermesHome(t *testing.T) {
	t.Setenv("HERMES_HOME", `C:\h`)
	if got := hermesHome(""); got != `C:\h` {
		t.Errorf("hermesHome() = %q", got)
	}
	if got := hermesHome("cm-lumen"); got != filepath.Join(`C:\h`, "profiles", "cm-lumen") {
		t.Errorf("hermesHome(cm-lumen) = %q", got)
	}
	t.Setenv("HERMES_HOME", "")
	t.Setenv("LOCALAPPDATA", `C:\la`)
	if got := hermesHome("default"); got != filepath.Join(`C:\la`, "hermes") {
		t.Errorf("hermesHome(default) = %q", got)
	}
}

func TestHandleResult_TagsNonSuccessSubtype(t *testing.T) {
	ev := parseLineAt(`{"type":"result","subtype":"error_max_turns","result":"stopped"}`, testTime)
	if ev.Entries[0].Message != "[error_max_turns] stopped" {
		t.Errorf("message = %q", ev.Entries[0].Message)
	}
	if ev.Result.Subtype != "error_max_turns" {
		t.Errorf("subtype = %q", ev.Result.Subtype)
	}
	ok := parseLineAt(`{"type":"result","subtype":"success","result":"done"}`, testTime)
	if ok.Entries[0].Message != "done" {
		t.Errorf("success message must stay untagged, got %q", ok.Entries[0].Message)
	}
}

// Lines copied from the real agent.log of the S6 incident: the 429 is only
// in the per-conversation lines, the turn itself reported a 401.
func TestHermesTurnHitRateLimit(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "agent.log")
	content := "" +
		"2026-09-24 13:51:20,000 INFO [20260924_135123_66a883] run_agent: turn started\n" +
		"2026-09-24 15:48:25,826 INFO [20260924_135123_66a883] run_agent: Credential 429 (rate limit) — rotated to pool entry 31d239\n" +
		"2026-09-24 15:48:26,841 WARNING [20260924_135123_66a883] agent.conversation_loop: API call failed (attempt 2/3) error_type=RateLimitError\n" +
		"2026-09-24 15:50:03,088 ERROR [20260924_135123_66a883] agent.conversation_loop: Non-retryable client error: Error code: 401\n" +
		"2026-09-24 15:48:26,000 WARNING [20260924_102935_377658] agent.conversation_loop: error_type=RateLimitError\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	at := func(s string) time.Time {
		v, err := time.ParseInLocation("2006-01-02 15:04:05", s, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if !hermesTurnHitRateLimit(logPath, "20260924_135123_66a883", at("2026-09-24 13:51:19")) {
		t.Error("429 of this conversation during this turn not found")
	}
	if hermesTurnHitRateLimit(logPath, "20260924_135123_66a883", at("2026-09-24 15:49:00")) {
		t.Error("a 429 from before this turn started must not count")
	}
	if hermesTurnHitRateLimit(logPath, "20260924_000000_other", at("2026-09-24 13:00:00")) {
		t.Error("another conversation's 429 must not count")
	}
	if hermesTurnHitRateLimit(filepath.Join(t.TempDir(), "none.log"), "x", time.Now()) {
		t.Error("missing log must report false")
	}
}
