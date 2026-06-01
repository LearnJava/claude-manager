package testkit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Scenario represents a fakeclaude test scenario loaded from a JSON file.
type Scenario struct {
	Name          string  `json:"name"`
	Match         string  `json:"match"`
	Model         string  `json:"model"`
	ContextWindow int     `json:"context_window"`
	ExitCode      int     `json:"exit_code"`
	Steps         []Step  `json:"steps"`

	matchRe *regexp.Regexp
}

// Step is one instruction in a scenario.
type Step struct {
	Type      string            `json:"type"`       // "emit" or "await_stdin"
	DelayMs   int               `json:"delay_ms"`
	Event     json.RawMessage   `json:"event"`      // for "emit"
	Expect    string            `json:"expect"`     // optional expected type for "await_stdin"
	TimeoutMs int               `json:"timeout_ms"` // for "await_stdin"
	StoreAs   string            `json:"store_as"`   // variable name to store parsed JSON
	On        map[string]string `json:"on"`         // branching conditions
}

// Vars holds template variables for scenario substitution.
type Vars struct {
	SessionID string
	Name      string
	Model     string
	Cwd       string
}

// Apply replaces ${session_id}, ${model}, ${cwd}, ${name} placeholders in raw JSON.
// Windows paths in Cwd are properly escaped for JSON strings.
func (v Vars) Apply(raw json.RawMessage) json.RawMessage {
	s := string(raw)
	s = strings.ReplaceAll(s, "${session_id}", jsonEscape(v.SessionID))
	s = strings.ReplaceAll(s, "${model}", jsonEscape(v.Model))
	s = strings.ReplaceAll(s, "${cwd}", jsonEscape(v.Cwd))
	s = strings.ReplaceAll(s, "${name}", jsonEscape(v.Name))
	return json.RawMessage(s)
}

// jsonEscape encodes a Go string as a JSON string value (without surrounding quotes).
// This handles backslashes and other special characters (important for Windows paths).
func jsonEscape(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		return s
	}
	// Strip the surrounding quotes that Marshal adds.
	return string(b[1 : len(b)-1])
}

// LoadScenario loads a single scenario from the given JSON file.
func LoadScenario(path string) (*Scenario, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Scenario
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Name == "" {
		s.Name = strings.TrimSuffix(filepath.Base(path), ".json")
	}
	if s.Model == "" {
		s.Model = "claude-sonnet-4-6"
	}
	if s.ContextWindow == 0 {
		s.ContextWindow = 200000
	}
	if s.Match != "" {
		re, err := regexp.Compile(s.Match)
		if err != nil {
			return nil, err
		}
		s.matchRe = re
	}
	return &s, nil
}

// LoadScenariosDir loads all *.json files from the given directory.
func LoadScenariosDir(dir string) ([]*Scenario, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var scenarios []*Scenario
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := LoadScenario(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		scenarios = append(scenarios, s)
	}
	return scenarios, nil
}

// MatchScenario returns the first scenario whose Match regex matches prompt.
// Returns nil if no scenario matches.
func MatchScenario(scenarios []*Scenario, prompt string) *Scenario {
	for _, s := range scenarios {
		if s.matchRe != nil && s.matchRe.MatchString(prompt) {
			return s
		}
	}
	return nil
}
