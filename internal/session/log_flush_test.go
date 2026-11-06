package session

import (
	"fmt"
	"testing"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/store"
)

// While a run is in flight, every logFlushBatch entries must already sit in
// SQLite — a crash then loses at most the unflushed tail, not the whole run.
// finishRun must write only the remainder, so the final log has each entry
// exactly once.
func TestIncrementalLogFlushPersistsBeforeRunEnds(t *testing.T) {
	m, st := newTestManagerWithStore(t)
	ms := addStubSession(m, "lumen", "P1")

	run := &store.SessionRun{Project: "lumen", Session: "P1", StartedAt: time.Now(), Status: "running"}
	if err := st.InsertRun(run); err != nil {
		t.Fatal(err)
	}
	ms.mu.Lock()
	ms.runID = run.ID
	ms.mu.Unlock()

	total := logFlushBatch*2 + 5
	for i := 0; i < total; i++ {
		m.onSessionEvent("lumen/P1", SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "text", Message: fmt.Sprintf("e%d", i),
		}})
	}

	// Mid-run: the flushed prefix is durable...
	persisted, err := st.GetLogs(run.ID, 0, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted) < logFlushBatch {
		t.Fatalf("only %d entries in SQLite mid-run, want >= %d flushed", len(persisted), logFlushBatch)
	}
	if persisted[len(persisted)-1].Message != fmt.Sprintf("e%d", len(persisted)-1) {
		t.Fatalf("flushed prefix out of order: last persisted = %q", persisted[len(persisted)-1].Message)
	}
	// ...and the live feed still serves the full log from memory.
	live, err := m.GetSessionLog("lumen/P1", 0, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != total {
		t.Fatalf("live log = %d entries, want %d", len(live), total)
	}

	m.finishRun(ms, "completed", "", "")
	done, err := st.GetLogs(run.ID, 0, 1<<30)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != total {
		t.Fatalf("after finish SQLite has %d entries, want %d (tail duplicated or dropped)", len(done), total)
	}
	if done[0].Message != "e0" || done[total-1].Message != fmt.Sprintf("e%d", total-1) {
		t.Fatalf("final log ends wrong: first=%q last=%q", done[0].Message, done[total-1].Message)
	}
	seen := map[string]bool{}
	for _, e := range done {
		if seen[e.Message] {
			t.Fatalf("duplicate entry %q in final log", e.Message)
		}
		seen[e.Message] = true
	}
}

// A run row still 'running' at startup belongs to a previous app lifetime.
// recoverOrphanedRuns must close it as unfinished and dump the logs the
// incremental flush had saved into the project's logs folder.
func TestRecoverOrphanedRunsClosesAndSaves(t *testing.T) {
	m, st := newTestManagerWithStore(t)

	orphan := &store.SessionRun{Project: "lumen", Session: "P1", StartedAt: time.Now().Add(-time.Hour), Status: "running"}
	if err := st.InsertRun(orphan); err != nil {
		t.Fatal(err)
	}
	entries := []store.LogEntry{
		{Timestamp: time.Now(), Level: "user", Message: "привет"},
		{Timestamp: time.Now(), Level: "text", Message: "ответ"},
	}
	if err := st.InsertLogs(orphan.ID, entries); err != nil {
		t.Fatal(err)
	}

	m.recoverOrphanedRuns()

	closed, err := st.GetRun(orphan.ID)
	if err != nil || closed == nil {
		t.Fatalf("get closed run: %v %v", closed, err)
	}
	if closed.Status != RunStatusUnfinished {
		t.Fatalf("orphan status = %q, want %q", closed.Status, RunStatusUnfinished)
	}
	if closed.FinishedAt == nil {
		t.Fatal("orphan finished_at not set")
	}
	left, err := st.ListRunningRuns()
	if err != nil || len(left) != 0 {
		t.Fatalf("running runs after recovery = %v (err %v), want none", left, err)
	}
	files, err := store.ListProjectLogFiles(projectDirForTest(t, m))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("recovered log files = %d, want 1 (%v)", len(files), files)
	}
}

// projectDirForTest returns the configured path of the "lumen" test project.
func projectDirForTest(t *testing.T, m *SessionManager) string {
	t.Helper()
	for i := range m.cfg.Projects {
		if m.cfg.Projects[i].Name == "lumen" {
			return m.cfg.Projects[i].Path
		}
	}
	t.Fatal("lumen project not configured")
	return ""
}
