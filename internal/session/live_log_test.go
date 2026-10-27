package session

import (
	"testing"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/store"
)

// A run in flight keeps its log in memory; GetSessionLog must serve it from
// there, and the same entries must come back from SQLite once the run ends.
func TestGetSessionLogServesLiveRun(t *testing.T) {
	m, st := newTestManagerWithStore(t)
	ms := addStubSession(m, "lumen", "P1")

	run := &store.SessionRun{Project: "lumen", Session: "P1", StartedAt: time.Now(), Status: "running"}
	if err := st.InsertRun(run); err != nil {
		t.Fatal(err)
	}
	ms.mu.Lock()
	ms.runID = run.ID
	ms.mu.Unlock()

	for _, msg := range []string{"one", "two", "three"} {
		m.onSessionEvent("lumen/P1", SessionEvent{Type: EvtLog, Entry: &config.LogEntry{Time: time.Now(), Level: "text", Message: msg}})
	}

	live, err := m.GetSessionLog("lumen/P1", 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(live) != 3 || live[0].Message != "one" || live[2].Message != "three" {
		t.Fatalf("live log = %d entries, want 3 in order", len(live))
	}
	page, _ := m.GetSessionLog("lumen/P1", 1, 1)
	if len(page) != 1 || page[0].Message != "two" {
		t.Fatalf("offset/limit page wrong: %+v", page)
	}

	m.finishRun(ms, "completed", "", "")
	done, err := m.GetSessionLog("lumen/P1", 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	if len(done) != 3 {
		t.Fatalf("after finish got %d entries, want 3 (no duplicates)", len(done))
	}
}
