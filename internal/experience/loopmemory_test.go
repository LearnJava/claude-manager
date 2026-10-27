package experience

import (
	"strings"
	"testing"
	"time"

	"claude-manager/internal/optimization"
	"claude-manager/internal/store"
)

type loopFix struct {
	st  *store.Store
	run *store.SessionRun
	at  time.Time
}

func newLoopFix(t *testing.T) loopFix {
	t.Helper()
	st, err := store.New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	run := &store.SessionRun{Project: "proj", Session: "S", StartedAt: time.Now(), Status: "completed"}
	if err := st.InsertRun(run); err != nil {
		t.Fatal(err)
	}
	return loopFix{st: st, run: run, at: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)}
}

func (f loopFix) mem(now time.Time) *LoopMemory {
	m := NewLoopMemory(f.st, "proj", "S", "", func() int64 { return f.run.ID })
	m.now = func() time.Time { return now }
	return m
}

func (f loopFix) actions(t *testing.T, rows ...store.ActionRow) {
	t.Helper()
	for i := range rows {
		rows[i].Project, rows[i].Session, rows[i].RunID, rows[i].StepIndex = "proj", "S", &f.run.ID, i
		if rows[i].Sig == "" {
			rows[i].Sig = LoopSig(rows[i].Tool, rows[i].Arg, "")
		}
	}
	if err := f.st.InsertActions(rows); err != nil {
		t.Fatal(err)
	}
}

func TestLoopIsRecordedResolvedAndAnsweredNextTime(t *testing.T) {
	f := newLoopFix(t)
	loop := &optimization.LoopDetected{Tool: "Bash", Input: "go test ./...", Count: 3}

	// Nothing known yet: no hint, and recording the loop does not make one.
	m := f.mem(f.at.Add(2 * time.Second))
	if h := m.Hint("Bash", "go test ./..."); h != "" {
		t.Fatalf("empty history hint = %q", h)
	}
	m.Record(loop)
	if h := m.Hint("Bash", "go test ./..."); h != "" {
		t.Fatalf("an unresolved loop must not answer itself, got %q", h)
	}

	// The run, indexed: the loop, a failed attempt, the repeat, then a way out.
	f.actions(t,
		store.ActionRow{Tool: "Bash", Arg: "go test ./...", Timestamp: f.at},
		store.ActionRow{Tool: "Bash", Arg: "go test ./...", Timestamp: f.at.Add(time.Second)},
		store.ActionRow{Tool: "Bash", Arg: "go test ./...", Timestamp: f.at.Add(2 * time.Second)},
		store.ActionRow{Tool: "Bash", Arg: "go vet ./...", IsError: true, Timestamp: f.at.Add(3 * time.Second)},
		store.ActionRow{Tool: "Bash", Arg: "go test ./...", Timestamp: f.at.Add(4 * time.Second)},
		store.ActionRow{Tool: "Edit", Arg: "internal/a.go", Timestamp: f.at.Add(5 * time.Second)},
	)
	n, err := ResolveRunLoops(f.st, f.run.ID)
	if err != nil || n != 1 {
		t.Fatalf("resolved %d, %v; want 1", n, err)
	}
	if n, _ := ResolveRunLoops(f.st, f.run.ID); n != 0 {
		t.Error("an already resolved loop is resolved again")
	}

	// A later session loops on the same call, a day on.
	hint := f.mem(f.at.Add(26 * time.Hour)).Hint("Bash", "go test ./...")
	for _, want := range []string{"Edit internal/a.go", "25 h ago"} {
		if !strings.Contains(hint, want) {
			t.Errorf("hint %q lacks %q", hint, want)
		}
	}
	if strings.Contains(hint, "go vet") {
		t.Errorf("a failed action is not a way out: %q", hint)
	}
	if other := f.mem(f.at).Hint("Bash", "go build ./..."); other != "" {
		t.Errorf("a different call must not get this hint: %q", other)
	}
}

func TestLoopWithNoWayOutStaysUnresolved(t *testing.T) {
	f := newLoopFix(t)
	f.mem(f.at).Record(&optimization.LoopDetected{Tool: "Read", Input: "a.go @10:20", Count: 3, Far: true})
	f.actions(t,
		store.ActionRow{Tool: "Read", Arg: "a.go", Timestamp: f.at.Add(time.Second)},
		store.ActionRow{Tool: "Read", Arg: "a.go", Timestamp: f.at.Add(2 * time.Second)},
	)
	if n, err := ResolveRunLoops(f.st, f.run.ID); err != nil || n != 0 {
		t.Fatalf("resolved %d, %v; want 0", n, err)
	}
	if h := f.mem(f.at).Hint("Read", "a.go"); h != "" {
		t.Errorf("hint = %q, want none", h)
	}
	evs, _ := f.st.ListLoopEvents("proj", 0)
	if len(evs) != 1 || !evs[0].Far || evs[0].Sig != LoopSig("Read", "a.go", "") {
		t.Errorf("event = %+v; the Read range must not change the signature", evs)
	}
}

func TestLoopMemoryNilIsSafe(t *testing.T) {
	var m *LoopMemory
	if m.Hint("Bash", "x") != "" {
		t.Error("nil memory hints")
	}
	m.Record(&optimization.LoopDetected{})
	if n, err := ResolveRunLoops(nil, 5); n != 0 || err != nil {
		t.Errorf("nil store: %d %v", n, err)
	}
}
