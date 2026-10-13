package session

import (
	"testing"

	"claude-manager/internal/config"
)

func TestPreflightMode(t *testing.T) {
	prompt := config.SessionConfig{Prompt: "Add a CSV export", Preflight: "auto"}
	cases := []struct {
		name   string
		global bool
		sc     config.SessionConfig
		want   string
	}{
		{"global switch off", false, prompt, "never"},
		{"auto with a prompt", true, prompt, "auto"},
		{"empty mode means auto", true, config.SessionConfig{Prompt: "x"}, "auto"},
		{"always", true, config.SessionConfig{Prompt: "x", Preflight: "always"}, "always"},
		{"never", true, config.SessionConfig{Prompt: "x", Preflight: "never"}, "never"},
		{"empty prompt", true, config.SessionConfig{Prompt: "  ", Preflight: "always"}, "never"},
		{"queue session", true, config.SessionConfig{Prompt: "x", Preflight: "always", TaskSource: "STATUS-P1.md"}, "never"},
		{"auto-restart session", true, config.SessionConfig{Prompt: "x", Preflight: "always", AutoRestart: true}, "never"},
	}
	for _, c := range cases {
		if got := PreflightMode(c.global, c.sc); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSessionPreflight_NeverSkipsAnalysis(t *testing.T) {
	m := newTestManager(t)
	m.cfg.Settings.PreflightAnalysis = true
	m.cfg.Projects[0].Sessions[0].Prompt = "do a thing"
	m.cfg.Projects[0].Sessions[0].Preflight = "never"

	// "never" must return without calling the analyst (no claude binary here).
	plan, err := m.SessionPreflight("lumen", "P1")
	if err != nil || plan != nil {
		t.Fatalf("got plan=%v err=%v, want nil/nil", plan, err)
	}
}
