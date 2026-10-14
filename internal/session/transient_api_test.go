package session

import (
	"errors"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// Observed live on a Lumen session: this result ended a 30-minute run and the
// Run loop counted it as an unfinished task instead of a network failure.
const econnrefusedResult = `{"type":"result","subtype":"success","is_error":true,"result":"API Error: Connection refused — a firewall or proxy may be blocking it (ECONNREFUSED)","total_cost_usd":1.4,"num_turns":28}`

func TestIsTransientAPIError(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"API Error: Connection refused — a firewall or proxy may be blocking it (ECONNREFUSED)", true},
		{"API Error: Connection error.", true},
		{"API Error: Request timed out.", true},
		{`API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, true},
		{`API Error: 500 {"type":"error","error":{"type":"api_error","message":"Internal server error"}}`, true},
		// Not transient: a request that will fail the same way again.
		{"API Error: 400 invalid_request_error: prompt is too long", false},
		{"Failed to authenticate. API Error: 403 Request not allowed", false},
		// A normal answer that merely mentions a network error.
		{"Fixed the ECONNREFUSED in the dev server: it now binds 0.0.0.0", false},
		{"", false},
	}
	for _, c := range cases {
		if got := isTransientAPIError(c.text); got != c.want {
			t.Errorf("isTransientAPIError(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestClaudeParser_ResultConnRefused_ClassifiesTransient(t *testing.T) {
	ev := ParseLine(econnrefusedResult)
	if ev.Failure == nil || ev.Failure.Kind != KindTransientAPI {
		t.Fatalf("Failure = %+v, want Kind=%q", ev.Failure, KindTransientAPI)
	}
}

func TestHandleLine_TransientResult_MapsToSentinel(t *testing.T) {
	s := New(Params{ID: "p/s", ProjectName: "p", Config: config.SessionConfig{Name: "s"}})
	if done := s.handleLine(econnrefusedResult, false); !done {
		t.Fatal("result line should report turn finished")
	}
	if err := s.classifySentinelError(); !errors.Is(err, errTransientAPI) {
		t.Fatalf("classifySentinelError = %v, want errTransientAPI", err)
	}
	s.resetRunAtomics()
	if err := s.classifySentinelError(); err != nil {
		t.Fatalf("after resetRunAtomics: %v, want nil", err)
	}
}

func TestTransientAPIDelay_DoublesAndCaps(t *testing.T) {
	want := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 4 * time.Minute, 8 * time.Minute, 10 * time.Minute, 10 * time.Minute}
	for i, w := range want {
		if got := transientAPIDelay(30, i+1); got != w {
			t.Errorf("transientAPIDelay(30, %d) = %s, want %s", i+1, got, w)
		}
	}
	if got := transientAPIDelay(0, 1); got != 30*time.Second {
		t.Errorf("zero retry delay: got %s, want 30s", got)
	}
}
