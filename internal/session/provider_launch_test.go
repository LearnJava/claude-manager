package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/config"
)

// TestBuildCLIArgs_SettingsFile covers the extra --settings payload: absent
// for a plain session (regression: argv unchanged), the file path when only
// the extra settings exist, and a guard+file merge when both are on. The
// core stays provider-agnostic: it only knows "a settings file".
func TestBuildCLIArgs_SettingsFile(t *testing.T) {
	base := func() *Session {
		return New(Params{
			ID:          "lumen/P1",
			ProjectName: "lumen",
			Config:      config.SessionConfig{Name: "P1", Model: "some-model"},
		})
	}

	s := base()
	args := s.buildCLIArgs(false)
	if hasFlag(args, "--settings") {
		t.Errorf("plain session must not pass --settings: %v", args)
	}

	dir := t.TempDir()
	file := filepath.Join(dir, "model.json")
	if err := os.WriteFile(file, []byte(`{"env":{"ANTHROPIC_BASE_URL":"https://example.invalid"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	s = base()
	s.settingsFile = file
	args = s.buildCLIArgs(false)
	if v := findFlag(args, "--settings"); v != file {
		t.Errorf("--settings = %q, want the file path", v)
	}

	// Guard + file: a single merged inline payload carrying both the file's
	// env block and the guard's hooks.
	s = base()
	s.settingsFile = file
	s.commandGuard = true
	args = s.buildCLIArgs(false)
	v := findFlag(args, "--settings")
	if v == "" || v == file {
		t.Fatalf("--settings = %q, want a merged inline payload", v)
	}
	var merged map[string]any
	if err := json.Unmarshal([]byte(v), &merged); err != nil {
		t.Fatalf("merged payload is not JSON: %v\n%s", err, v)
	}
	if _, ok := merged["env"]; !ok {
		t.Errorf("merge lost the file's env block: %s", v)
	}
	if _, ok := merged["hooks"]; !ok {
		t.Errorf("merge lost the guard's hooks: %s", v)
	}
	if n := strings.Count(strings.Join(args, "\n"), "--settings"); n != 1 {
		t.Errorf("--settings appears %d times, want exactly 1: %v", n, args)
	}
}

func TestApplyEnvPairs(t *testing.T) {
	env := []string{"PATH=C:\\bin", "TOKEN=old"}
	out := applyEnvPairs(env, []string{"TOKEN=new", "MALFORMED", "EXTRA=1"})
	joined := strings.Join(out, "\n")
	if !strings.Contains(joined, "TOKEN=new") || strings.Contains(joined, "TOKEN=old") {
		t.Errorf("duplicate key must be overridden: %v", out)
	}
	if !strings.Contains(joined, "EXTRA=1") {
		t.Errorf("new key must be added: %v", out)
	}
	if len(out) != 3 {
		t.Errorf("malformed pair must be skipped: %v", out)
	}
}

// TestDrainStderrUnrecognizedModelIsInfo: the CLI prints
// "[claude-code:unrecognized_model]" for any model id it does not know —
// a provider brain is proxied as-is and the session works, so the feed must
// not paint that line red. Any other stderr line stays an error.
func TestDrainStderrUnrecognizedModelIsInfo(t *testing.T) {
	var got []config.LogEntry
	s := New(Params{
		ID: "t/P1", ProjectName: "t", Config: config.SessionConfig{Name: "P1"},
		OnEvent: func(sid string, ev SessionEvent) {
			if ev.Type == EvtLog && ev.Entry != nil {
				got = append(got, *ev.Entry)
			}
		},
	})
	s.drainStderr(strings.NewReader("[claude-code:unrecognized_model] {\"model\":\"x\"}\nsomething actually broke\n"))
	if len(got) != 2 {
		t.Fatalf("entries = %d, want 2", len(got))
	}
	if got[0].Level != "info" || !strings.Contains(got[0].Message, "не ошибка") {
		t.Errorf("unrecognized_model entry = %+v, want info with a note", got[0])
	}
	if got[1].Level != "error" {
		t.Errorf("plain stderr entry level = %s, want error", got[1].Level)
	}
}
