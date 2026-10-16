package session

import (
	"encoding/json"
	"strings"
	"testing"

	"claude-manager/internal/config"
)

// LEARN-TASKS.md LN-15: a context-triggered restart replaces --resume (which
// would re-send the very expensive prefix that caused the restart) with a
// compact distilled recap sent as the first user turn of a fresh,
// non-resumed process — but only when the session's own ContextHandoff flag
// is on. Off, checkContextRestart must never touch contextMonitor/
// pendingHandoff/contextRestartHit at all (the "flag off -> byte-identical
// behaviour" invariant).

// resultLineWithUsage builds a raw result stream-json line carrying the same
// usage/modelUsage shape context-growth.json's own final event uses (PLAN.md
// 18.1/18.2), so tests exercise checkContextRestart exactly as handleLine's
// EventResult branch would receive it.
func resultLineWithUsage(model string, inputTokens, contextWindow int) string {
	data, _ := json.Marshal(map[string]any{
		"type":           "result",
		"subtype":        "success",
		"result":         "done",
		"total_cost_usd": 0.1,
		"num_turns":      3,
		"usage": map[string]any{
			"input_tokens": inputTokens,
		},
		"modelUsage": map[string]any{
			model: map[string]any{
				"inputTokens":   inputTokens,
				"contextWindow": contextWindow,
			},
		},
	})
	return string(data)
}

func handoffCfg() *config.OptimizationSettings {
	return &config.OptimizationSettings{
		ContextRestartThreshold: 0.75,
		ContextWarnThreshold:    0.60,
		ContextRestartMode:      "fresh",
	}
}

func TestCheckContextRestart_FlagOffIsNoOp(t *testing.T) {
	s := New(Params{
		Config:       config.SessionConfig{Name: "s"}, // ContextHandoff: false
		Optimization: handoffCfg(),
	})

	done := s.handleLine(resultLineWithUsage("claude-sonnet-4-6", 155_000, 200_000), false)
	if !done {
		t.Fatal("a plain result event must still finish the turn")
	}
	if s.contextRestartHit.Load() {
		t.Error("contextRestartHit must stay false with ContextHandoff off")
	}
	s.mu.Lock()
	handoff := s.pendingHandoff
	s.mu.Unlock()
	if handoff != "" {
		t.Errorf("pendingHandoff = %q, want empty with ContextHandoff off", handoff)
	}
}

func TestCheckContextRestart_BelowThresholdIsNoOp(t *testing.T) {
	s := New(Params{
		Config:       config.SessionConfig{Name: "s", ContextHandoff: true},
		Optimization: handoffCfg(),
	})

	// 40% utilization — below both warn and restart thresholds.
	s.handleLine(resultLineWithUsage("claude-sonnet-4-6", 80_000, 200_000), false)
	if s.contextRestartHit.Load() {
		t.Error("contextRestartHit must stay false below the restart threshold")
	}
}

func TestCheckContextRestart_TriggersWithHandoffFn(t *testing.T) {
	var gotProject, gotSession, gotPath, gotCLISessionID, gotTask string
	var gotTodos []string
	s := New(Params{
		ID:           "proj/s",
		ProjectName:  "proj",
		ProjectPath:  "/repo",
		Config:       config.SessionConfig{Name: "s", ContextHandoff: true},
		Optimization: handoffCfg(),
		HandoffFn: func(project, sessionName, projectPath, cliSessionID, taskDesc string, todos []string) (string, error) {
			gotProject, gotSession, gotPath, gotCLISessionID, gotTask = project, sessionName, projectPath, cliSessionID, taskDesc
			gotTodos = todos
			return "--- Session handoff (context restart) ---\nDone so far: X", nil
		},
	})
	s.mu.Lock()
	s.taskSourceDesc = "ROADMAP.md:1 | do X"
	s.todos = []TodoItem{{Content: "step one", Status: "completed"}, {Content: "step two", Status: "in_progress"}}
	cliSessionID := s.CLISessionID
	s.mu.Unlock()

	done := s.handleLine(resultLineWithUsage("claude-sonnet-4-6", 155_000, 200_000), false)
	if !done {
		t.Fatal("the turn that crosses the threshold still finishes normally")
	}
	if !s.contextRestartHit.Load() {
		t.Fatal("expected contextRestartHit to be armed")
	}
	s.mu.Lock()
	handoff := s.pendingHandoff
	s.mu.Unlock()
	if handoff == "" {
		t.Fatal("expected pendingHandoff to be set from HandoffFn's result")
	}

	if gotProject != "proj" || gotSession != "s" || gotPath != "/repo" || gotCLISessionID != cliSessionID {
		t.Errorf("HandoffFn identity args = (%q,%q,%q,%q), unexpected", gotProject, gotSession, gotPath, gotCLISessionID)
	}
	if gotTask != "ROADMAP.md:1 | do X" {
		t.Errorf("HandoffFn taskDesc = %q, want the session's taskSourceDesc", gotTask)
	}
	if len(gotTodos) != 2 || gotTodos[0] != "[x] step one" || gotTodos[1] != "[~] step two" {
		t.Errorf("HandoffFn todos = %v, want formatted checklist lines", gotTodos)
	}
}

// TestCheckContextRestart_TriggersWithoutHandoffFn verifies a restart still
// arms (the process still gets closed and restarted) even when no HandoffFn
// is wired — just without a distilled recap; initialPromptText then falls
// through to the session's normal prompt on the next runOnce.
func TestCheckContextRestart_TriggersWithoutHandoffFn(t *testing.T) {
	s := New(Params{
		Config:       config.SessionConfig{Name: "s", ContextHandoff: true},
		Optimization: handoffCfg(),
	})

	s.handleLine(resultLineWithUsage("claude-sonnet-4-6", 155_000, 200_000), false)
	if !s.contextRestartHit.Load() {
		t.Fatal("expected contextRestartHit to be armed even without a HandoffFn")
	}
	s.mu.Lock()
	handoff := s.pendingHandoff
	s.mu.Unlock()
	if handoff != "" {
		t.Errorf("pendingHandoff = %q, want empty with no HandoffFn wired", handoff)
	}
}

// TestCheckContextRestart_ScannerLoopClosesInputEvenWhenInteractive is a
// documentation-only assertion of the invariant the scanner loop in runOnce
// relies on: contextRestartHit alone (independent of the handleLine return
// value or the autonomous flag) is what must trigger closeInput for an
// interactive session, which otherwise never closes stdin on a finished
// turn. Exercised for real by the context-handoff e2e scenario; this test
// just locks in that handleLine's own return value is orthogonal to it.
func TestCheckContextRestart_ScannerLoopClosesInputEvenWhenInteractive(t *testing.T) {
	s := New(Params{
		Config:       config.SessionConfig{Name: "s", ContextHandoff: true},
		Optimization: handoffCfg(),
	})
	done := s.handleLine(resultLineWithUsage("claude-sonnet-4-6", 155_000, 200_000), false /* not autonomous */)
	if !done {
		t.Fatal("expected the finished-turn return value regardless of autonomy")
	}
	if !s.contextRestartHit.Load() {
		t.Fatal("expected contextRestartHit to be armed for an interactive session too")
	}
}

func TestInitialPromptText_Handoff_TakesPriorityOverNormalPrompt(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{Prompt: "do the thing", ContextHandoff: true},
	})
	s.mu.Lock()
	s.pendingHandoff = "--- Session handoff (context restart) ---\nDone so far: X"
	s.mu.Unlock()

	got := s.initialPromptText(false)
	if got != "--- Session handoff (context restart) ---\nDone so far: X" {
		t.Errorf("got %q, want the pending handoff verbatim", got)
	}

	// Consumed: a second call must fall back to the normal prompt.
	got2 := s.initialPromptText(false)
	if got2 != "do the thing" {
		t.Errorf("second call got %q, want the normal prompt (handoff must be cleared after use)", got2)
	}
}

func TestInitialPromptText_Handoff_CrashRecoveryStillWins(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{Prompt: "do the thing"},
	})
	s.mu.Lock()
	s.resumeSessionID = "saved-cli-id"
	s.pendingHandoff = "--- Session handoff (context restart) ---\nDone so far: X"
	s.mu.Unlock()

	got := s.initialPromptText(false)
	if got == "--- Session handoff (context restart) ---\nDone so far: X" {
		t.Error("crash recovery must take priority over a pending handoff (LEARN-TASKS.md LN-15 invariant)")
	}
}

func TestInitialPromptText_Handoff_FlagOffByteIdenticalWhenUnset(t *testing.T) {
	s := New(Params{
		Config: config.SessionConfig{Prompt: "do the thing"}, // ContextHandoff off, never set
	})
	got := s.initialPromptText(false)
	if got != "do the thing" {
		t.Errorf("got %q, want the plain configured prompt unchanged", got)
	}
}

func TestFormatTodoLines(t *testing.T) {
	got := formatTodoLines([]TodoItem{
		{Content: "a", Status: "completed"},
		{Content: "b", Status: "in_progress"},
		{Content: "c", Status: "pending"},
	})
	want := []string{"[x] a", "[~] b", "[ ] c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestFormatTodoLines_Empty(t *testing.T) {
	if got := formatTodoLines(nil); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func modeCfg(mode string) *config.OptimizationSettings {
	c := handoffCfg()
	c.ContextRestartMode = mode
	return c
}

// overThreshold is a result line at 77.5% of a 200k window.
var overThreshold = resultLineWithUsage("claude-sonnet-4-6", 155_000, 200_000)

func TestCheckContextRestart_ResumeCompactsInPlace(t *testing.T) {
	s := New(Params{
		Config:       config.SessionConfig{Name: "s", ContextHandoff: true},
		Optimization: modeCfg("resume"),
	})
	in := make(chan []byte, 4)
	s.mu.Lock()
	s.inputCh = in
	s.mu.Unlock()

	s.handleLine(overThreshold, false)
	if s.contextRestartHit.Load() {
		t.Fatal("resume must keep the process, not restart it")
	}
	select {
	case line := <-in:
		if !strings.Contains(string(line), `"/compact"`) {
			t.Fatalf("sent %s, want a /compact user turn", line)
		}
	default:
		t.Fatal("expected /compact on stdin")
	}

	// The /compact turn's own result is still big: no second compaction.
	// A turn back under the threshold re-arms; the next crossing compacts.
	s.handleLine(overThreshold, false)
	if len(in) != 0 {
		t.Fatal("the /compact turn's own result must not trigger another compaction")
	}
	s.handleLine(resultLineWithUsage("claude-sonnet-4-6", 30_000, 200_000), false)
	s.handleLine(overThreshold, false)
	if len(in) != 1 {
		t.Fatal("a crossing after compaction must compact again")
	}
}

func TestCheckContextRestart_ResumeWithoutStdinFallsBackToRestart(t *testing.T) {
	s := New(Params{
		Config:       config.SessionConfig{Name: "s", ContextHandoff: true},
		Optimization: modeCfg("resume"),
	})
	s.handleLine(overThreshold, false)
	if !s.contextRestartHit.Load() {
		t.Fatal("with no live stdin, resume must fall back to a recap restart")
	}
}

func TestCheckContextRestart_AutonomousAlwaysFresh(t *testing.T) {
	for _, mode := range []string{"resume", "ask"} {
		s := New(Params{
			Config:       config.SessionConfig{Name: "s", ContextHandoff: true},
			Optimization: modeCfg(mode),
		})
		s.handleLine(overThreshold, true)
		if !s.contextRestartHit.Load() {
			t.Errorf("mode %q: an autonomous run must restart fresh", mode)
		}
		if s.PendingQuestion() != nil {
			t.Errorf("mode %q: an autonomous run must not ask", mode)
		}
	}
}

func TestCheckContextRestart_AskAnswers(t *testing.T) {
	cases := []struct {
		answer      string
		wantRestart bool
		wantCompact bool
	}{
		{contextRestartOptions[0], false, true},
		{contextRestartOptions[1], true, false},
		{contextRestartOptions[2], false, false},
	}
	for _, c := range cases {
		s := New(Params{
			Config:       config.SessionConfig{Name: "s", ContextHandoff: true},
			Optimization: modeCfg("ask"),
		})
		in := make(chan []byte, 4)
		closed := make(chan struct{})
		s.mu.Lock()
		s.inputCh = in
		s.endInput = func() { close(closed) }
		s.mu.Unlock()

		s.handleLine(overThreshold, false)
		pq := s.PendingQuestion()
		if pq == nil || len(pq.Options) != 3 {
			t.Fatalf("expected a three-option question, got %+v", pq)
		}
		if s.contextRestartHit.Load() || len(in) != 0 {
			t.Fatal("nothing may happen before the user answers")
		}
		if err := s.AnswerQuestion(pq.ID, c.answer); err != nil {
			t.Fatal(err)
		}
		if c.wantRestart {
			<-closed // restart runs off the binding call
			if !s.contextRestartHit.Load() {
				t.Errorf("%q: expected a restart", c.answer)
			}
		}
		if got := len(in) == 1; got != c.wantCompact {
			t.Errorf("%q: /compact sent = %v, want %v", c.answer, got, c.wantCompact)
		}
		if !c.wantRestart && s.contextRestartHit.Load() {
			t.Errorf("%q: unexpected restart", c.answer)
		}
		if s.PendingQuestion() != nil {
			t.Errorf("%q: question still pending", c.answer)
		}
	}
}

// CTX-01: result.usage sums every call of the turn; the restart check must
// use the last call's usage.
func TestCheckContextRestart_UsesLastCallNotResultSum(t *testing.T) {
	newS := func() *Session {
		return New(Params{
			ID:           "proj/s",
			ProjectName:  "proj",
			Config:       config.SessionConfig{Name: "s", ContextHandoff: true},
			Optimization: &config.OptimizationSettings{ContextRestartThreshold: 0.5, ContextWarnThreshold: 0.4, ContextRestartMode: "fresh"},
			HandoffFn: func(_, _, _, _, _ string, _ []string) (string, error) {
				return "recap", nil
			},
		})
	}

	// Three calls of 40K (20%); the result sums to 120K (60% > 0.5).
	s := newS()
	for i := 0; i < 3; i++ {
		s.handleLine(assistantUsageLine(40_000), false)
	}
	s.handleLine(resultLineWithUsage("claude-sonnet-4-6", 120_000, 200_000), false)
	if s.contextRestartHit.Load() {
		t.Error("restart fired from the summed usage; the last call is only 20%")
	}

	// Last call above the threshold: fires.
	s = newS()
	s.handleLine(assistantUsageLine(40_000), false)
	s.handleLine(assistantUsageLine(110_000), false)
	s.handleLine(resultLineWithUsage("claude-sonnet-4-6", 150_000, 200_000), false)
	if !s.contextRestartHit.Load() {
		t.Error("restart should fire when the last call is above the threshold")
	}
}
