package session

import (
	"testing"

	"claude-manager/internal/config"
)

// Auth failures from the real CLI arrive as result text on stdout, not stderr
// (observed live: crash-loop on "Failed to authenticate. API Error: 403
// Request not allowed"). handleLine must flag them so runOnce returns
// errAuthError instead of a generic error that retries every 30s forever.

func TestIsAuthError_ResultText(t *testing.T) {
	if !isAuthError("Failed to authenticate. API Error: 403 Request not allowed") {
		t.Error("real-world CLI auth failure text not detected")
	}
}

func TestHandleLine_ResultAuthErrorSetsFlag(t *testing.T) {
	s := New(Params{
		ID:          "p/s",
		ProjectName: "p",
		Config:      config.SessionConfig{Name: "s"},
	})

	line := `{"type":"result","subtype":"success","result":"Failed to authenticate. API Error: 403 Request not allowed","total_cost_usd":0,"num_turns":1}`
	if done := s.handleLine(line, false); !done {
		t.Fatal("result line should report turn finished")
	}
	if !s.authErrorHit.Load() {
		t.Error("authErrorHit not set from 403 result text")
	}
}

func TestHandleLine_ResultOKDoesNotSetFlag(t *testing.T) {
	s := New(Params{
		ID:          "p/s",
		ProjectName: "p",
		Config:      config.SessionConfig{Name: "s"},
	})

	line := `{"type":"result","subtype":"success","result":"Task completed successfully","total_cost_usd":0.5,"num_turns":3}`
	s.handleLine(line, false)
	if s.authErrorHit.Load() {
		t.Error("authErrorHit set on a successful result")
	}
}
