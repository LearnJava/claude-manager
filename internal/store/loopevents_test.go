package store

import (
	"testing"
	"time"
)

func TestLoopEventsLifecycle(t *testing.T) {
	s := newTestStore(t)
	t0 := time.Now().UTC().Truncate(time.Second)
	a := &LoopEvent{Project: "p", Session: "S", RunID: 7, Sig: "Bash:go test", Tool: "Bash", Arg: "go test ./...", Count: 3, Timestamp: t0}
	if err := s.InsertLoopEvent(a); err != nil || a.ID == 0 {
		t.Fatalf("insert: %+v %v", a, err)
	}
	if got, err := s.LastResolvedLoop("p", "Bash:go test"); err != nil || got != nil {
		t.Fatalf("an unresolved loop must not be offered: %+v %v", got, err)
	}
	open, err := s.UnresolvedLoopEvents(7)
	if err != nil || len(open) != 1 || open[0].Far || open[0].Arg != "go test ./..." {
		t.Fatalf("unresolved = %+v %v", open, err)
	}
	if err := s.SetLoopResolved(a.ID, "Edit main.go"); err != nil {
		t.Fatal(err)
	}
	if open, _ := s.UnresolvedLoopEvents(7); len(open) != 0 {
		t.Error("resolved event is still listed as open")
	}
	b := &LoopEvent{Project: "p", Session: "S", RunID: 8, Sig: "Bash:go test", Tool: "Bash", Far: true, Timestamp: t0.Add(time.Hour), ResolvedBy: "Read go.mod"}
	if err := s.InsertLoopEvent(b); err != nil {
		t.Fatal(err)
	}
	got, err := s.LastResolvedLoop("p", "Bash:go test")
	if err != nil || got == nil || got.ID != b.ID || !got.Far || got.ResolvedBy != "Read go.mod" {
		t.Fatalf("latest resolved = %+v %v, want the newer one", got, err)
	}
	if other, _ := s.LastResolvedLoop("q", "Bash:go test"); other != nil {
		t.Error("another project's loops must not leak")
	}
	if list, _ := s.ListLoopEvents("p", 1); len(list) != 1 || list[0].ID != b.ID {
		t.Errorf("ListLoopEvents newest first = %+v", list)
	}
}
