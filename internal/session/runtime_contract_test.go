// Package session — runtime contract tests (RUNTIME-TASKS.md RT-07).
//
// A single table-driven suite runs the SAME assertions against every
// Runtime this codebase ships (Claude via fakeclaude, Hermes via
// fakehermes): init carries a conversation id, a tool call/result pair
// shares one id, agent text reaches the log, an ask-user question is
// delivered and its answer(s) resume the conversation, a turn ends with
// token usage, each TurnFailure Kind the runtime is able to produce (RT-04,
// docs/runtimes.md §10) is classified correctly, and the conversation
// survives a process restart (crash recovery / per-turn --resume). A new
// runtime is only "connected" (docs/runtimes.md §7) once it passes this
// whole file — see contractRuntimes below for how to add one.
package session

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// contractCase is one runtime under test: how to build a Session that talks
// to it, and prompts that steer its fake binary into each contract
// scenario. Both fakeclaude (directory-mode, matched by prompt keyword
// regex, see testdata/scenarios/scenarios_doc.md) and fakehermes (matched by
// substrings in the query, see cmd/fakehermes's own doc comment) already
// pick their scripted behaviour from the prompt text, so a contract case is
// just "what prompt gets me scenario X for this runtime".
type contractCase struct {
	name string
	// newSession builds a fresh Session wired to this runtime's fake
	// binary, with prompt as its (initial) task/message and onEvent as the
	// event sink. autoRestart selects an autonomous run (task_source-style
	// loop) vs. an interactive one — both process models are exercised by
	// separate subtests below.
	newSession func(t *testing.T, prompt string, onEvent EventCallback, autoRestart bool) *Session

	// Prompts that select each scripted scenario in the runtime's fake
	// binary. Empty string means the runtime has no way to trigger that
	// Kind (see docs/runtimes.md §10 — not every Kind exists on every
	// runtime) and the subtest is skipped for it.
	happyPrompt string // a plain turn: init, text, usage, result — no tools

	toolPrompt string // a turn whose scenario includes a tool_use/tool_result pair

	askUserPrompt string
	// askUserAnswers is consumed one per question, in order (cycling on the
	// last entry if more questions arrive than answers listed) — Claude's
	// marker asks exactly one question, Hermes's clarify scenario asks two.
	askUserAnswers    []string
	askUserAnswerWant string // substring the final turn's reply must contain

	rateLimitPrompt string
	authPrompt      string
	sessionNotFound string // Hermes only
	stepLimitPrompt string // Claude only (Hermes's is a state.db scan, see task_outcome_test.go)

	resumePrompt1     string // first turn of a two-turn interactive conversation
	resumePrompt2     string
	resumePrompt2Want string // substring the SECOND turn's reply text must contain, proving continuity
}

// contractRuntimes is the list this file's TestContract_* functions range
// over. Adding a runtime here (plus a fake binary and the prompts to drive
// it into each scenario) is what "passes the RT-07 contract" means —
// docs/runtimes.md §7 point 6 refers back to this table.
func contractRuntimes(t *testing.T) []contractCase {
	t.Helper()
	claudeBin := buildFakeclaudeForSession(t)
	hermesBin := buildFakehermes(t)
	scenarioDir, err := filepath.Abs(filepath.Join("..", "..", "testdata", "scenarios"))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}

	newClaudeSession := func(t *testing.T, prompt string, onEvent EventCallback, autoRestart bool) *Session {
		t.Setenv("FAKECLAUDE_SCENARIO", scenarioDir)
		cfg := config.SessionConfig{
			Name: "C", Model: "sonnet", PermissionMode: "bypassPermissions",
			Prompt: prompt,
		}
		if autoRestart {
			cfg.AutoRestart = true
			cfg.MaxTasks = 1
		}
		s := New(Params{
			ID: "ct/claude", ProjectName: "ct", ProjectPath: t.TempDir(), ClaudePath: claudeBin,
			RetryDelay: 1, RateLimitPauseSec: 1,
			Config: cfg, OnEvent: onEvent,
		})
		return s
	}
	newHermesSession := func(t *testing.T, prompt string, onEvent EventCallback, autoRestart bool) *Session {
		cfg := config.SessionConfig{
			Name: "H", Runtime: "hermes", Model: "m", PermissionMode: "bypassPermissions",
			Prompt: prompt,
		}
		if autoRestart {
			cfg.AutoRestart = true
			cfg.MaxTasks = 1
		}
		return New(Params{
			ID: "ct/hermes", ProjectName: "ct", ProjectPath: t.TempDir(), HermesPath: hermesBin,
			RetryDelay: 1, RateLimitPauseSec: 1,
			Config: cfg, OnEvent: onEvent,
		})
	}

	return []contractCase{
		{
			name:              "claude",
			newSession:        newClaudeSession,
			happyPrompt:       "simple",
			toolPrompt:        "feed-demo",
			askUserPrompt:     "ask-user-marker please",
			askUserAnswers:    []string{"Keep 2ms"},
			askUserAnswerWant: "proceeding with the chosen budget",
			rateLimitPrompt:   "rate-limit-exceeded-demo",
			authPrompt:        "auth-error-demo",
			stepLimitPrompt:   "step-limit-demo",
			resumePrompt1:     "resume-demo",
			resumePrompt2:     "resume-demo turn 2",
			resumePrompt2Want: "step 2",
		},
		{
			name:              "hermes",
			newSession:        newHermesSession,
			happyPrompt:       "hello there",
			toolPrompt:        "hello there", // fakehermes always emits a terminal tool_use/result
			askUserPrompt:     "ASK_CLARIFY please",
			askUserAnswers:    []string{"proxy", "no"},
			askUserAnswerWant: "The user's answers",
			rateLimitPrompt:   "FAIL_429",
			authPrompt:        "FAIL_403",
			sessionNotFound:   "FAIL_SESSION_NOT_FOUND",
			resumePrompt1:     "first",
			resumePrompt2:     "second turn text",
			resumePrompt2Want: "echo: second turn text",
		},
	}
}

// waitFor drains ch for the next value or fails the test after timeout —
// shared helper for every subtest below, which all funnel one kind of
// SessionEvent into a channel from onEvent.
func waitFor[T any](t *testing.T, ch chan T, timeout time.Duration) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(timeout):
		t.Fatal("timed out waiting for event")
	}
	var zero T
	return zero
}

// TestContract_InitCarriesConversationID: every runtime's first event is an
// EvtInit whose Init.SessionID is non-empty — the id later turns resume by.
func TestContract_InitCarriesConversationID(t *testing.T) {
	for _, rc := range contractRuntimes(t) {
		rc := rc
		t.Run(rc.name, func(t *testing.T) {
			inits := make(chan *InitInfo, 4)
			s := rc.newSession(t, rc.happyPrompt, func(_ string, ev SessionEvent) {
				if ev.Type == EvtInit {
					inits <- ev.Init
				}
			}, true)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s.Run(ctx)

			init := waitFor(t, inits, 1*time.Second)
			if init == nil || init.SessionID == "" {
				t.Fatalf("Init = %+v, want a non-empty SessionID", init)
			}
		})
	}
}

// TestContract_ToolCallAndResultShareID: a tool_use/tool_result pair is
// linked by ToolUseID and the result carries a non-negative DurationMs
// (Claude derives it from wall-clock via toolTimer; Hermes reports it on
// the wire) — the UI's tool-call rendering (VIEW-TASKS.md UI-04) depends on
// both runtimes producing this shape identically.
func TestContract_ToolCallAndResultShareID(t *testing.T) {
	for _, rc := range contractRuntimes(t) {
		rc := rc
		t.Run(rc.name, func(t *testing.T) {
			var mu sync.Mutex
			var toolEntries []config.LogEntry
			s := rc.newSession(t, rc.toolPrompt, func(_ string, ev SessionEvent) {
				if ev.Type == EvtLog && ev.Entry != nil &&
					(ev.Entry.Level == "tool" || ev.Entry.Level == "tool_result" || ev.Entry.Level == "error") {
					mu.Lock()
					toolEntries = append(toolEntries, *ev.Entry)
					mu.Unlock()
				}
			}, true)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s.Run(ctx)

			mu.Lock()
			defer mu.Unlock()
			var call, result *config.LogEntry
			for i := range toolEntries {
				e := &toolEntries[i]
				if e.Level == "tool" && call == nil {
					call = e
				}
				if (e.Level == "tool_result" || e.Level == "error") && result == nil {
					result = e
				}
			}
			if call == nil || result == nil {
				t.Fatalf("tool entries = %+v, want a tool + tool_result/error pair", toolEntries)
			}
			if call.ToolUseID == "" || call.ToolUseID != result.ToolUseID {
				t.Fatalf("call.ToolUseID=%q result.ToolUseID=%q, want equal and non-empty",
					call.ToolUseID, result.ToolUseID)
			}
			if result.DurationMs < 0 {
				t.Errorf("DurationMs = %d, want >= 0", result.DurationMs)
			}
		})
	}
}

// TestContract_AgentTextReachesLog: the agent's own reply text lands on an
// EvtLog entry with Level "text" (Hermes coalesces streamed deltas into
// one; Claude's fixture scenarios speak in whole `assistant` messages —
// either way the same log shape reaches the UI).
func TestContract_AgentTextReachesLog(t *testing.T) {
	for _, rc := range contractRuntimes(t) {
		rc := rc
		t.Run(rc.name, func(t *testing.T) {
			var mu sync.Mutex
			var texts []string
			s := rc.newSession(t, rc.happyPrompt, func(_ string, ev SessionEvent) {
				if ev.Type == EvtLog && ev.Entry != nil && ev.Entry.Level == "text" {
					mu.Lock()
					texts = append(texts, ev.Entry.Message)
					mu.Unlock()
				}
			}, true)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s.Run(ctx)

			mu.Lock()
			defer mu.Unlock()
			if len(texts) == 0 {
				t.Fatal("no text-level EvtLog entry — the agent's reply never reached the log")
			}
		})
	}
}

// TestContract_QuestionDeliveredAndAnswerResumes: an ask-user question (the
// marker for Claude, `clarify` for Hermes) reaches EvtQuestion, pauses the
// session at StatusWaitingForUser, and AnswerQuestion resumes the SAME
// conversation. Every question the scenario asks is answered in order
// (askUserAnswers); once the final turn's reply contains askUserAnswerWant
// the round trip is confirmed complete.
func TestContract_QuestionDeliveredAndAnswerResumes(t *testing.T) {
	for _, rc := range contractRuntimes(t) {
		rc := rc
		if rc.askUserPrompt == "" {
			continue
		}
		t.Run(rc.name, func(t *testing.T) {
			questions := make(chan PendingQuestion, 8)
			texts := make(chan string, 8)
			s := rc.newSession(t, rc.askUserPrompt, func(_ string, ev SessionEvent) {
				switch ev.Type {
				case EvtQuestion:
					questions <- *ev.Question
				case EvtLog:
					if ev.Entry != nil && ev.Entry.Level == "text" {
						texts <- ev.Entry.Message
					}
				}
			}, true)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			done := make(chan struct{})
			go func() { s.Run(ctx); close(done) }()

			answered := 0
			deadline := time.After(25 * time.Second)
			var found bool
		loop:
			for !found {
				select {
				case q := <-questions:
					if q.Question == "" {
						t.Fatalf("question = %+v, want non-empty text", q)
					}
					if st := s.Status(); st != config.StatusWaitingForUser {
						t.Errorf("status = %s, want waiting_for_user", st)
					}
					answer := rc.askUserAnswers[min(answered, len(rc.askUserAnswers)-1)]
					answered++
					if err := s.AnswerQuestion(q.ID, answer); err != nil {
						t.Fatalf("AnswerQuestion: %v", err)
					}
				case txt := <-texts:
					if strings.Contains(txt, rc.askUserAnswerWant) {
						found = true
						break loop
					}
				case <-deadline:
					t.Fatal("timed out waiting for the answered reply")
				}
			}

			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("Run did not finish the task")
			}
		})
	}
}

// TestContract_TurnEndsWithTokenUsage: every runtime emits at least one
// EvtUsage carrying non-zero token counts by the time the turn's result
// arrives (Claude: per assistant message; Hermes: once, synthesized from
// the result's own tokens — docs/runtimes.md §6).
func TestContract_TurnEndsWithTokenUsage(t *testing.T) {
	for _, rc := range contractRuntimes(t) {
		rc := rc
		t.Run(rc.name, func(t *testing.T) {
			usage := make(chan TokenUsage, 8)
			s := rc.newSession(t, rc.happyPrompt, func(_ string, ev SessionEvent) {
				if ev.Type == EvtUsage {
					usage <- *ev.Usage
				}
			}, true)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			s.Run(ctx)

			u := waitFor(t, usage, 1*time.Second)
			if u.InputTokens <= 0 && u.OutputTokens <= 0 {
				t.Errorf("usage = %+v, want non-zero token counts", u)
			}
		})
	}
}

// TestContract_FailureKinds runs one subtest per TurnFailure.Kind
// (RUNTIME-TASKS.md RT-04) the runtime is able to produce, asserting the
// matching EvtRateLimit/session atomic fires. Kinds with no prompt set for
// a given runtime (contractCase's doc comment) are skipped for it —
// context_restart is intentionally absent here: it is runtime-agnostic
// (checkContextRestart reads SessionResult.ModelUsage, see
// docs/runtimes.md §10) and already covered by session_contexthandoff_test.go
// without needing a second per-runtime copy.
func TestContract_FailureKinds(t *testing.T) {
	for _, rc := range contractRuntimes(t) {
		rc := rc
		t.Run(rc.name+"/rate_limit", func(t *testing.T) {
			if rc.rateLimitPrompt == "" {
				t.Skip("no rate-limit prompt for this runtime")
			}
			var limited bool
			s := rc.newSession(t, rc.rateLimitPrompt, func(_ string, ev SessionEvent) {
				if ev.Type == EvtRateLimit {
					limited = true
				}
			}, false)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = s.runOnce(ctx, false)
			if !limited {
				t.Error("no EvtRateLimit emitted for a rate-limit scenario")
			}
		})

		t.Run(rc.name+"/auth", func(t *testing.T) {
			if rc.authPrompt == "" {
				t.Skip("no auth-error prompt for this runtime")
			}
			s := rc.newSession(t, rc.authPrompt, nil, false)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = s.runOnce(ctx, false)
			if !s.authErrorHit.Load() {
				t.Error("authErrorHit not set for an auth-error scenario")
			}
		})

		t.Run(rc.name+"/session_not_found", func(t *testing.T) {
			if rc.sessionNotFound == "" {
				t.Skip("no session-not-found prompt for this runtime")
			}
			s := rc.newSession(t, rc.sessionNotFound, nil, false)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if err := s.runOnce(ctx, false); err != errSessionNotFound {
				t.Errorf("runOnce err = %v, want errSessionNotFound", err)
			}
		})

		t.Run(rc.name+"/step_limit", func(t *testing.T) {
			if rc.stepLimitPrompt == "" {
				t.Skip("no step-limit prompt for this runtime")
			}
			s := rc.newSession(t, rc.stepLimitPrompt, nil, false)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_ = s.runOnce(ctx, false)
			if !s.stepLimitHit.Load() {
				t.Error("stepLimitHit not set for a step-limit scenario")
			}
		})
	}
}

// TestContract_ConversationSurvivesRestart: a second user turn's reply
// still reflects the first turn's content, proving the conversation is a
// single continuous exchange — the defining property of --resume for
// Hermes (a brand new process every turn) and simply what a long-lived
// process already gives Claude for free. Deliberately asserts on log TEXT,
// not EvtResult: Claude's single long-lived process only emits EventResult
// once, at the very end of the whole interactive session, while Hermes
// emits one per turn (docs/runtimes.md §4) — that process-model difference
// is real and NOT part of this shared contract (§11, PerTurnProcess is a
// property callers may branch on, not something every runtime must match).
func TestContract_ConversationSurvivesRestart(t *testing.T) {
	for _, rc := range contractRuntimes(t) {
		rc := rc
		t.Run(rc.name, func(t *testing.T) {
			texts := make(chan string, 8)
			s := rc.newSession(t, rc.resumePrompt1, func(_ string, ev SessionEvent) {
				if ev.Type == EvtLog && ev.Entry != nil && ev.Entry.Level == "text" {
					texts <- ev.Entry.Message
				}
			}, false)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			done := make(chan struct{})
			go func() { s.Run(ctx); close(done) }()

			waitFor(t, texts, 20*time.Second) // first turn's reply

			if err := s.SendMessage(rc.resumePrompt2); err != nil {
				t.Fatalf("SendMessage: %v", err)
			}
			t2 := waitFor(t, texts, 20*time.Second)
			if !strings.Contains(t2, rc.resumePrompt2Want) {
				t.Errorf("second turn text = %q, want it to contain %q", t2, rc.resumePrompt2Want)
			}

			s.Stop(false)
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Fatal("Run did not exit after Stop")
			}
		})
	}
}
