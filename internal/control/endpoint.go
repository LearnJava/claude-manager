package control

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/logger"
)

// endpointFile is the name of the discovery file written next to the global
// config (~/.claude-manager/control.json).
const endpointFile = "control.json"

// Endpoint describes a live control-plane server. It is written to disk on
// startup so a local client — cm-mcp above all — can find the address and the
// generated token without the user exporting anything.
//
// The control-plane is on by default (see Enabled), and a GUI build has no
// terminal to print the token to, so "print it to stdout" is not a usable
// handoff for the normal case. The file is the handoff.
type Endpoint struct {
	Addr  string `json:"addr"`  // base URL, e.g. http://127.0.0.1:7333
	Token string `json:"token"` // value of the X-CM-Token header
	PID   int    `json:"pid"`   // the app process that owns this endpoint
}

// EndpointPath returns ~/.claude-manager/control.json.
func EndpointPath() string {
	return filepath.Join(filepath.Dir(config.DefaultConfigPath()), endpointFile)
}

// WriteEndpoint persists ep atomically (tmp → rename) with owner-only
// permissions — the token is a capability to drive every session.
func WriteEndpoint(ep Endpoint) error {
	path := EndpointPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("control: endpoint dir: %w", err)
	}
	data, err := json.MarshalIndent(ep, "", "  ")
	if err != nil {
		return fmt.Errorf("control: marshal endpoint: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("control: write endpoint: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("control: rename endpoint: %w", err)
	}
	return nil
}

// LoadEndpoint reads the discovery file. A missing file yields (nil, nil): no
// app is running with a control-plane, which is not an error for a client.
func LoadEndpoint() (*Endpoint, error) {
	data, err := os.ReadFile(EndpointPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ep Endpoint
	if err := json.Unmarshal(data, &ep); err != nil {
		return nil, fmt.Errorf("control: parse endpoint: %w", err)
	}
	return &ep, nil
}

// RemoveEndpoint deletes the discovery file. Called when the server stops so a
// client never dials a dead address with a stale token.
//
// The delete is retried for half a second because on Windows it transiently
// fails with a sharing violation (an indexer or scanner still holding the
// just-written file) often enough to be seen in the tests — and the cost of
// giving up is a stale file that sends the next cm-mcp at a dead port.
func RemoveEndpoint() {
	path := EndpointPath()
	var err error
	for i := 0; i < 10; i++ {
		if err = os.Remove(path); err == nil || os.IsNotExist(err) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	logger.L.Warn("control.endpoint.remove", "path", path, "error", err)
}
