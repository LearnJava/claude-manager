package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"claude-manager/internal/config"
)

func queueEvents(em *captureEmitter) int {
	n := 0
	for _, e := range em.events {
		if e.Name == EventNameQueueChanged {
			n++
		}
	}
	return n
}

func TestQueueWatch(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("STATUS-P1.md", "T.md:1\n")
	write("T.md", "## A-01: one\n")
	write("Other.md", "unrelated\n")

	m := NewSessionManager(&config.AppConfig{Projects: []config.ProjectConfig{{
		Name: "p", Path: dir,
		Sessions: []config.SessionConfig{{Name: "S", TaskSource: "STATUS-P1.md"}, {Name: "Chat"}},
	}}}, filepath.Join(dir, "cfg.toml"), nil, nil)
	em := &captureEmitter{}
	m.emitter = em
	w := &queueWatch{m: m, prev: map[string]string{}}

	w.tick() // first pass only records
	w.tick()
	if n := queueEvents(em); n != 0 {
		t.Fatalf("no change: got %d events", n)
	}

	write("STATUS-P1.md", "T.md:1\nT.md:2\n")
	w.tick()
	w.tick()
	if n := queueEvents(em); n != 1 {
		t.Fatalf("queue edit: got %d events, want 1", n)
	}
	if ev := em.events[0].Data.(QueueChangedEvent); ev.ID != "p/S" {
		t.Fatalf("event id = %q", ev.ID)
	}

	write("T.md", "## A-01: one\nmore text\n")
	w.tick()
	if n := queueEvents(em); n != 2 {
		t.Fatalf("pointed-at file edit: got %d events, want 2", n)
	}

	write("Other.md", "changed, not referenced\n")
	w.tick()
	if n := queueEvents(em); n != 2 {
		t.Fatalf("unreferenced file: got %d events, want 2", n)
	}

	os.Remove(filepath.Join(dir, "T.md"))
	w.tick()
	w.tick()
	if n := queueEvents(em); n != 3 {
		t.Fatalf("deleted file: got %d events, want 3", n)
	}
}

func TestQueueWatchStartStop(t *testing.T) {
	old := queueWatchInterval
	queueWatchInterval = time.Millisecond
	defer func() { queueWatchInterval = old }()
	m := NewSessionManager(&config.AppConfig{}, filepath.Join(t.TempDir(), "c.toml"), nil, nil)
	m.StartQueueWatch()
	m.StartQueueWatch()
	time.Sleep(5 * time.Millisecond)
	m.Shutdown()
	if m.queueWatchCancel != nil {
		t.Fatal("cancel not cleared")
	}
}
