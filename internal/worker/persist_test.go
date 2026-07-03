package worker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestStoreSaveLoadRoundtrip(t *testing.T) {
	s := NewStore(t.TempDir())
	msgs := []ChatMessage{
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
	}
	if err := s.Save("proj/task-1", msgs); err != nil {
		t.Fatalf("Save: %v", err)
	}
	loaded, err := s.Load("proj/task-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded == nil || len(loaded.Messages) != 2 || loaded.Messages[1].Content != "hello" {
		t.Fatalf("loaded = %+v", loaded)
	}
	if loaded.UpdatedAt.IsZero() {
		t.Error("UpdatedAt not set")
	}
}

func TestStoreLoadMissing(t *testing.T) {
	s := NewStore(t.TempDir())
	loaded, err := s.Load("nope")
	if err != nil || loaded != nil {
		t.Fatalf("Load missing = (%v, %v), want (nil, nil)", loaded, err)
	}
}

func TestStoreLoadCorruptedDiscarded(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := os.WriteFile(s.filePath("bad"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	loaded, err := s.Load("bad")
	if err != nil || loaded != nil {
		t.Fatalf("Load corrupted = (%v, %v), want (nil, nil)", loaded, err)
	}
	if _, statErr := os.Stat(s.filePath("bad")); !os.IsNotExist(statErr) {
		t.Error("corrupted file was not removed")
	}
}

func TestStoreClear(t *testing.T) {
	s := NewStore(t.TempDir())
	s.Save("x", []ChatMessage{{Role: "user", Content: "hi"}})
	s.Clear("x")
	loaded, err := s.Load("x")
	if err != nil || loaded != nil {
		t.Fatalf("after Clear: (%v, %v), want (nil, nil)", loaded, err)
	}
	s.Clear("x") // idempotent, no error
}

func TestStoreFilePathSanitizesID(t *testing.T) {
	s := NewStore(t.TempDir())
	path := s.filePath("lumen-browser/S1")
	if filepath.Base(path) != "worker-lumen-browser_S1.json" {
		t.Errorf("filePath = %q", path)
	}
}

func TestStoreAtomicNoTmpLeftover(t *testing.T) {
	dir := t.TempDir()
	s := NewStore(dir)
	if err := s.Save("x", []ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".tmp" {
			t.Errorf("tmp file left behind: %s", e.Name())
		}
	}
}

// Integration: Client.Complete persists the dialogue after every exchange,
// including mid-continuation state, so a crash between continuations is
// recoverable by replaying the persisted messages.
func TestClientPersistsDialogueAfterEachExchange(t *testing.T) {
	var call int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call++
		w.Header().Set("Content-Type", "text/event-stream")
		if call == 1 {
			sseFrame(w, "part1", "length")
		} else {
			sseFrame(w, "part2", "stop")
		}
		writeDone(w)
	}))
	defer srv.Close()

	store := NewStore(t.TempDir())
	cfg := testWorkerConfig(srv.URL)
	c := NewClient(cfg, "key")
	c.sleep = noSleep

	_, err := c.Complete(context.Background(), "brief-42", []ChatMessage{{Role: "user", Content: "go"}}, store)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}

	loaded, err := store.Load("brief-42")
	if err != nil || loaded == nil {
		t.Fatalf("Load: (%v, %v)", loaded, err)
	}
	// user + assistant(part1) + user(continue) + assistant(part2) = 4
	if len(loaded.Messages) != 4 {
		b, _ := json.MarshalIndent(loaded.Messages, "", "  ")
		t.Fatalf("persisted messages = %d, want 4:\n%s", len(loaded.Messages), b)
	}
	if loaded.Messages[3].Content != "part2" {
		t.Errorf("final assistant message = %+v", loaded.Messages[3])
	}
}
