package logship

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// stateFile is the name of the marks file inside the manager's data dir. It
// lives outside the clone on purpose: a log repository recreated by hand must
// not receive the whole old history again.
const stateFile = "logship.json"

// state holds, per project, the timestamp string (from the log file name) of
// the newest file already pushed. Strings compare lexicographically.
type state struct {
	Marks map[string]string `json:"marks"`
	// Offsets is, per manager log file name, how many bytes were already shipped.
	Offsets map[string]int64 `json:"offsets,omitempty"`
}

func loadState(dataDir string) (state, error) {
	st := state{Marks: map[string]string{}, Offsets: map[string]int64{}}
	data, err := os.ReadFile(filepath.Join(dataDir, stateFile))
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}
	if err != nil {
		return st, fmt.Errorf("logship: read state: %w", err)
	}
	if err := json.Unmarshal(data, &st); err != nil {
		// A corrupt marks file only costs a re-send of the last 7 days.
		return state{Marks: map[string]string{}, Offsets: map[string]int64{}}, nil
	}
	if st.Marks == nil {
		st.Marks = map[string]string{}
	}
	if st.Offsets == nil {
		st.Offsets = map[string]int64{}
	}
	return st, nil
}

func saveState(dataDir string, st state) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fmt.Errorf("logship: mkdir %s: %w", dataDir, err)
	}
	path := filepath.Join(dataDir, stateFile)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("logship: write state: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("logship: replace state: %w", err)
	}
	return nil
}
