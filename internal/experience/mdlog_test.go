package experience

import "testing"

const (
	fixtureBasic     = "../../testdata/logfiles/basic.md"
	fixtureParallel  = "../../testdata/logfiles/parallel.md"
	fixtureHeartbeat = "../../testdata/logfiles/heartbeat-error.md"
	fixtureBroken    = "../../testdata/logfiles/broken.md"
)

// TestParseLogFile_Basic covers the plain case: text, a tool call, its
// result, closing text — one of the three required parsing fixtures.
func TestParseLogFile_Basic(t *testing.T) {
	traj, err := ParseLogFile(fixtureBasic)
	if err != nil {
		t.Fatalf("ParseLogFile: %v", err)
	}
	if traj.SessionID != "example-project/S1" {
		t.Errorf("SessionID = %q", traj.SessionID)
	}
	if traj.Skipped != 0 {
		t.Errorf("Skipped = %d, want 0", traj.Skipped)
	}
	wantKinds := []StepKind{StepText, StepToolUse, StepText}
	if len(traj.Steps) != len(wantKinds) {
		t.Fatalf("got %d steps, want %d: %+v", len(traj.Steps), len(wantKinds), traj.Steps)
	}
	for i, k := range wantKinds {
		if traj.Steps[i].Kind != k {
			t.Errorf("step %d kind = %q, want %q", i, traj.Steps[i].Kind, k)
		}
	}

	bash := traj.Steps[1]
	if bash.ToolName != "Bash" {
		t.Errorf("ToolName = %q", bash.ToolName)
	}
	if bash.InputText != "git status --short" {
		t.Errorf("InputText = %q", bash.InputText)
	}
	if bash.ResultText != " M internal/app.go" {
		t.Errorf("ResultText = %q", bash.ResultText)
	}
	if bash.ResultChars != len(" M internal/app.go") {
		t.Errorf("ResultChars = %d", bash.ResultChars)
	}
	if bash.ResultIsError {
		t.Error("ResultIsError = true, want false")
	}
	if bash.Time.IsZero() {
		t.Error("Time is zero")
	}
}

// TestParseLogFile_Parallel: three tool_use entries back to back, followed
// by three tool_result entries — the FIFO binding must match each result to
// the call in the same order they were issued, not "next line" (LN-17 rule 1).
func TestParseLogFile_Parallel(t *testing.T) {
	traj, err := ParseLogFile(fixtureParallel)
	if err != nil {
		t.Fatalf("ParseLogFile: %v", err)
	}
	var calls []Step
	for _, s := range traj.Steps {
		if s.Kind == StepToolUse {
			calls = append(calls, s)
		}
	}
	if len(calls) != 3 {
		t.Fatalf("got %d tool calls, want 3", len(calls))
	}
	wantTools := []string{"Read", "Read", "Grep"}
	wantResults := []string{
		"package experience...",
		"package experience...",
		"transcript.go:30:	StepToolUse  StepKind = \"tool_use\"",
	}
	for i, c := range calls {
		if c.ToolName != wantTools[i] {
			t.Errorf("call %d ToolName = %q, want %q", i, c.ToolName, wantTools[i])
		}
		if c.ResultText != wantResults[i] {
			t.Errorf("call %d ResultText = %q, want %q", i, c.ResultText, wantResults[i])
		}
	}
}

// TestParseLogFile_HeartbeatAndError: tool_progress heartbeats between a call
// and its outcome must not break the binding (LN-17 rule 2), and a failure
// reported at "error" level (not "tool_result") must still close the call and
// mark it failed (LN-17 rule 3) — the third required parsing fixture.
func TestParseLogFile_HeartbeatAndError(t *testing.T) {
	traj, err := ParseLogFile(fixtureHeartbeat)
	if err != nil {
		t.Fatalf("ParseLogFile: %v", err)
	}
	var call *Step
	for i := range traj.Steps {
		if traj.Steps[i].Kind == StepToolUse {
			call = &traj.Steps[i]
			break
		}
	}
	if call == nil {
		t.Fatal("no tool_use step found")
	}
	if call.ToolName != "Bash" {
		t.Errorf("ToolName = %q", call.ToolName)
	}
	if !call.ResultIsError {
		t.Error("ResultIsError = false, want true (closed by an \"error\" entry)")
	}
	if call.ResultText == "" {
		t.Error("ResultText is empty, want the error message")
	}

	// The two heartbeat "system" entries must not have become steps or split
	// the trajectory in two.
	var toolUseCount int
	for _, s := range traj.Steps {
		if s.Kind == StepToolUse {
			toolUseCount++
		}
	}
	if toolUseCount != 1 {
		t.Errorf("got %d tool_use steps, want 1 (heartbeats must not create steps)", toolUseCount)
	}
}

// TestParseLogFile_Broken: a garbage line among otherwise well-formed entries
// must not abort parsing — the surrounding entries still come through, and
// the bad line is counted in Skipped.
func TestParseLogFile_Broken(t *testing.T) {
	traj, err := ParseLogFile(fixtureBroken)
	if err != nil {
		t.Fatalf("ParseLogFile: %v", err)
	}
	if traj.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", traj.Skipped)
	}
	var toolUseCount int
	for _, s := range traj.Steps {
		if s.Kind == StepToolUse {
			toolUseCount++
			if s.ResultText != "hi" {
				t.Errorf("ResultText = %q, want %q", s.ResultText, "hi")
			}
		}
	}
	if toolUseCount != 1 {
		t.Errorf("got %d tool_use steps, want 1", toolUseCount)
	}
}

func TestParseLogFile_MissingFile(t *testing.T) {
	if _, err := ParseLogFile("../../testdata/logfiles/does-not-exist.md"); err == nil {
		t.Fatal("expected an error for a missing file")
	}
}
