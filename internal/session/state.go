package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// PersistedState is the on-disk state saved before each runOnce call so that
// an interrupted session can be resumed after an app restart via --resume.
type PersistedState struct {
	SessionID string    `json:"session_id"` // populated once the init event arrives
	StartedAt time.Time `json:"started_at"`
}

// StateStore reads and writes PersistedState files to a directory.
// File names are derived from project and session names so they are human
// readable and survive config renames (project-session.json).
type StateStore struct {
	dir string
}

// NewStateStore creates a StateStore that persists files under dir.
func NewStateStore(dir string) *StateStore {
	return &StateStore{dir: dir}
}

var stateNameReplacer = strings.NewReplacer("/", "_", "\\", "_", ":", "_", " ", "_")

func (s *StateStore) filePath(project, name string) string {
	filename := stateNameReplacer.Replace(project) + "-" + stateNameReplacer.Replace(name) + ".json"
	return filepath.Join(s.dir, filename)
}

// Load returns the saved state for project/session, or (nil, nil) if none exists.
// Corrupted files are silently discarded.
func (s *StateStore) Load(project, name string) (*PersistedState, error) {
	data, err := os.ReadFile(s.filePath(project, name))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st PersistedState
	if err := json.Unmarshal(data, &st); err != nil {
		_ = os.Remove(s.filePath(project, name))
		return nil, nil
	}
	return &st, nil
}

// Save writes st to disk atomically (write tmp → rename) so a crash mid-write
// cannot corrupt the state file and lose recovery information.
func (s *StateStore) Save(project, name string, st *PersistedState) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	target := s.filePath(project, name)
	tmp := target + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, target)
}

// UpdateSessionID patches the session_id field in an existing state file.
// No-op if the file does not exist or already has the same ID.
func (s *StateStore) UpdateSessionID(project, name, sessionID string) {
	st, _ := s.Load(project, name)
	if st == nil || st.SessionID == sessionID {
		return
	}
	st.SessionID = sessionID
	_ = s.Save(project, name, st)
}

// Clear deletes the state file for project/session. Safe to call when absent.
func (s *StateStore) Clear(project, name string) {
	path := s.filePath(project, name)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		// Non-fatal; the file may have already been removed.
		_ = err
	}
}
