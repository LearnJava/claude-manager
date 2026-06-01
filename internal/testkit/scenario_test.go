package testkit

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoadScenario(t *testing.T) {
	s, err := LoadScenario("../../testdata/scenarios/happy-path.json")
	if err != nil {
		t.Fatalf("LoadScenario: %v", err)
	}
	if s.Name != "happy-path" {
		t.Errorf("Name = %q, want %q", s.Name, "happy-path")
	}
	if len(s.Steps) != 4 {
		t.Errorf("len(Steps) = %d, want 4", len(s.Steps))
	}
}

func TestVarsApply(t *testing.T) {
	v := Vars{
		SessionID: "s123",
		Model:     "claude-sonnet-4-6",
		Cwd:       "/tmp/proj",
		Name:      "P1",
	}
	raw := json.RawMessage(`{"session_id":"${session_id}","model":"${model}"}`)
	result := string(v.Apply(raw))

	if !strings.Contains(result, "s123") {
		t.Errorf("result %q does not contain session id s123", result)
	}
	if !strings.Contains(result, "claude-sonnet-4-6") {
		t.Errorf("result %q does not contain model", result)
	}
}

func TestVarsApplyWindowsPath(t *testing.T) {
	v := Vars{
		SessionID: "sid",
		Model:     "m",
		Cwd:       `C:\Users\foo`,
		Name:      "N",
	}
	raw := json.RawMessage(`{"cwd":"${cwd}"}`)
	result := string(v.Apply(raw))

	// In the resulting JSON string value, backslashes must be escaped as \\
	if !strings.Contains(result, `C:\\Users\\foo`) {
		t.Errorf("result %q does not contain properly escaped Windows path", result)
	}
}

func TestMatchScenario(t *testing.T) {
	scenarios, err := LoadScenariosDir("../../testdata/scenarios")
	if err != nil {
		t.Fatalf("LoadScenariosDir: %v", err)
	}

	got := MatchScenario(scenarios, "please say hello")
	if got == nil {
		t.Fatal("expected a match for 'hello', got nil")
	}
	if got.Name != "happy-path" {
		t.Errorf("matched %q, want happy-path", got.Name)
	}

	got2 := MatchScenario(scenarios, "this will fail completely")
	if got2 == nil {
		t.Fatal("expected a match for 'fail', got nil")
	}
	if got2.Name != "error-exit" {
		t.Errorf("matched %q, want error-exit", got2.Name)
	}
}

func TestLoadScenariosDir(t *testing.T) {
	scenarios, err := LoadScenariosDir("../../testdata/scenarios")
	if err != nil {
		t.Fatalf("LoadScenariosDir: %v", err)
	}
	if len(scenarios) < 4 {
		t.Errorf("expected at least 4 scenarios, got %d", len(scenarios))
	}
}

func TestCheckOn_Empty(t *testing.T) {
	s, err := LoadScenario("../../testdata/scenarios/happy-path.json")
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{
		scenario: s,
		stored:   make(map[string]map[string]interface{}),
	}
	if !r.checkOn(nil) {
		t.Error("checkOn(nil) should return true")
	}
	if !r.checkOn(map[string]string{}) {
		t.Error("checkOn(empty) should return true")
	}
}

func TestCheckOn_Match(t *testing.T) {
	s, err := LoadScenario("../../testdata/scenarios/happy-path.json")
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{
		scenario: s,
		stored: map[string]map[string]interface{}{
			"perm": {"decision": "allow"},
		},
	}
	if !r.checkOn(map[string]string{"perm.decision": "allow"}) {
		t.Error("checkOn should return true when condition matches")
	}
}

func TestCheckOn_NoMatch(t *testing.T) {
	s, err := LoadScenario("../../testdata/scenarios/happy-path.json")
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{
		scenario: s,
		stored: map[string]map[string]interface{}{
			"perm": {"decision": "allow"},
		},
	}
	if r.checkOn(map[string]string{"perm.decision": "deny"}) {
		t.Error("checkOn should return false when condition does not match")
	}
}
