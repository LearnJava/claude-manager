package session

import (
	"testing"

	"claude-manager/internal/config"
)

// Live model switching (see CLAUDE.md "Live Model Switching"): an autonomous
// session picks up SetModel at its next task boundary with no restart, while
// an interactive session has no such boundary and must be soft-restarted by
// the manager (SessionManager.SetSessionModel) to actually take effect.

func TestSession_SetModel_UpdatesActiveModelOnly(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Model: "sonnet"}})
	if got := s.ActiveModel(); got != "sonnet" {
		t.Fatalf("initial ActiveModel = %q, want sonnet", got)
	}

	s.SetModel("opus")

	if got := s.ActiveModel(); got != "opus" {
		t.Errorf("ActiveModel = %q, want opus", got)
	}
	if s.Config.Model != "sonnet" {
		t.Errorf("Config.Model changed to %q, want it to stay sonnet (SetModel is a live override only)", s.Config.Model)
	}
}

func TestSession_Autonomous(t *testing.T) {
	cases := []struct {
		name string
		cfg  config.SessionConfig
		want bool
	}{
		{"plain interactive", config.SessionConfig{}, false},
		{"auto_restart", config.SessionConfig{AutoRestart: true}, true},
		{"stop_when_no_tasks", config.SessionConfig{StopWhenNoTasks: true}, true},
		{"both", config.SessionConfig{AutoRestart: true, StopWhenNoTasks: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(Params{Config: tc.cfg})
			if got := s.Autonomous(); got != tc.want {
				t.Errorf("Autonomous() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNew_SeedsResumeSessionIDFromParams(t *testing.T) {
	s := New(Params{
		Config:          config.SessionConfig{Model: "sonnet"},
		ResumeSessionID: "prior-cli-session-abc",
	})
	if s.resumeSessionID != "prior-cli-session-abc" {
		t.Errorf("resumeSessionID = %q, want the seeded value", s.resumeSessionID)
	}
}
