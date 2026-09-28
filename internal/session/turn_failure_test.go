package session

import (
	"bytes"
	"testing"

	"claude-manager/internal/config"
)

// RT-04: each Kind of TurnFailure covered for both runtimes (where the
// runtime can actually produce it — session_not_found is Hermes-only, per
// docs/runtimes.md §5).

func TestApplyFailure_Nil_NoOp(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "s"}})
	s.applyFailure(nil)
	if s.rateLimited.Load() || s.authErrorHit.Load() || s.sessionNotFoundHit.Load() ||
		s.contextRestartHit.Load() || s.stepLimitHit.Load() {
		t.Fatal("applyFailure(nil) must not touch any atomic")
	}
}

func TestApplyFailure_Other_NoAtomicSet(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "s"}})
	s.applyFailure(&TurnFailure{Kind: KindOther, Message: "unclassified"})
	if s.rateLimited.Load() || s.authErrorHit.Load() || s.sessionNotFoundHit.Load() ||
		s.contextRestartHit.Load() || s.stepLimitHit.Load() {
		t.Fatal("KindOther must not set any of the specific-outcome atomics")
	}
}

func TestApplyFailure_RateLimit_SetsRateLimited(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "s"}})
	s.applyFailure(&TurnFailure{Kind: KindRateLimit, Message: "hit your limit"})
	if !s.rateLimited.Load() {
		t.Fatal("KindRateLimit must set rateLimited")
	}
}

func TestApplyFailure_Auth_SetsAuthErrorHit(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "s"}})
	s.applyFailure(&TurnFailure{Kind: KindAuth, Message: "403 forbidden"})
	if !s.authErrorHit.Load() {
		t.Fatal("KindAuth must set authErrorHit")
	}
}

func TestApplyFailure_SessionNotFound_SetsSessionNotFoundHit(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "s"}})
	s.applyFailure(&TurnFailure{Kind: KindSessionNotFound, Message: "Session not found: abc"})
	if !s.sessionNotFoundHit.Load() {
		t.Fatal("KindSessionNotFound must set sessionNotFoundHit")
	}
}

func TestApplyFailure_ContextRestart_SetsContextRestartHit(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "s"}})
	s.applyFailure(&TurnFailure{Kind: KindContextRestart})
	if !s.contextRestartHit.Load() {
		t.Fatal("KindContextRestart must set contextRestartHit")
	}
}

func TestApplyFailure_StepLimit_SetsStepLimitHit(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "s"}})
	s.applyFailure(&TurnFailure{Kind: KindStepLimit})
	if !s.stepLimitHit.Load() {
		t.Fatal("KindStepLimit must set stepLimitHit")
	}
}

// ---- Claude adapter: parser.go's handleResult classifies into ev.Failure ----

func TestClaudeParser_ResultAuthText_ClassifiesAuthFailure(t *testing.T) {
	line := `{"type":"result","subtype":"success","result":"Failed to authenticate. API Error: 403 Request not allowed","total_cost_usd":0,"num_turns":1}`
	ev := ParseLine(line)
	if ev.Failure == nil || ev.Failure.Kind != KindAuth {
		t.Fatalf("Failure = %+v, want Kind=%q", ev.Failure, KindAuth)
	}
}

func TestClaudeParser_ErrorMaxTurns_ClassifiesStepLimitFailure(t *testing.T) {
	line := `{"type":"result","subtype":"error_max_turns","result":"cut off","total_cost_usd":0,"num_turns":50}`
	ev := ParseLine(line)
	if ev.Failure == nil || ev.Failure.Kind != KindStepLimit {
		t.Fatalf("Failure = %+v, want Kind=%q", ev.Failure, KindStepLimit)
	}
}

func TestClaudeParser_SuccessResult_NoFailure(t *testing.T) {
	line := `{"type":"result","subtype":"success","result":"Task completed successfully","total_cost_usd":0.5,"num_turns":3}`
	ev := ParseLine(line)
	if ev.Failure != nil {
		t.Fatalf("Failure = %+v, want nil on a clean success result", ev.Failure)
	}
}

// TestHandleLine_StepLimit_PropagatesToStepLimitHit end-to-ends the adapter
// classification through handleEvent's applyFailure call.
func TestHandleLine_StepLimit_PropagatesToStepLimitHit(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "s"}})
	line := `{"type":"result","subtype":"error_max_turns","result":"cut off","total_cost_usd":0,"num_turns":50}`
	if !s.handleLine(line, false) {
		t.Fatal("result line should report turn finished")
	}
	if !s.stepLimitHit.Load() {
		t.Error("stepLimitHit not set from error_max_turns via ParsedEvent.Failure")
	}
}

// ---- Stderr classifiers (shared helper, both runtimes) ----

func TestClassifyStderrLine_Auth(t *testing.T) {
	f := ClassifyStderrLine("Error: 403 Forbidden — not authenticated")
	if f == nil || f.Kind != KindAuth {
		t.Fatalf("got %+v, want Kind=%q", f, KindAuth)
	}
}

func TestClassifyStderrLine_NoSignal(t *testing.T) {
	if f := ClassifyStderrLine("some ordinary diagnostic line"); f != nil {
		t.Fatalf("got %+v, want nil", f)
	}
}

func TestClassifyHermesStderrLine_SessionNotFound(t *testing.T) {
	f := ClassifyHermesStderrLine("Session not found: 20260101_000000_abcdef")
	if f == nil || f.Kind != KindSessionNotFound {
		t.Fatalf("got %+v, want Kind=%q", f, KindSessionNotFound)
	}
}

func TestClassifyHermesStderrLine_Auth(t *testing.T) {
	f := ClassifyHermesStderrLine("HTTP 403 forbidden: unauthorized")
	if f == nil || f.Kind != KindAuth {
		t.Fatalf("got %+v, want Kind=%q", f, KindAuth)
	}
}

func TestClassifyHermesStderrLine_NoSignal(t *testing.T) {
	if f := ClassifyHermesStderrLine("session_id: 20260101_000000_abcdef"); f != nil {
		t.Fatalf("got %+v, want nil", f)
	}
}

// ---- drainStderr / drainHermesStderr wire the classifiers into the session ----

func TestDrainStderr_AuthLine_SetsAuthErrorHit(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "s"}})
	s.drainStderr(bytes.NewBufferString("Error: 403 Forbidden — not authenticated\n"))
	if !s.authErrorHit.Load() {
		t.Fatal("drainStderr did not set authErrorHit on a 403 line")
	}
}

func TestDrainHermesStderr_SessionNotFound_SetsSessionNotFoundHit(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "s"}})
	s.drainHermesStderr(bytes.NewBufferString("Session not found: 20260101_000000_abcdef\n"))
	if !s.sessionNotFoundHit.Load() {
		t.Fatal("drainHermesStderr did not set sessionNotFoundHit")
	}
}

func TestDrainHermesStderr_Auth_SetsAuthErrorHit(t *testing.T) {
	s := New(Params{Config: config.SessionConfig{Name: "s"}})
	s.drainHermesStderr(bytes.NewBufferString("HTTP 403 forbidden: unauthorized\n"))
	if !s.authErrorHit.Load() {
		t.Fatal("drainHermesStderr did not set authErrorHit on a 403 line")
	}
}
