package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DialogueState is the persisted conversation with a worker. The chat API is
// stateless — resuming after a crash means reloading and replaying these
// messages, not reconnecting to a session.
type DialogueState struct {
	Messages  []ChatMessage `json:"messages"`
	UpdatedAt time.Time     `json:"updated_at"`
}

// Store persists worker dialogues to ~/.claude-manager/state/worker-<id>.json,
// atomically (tmp file + rename), mirroring session.StateStore.
type Store struct {
	dir string
}

// NewStore creates a Store that persists files under dir.
func NewStore(dir string) *Store {
	return &Store{dir: dir}
}

var storeIDReplacer = strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")

func (s *Store) filePath(id string) string {
	return filepath.Join(s.dir, "worker-"+storeIDReplacer.Replace(id)+".json")
}

// Save writes the current message history for id, overwriting any prior save.
// Called after every exchange so a crash mid-round can be replayed exactly.
func (s *Store) Save(id string, messages []ChatMessage) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	st := DialogueState{Messages: messages, UpdatedAt: time.Now()}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	target := s.filePath(id)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// Load returns the saved dialogue for id, or (nil, nil) if none exists.
// Corrupted files are silently discarded, matching session.StateStore.Load.
func (s *Store) Load(id string) (*DialogueState, error) {
	data, err := os.ReadFile(s.filePath(id))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st DialogueState
	if err := json.Unmarshal(data, &st); err != nil {
		_ = os.Remove(s.filePath(id))
		return nil, nil
	}
	return &st, nil
}

// Clear deletes the persisted dialogue for id. Safe to call when absent.
func (s *Store) Clear(id string) {
	if err := os.Remove(s.filePath(id)); err != nil && !os.IsNotExist(err) {
		_ = err
	}
}
