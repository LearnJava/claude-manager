package session

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// A task-source/auto_restart loop resets its CLI conversation every task, so a
// question asked in prose and never answered used to be silently lost. The
// ask-user marker fixes that, but per the one-session-per-task rule there is
// a real recurring case (observed live: "S8 merged — start S9 now in this
// same session?") where the answer is always the same and nobody should have
// to sit around answering it — that's KindContinueSession, handled instantly.
// Any other marker is a genuine decision: it pauses the run for a human, with
// a 5-minute (configurable) timeout that auto-picks the first listed option
// so an unattended run is never stuck forever.

func resultLineWithAskUserKind(question, kind string) string {
	payload := map[string]any{"question": question, "options": []string{"A", "B"}}
	if kind != "" {
		payload["kind"] = kind
	}
	q, _ := json.Marshal(payload)
	resultText := "Investigated the options.\n\n```ask-user\n" + string(q) + "\n```"
	data, _ := json.Marshal(map[string]any{
		"type":           "result",
		"subtype":        "success",
		"result":         resultText,
		"total_cost_usd": 0.1,
		"num_turns":      3,
	})
	return string(data)
}

func resultLineWithAskUser(question string) string {
	return resultLineWithAskUserKind(question, "")
}

func TestHandleLine_AskUserMarker_ContinueSessionAutoResolvesInstantly(t *testing.T) {
	var logEntries []config.LogEntry
	s := New(Params{
		ID:     "p/s",
		Config: config.SessionConfig{Name: "s", AutoRestart: true},
		OnEvent: func(_ string, ev SessionEvent) {
			if ev.Type == EvtLog && ev.Entry != nil {
				logEntries = append(logEntries, *ev.Entry)
			}
		},
	})

	done := s.handleLine(resultLineWithAskUserKind("Start S9 now?", KindContinueSession), true)
	if !done {
		t.Error("a continue_session marker must still finish the turn instantly — nobody waits for it")
	}
	if s.Status() == config.StatusWaitingForUser {
		t.Error("a continue_session marker must never pause the session")
	}
	if s.PendingQuestion() != nil {
		t.Error("a continue_session marker must not set a pending question")
	}

	var found bool
	for _, e := range logEntries {
		if strings.Contains(e.Message, "Start S9 now?") {
			found = true
		}
	}
	if !found {
		t.Error("expected the question text to be recorded as a log entry")
	}
}

func TestHandleLine_AskUserMarker_DefaultKindPausesTurn(t *testing.T) {
	var questionEvents []SessionEvent
	s := New(Params{
		ID:     "p/s",
		Config: config.SessionConfig{Name: "s", AutoRestart: true},
		OnEvent: func(_ string, ev SessionEvent) {
			if ev.Type == EvtQuestion {
				questionEvents = append(questionEvents, ev)
			}
		},
	})

	done := s.handleLine(resultLineWithAskUser("Which budget?"), true)
	if done {
		t.Error("a default-kind ask-user result must NOT report the turn as finished (stdin must stay open)")
	}
	if s.Status() != config.StatusWaitingForUser {
		t.Errorf("status = %s, want %s", s.Status(), config.StatusWaitingForUser)
	}
	if len(questionEvents) != 1 {
		t.Fatalf("expected 1 EvtQuestion, got %d", len(questionEvents))
	}
	q := questionEvents[0].Question
	if q == nil || q.Question != "Which budget?" {
		t.Errorf("unexpected question payload: %+v", q)
	}
	if pq := s.PendingQuestion(); pq == nil || pq.ID == "" {
		t.Errorf("PendingQuestion() = %+v, want a populated question with an ID", pq)
	}
}

func TestHandleLine_AskUserMarker_IgnoredWhenNotAutonomous(t *testing.T) {
	// Interactive sessions already have a human reading every reply directly,
	// so the marker convention is not worth pausing (or auto-resolving) for.
	s := New(Params{ID: "p/s", Config: config.SessionConfig{Name: "s"}})

	done := s.handleLine(resultLineWithAskUser("Which budget?"), false)
	if !done {
		t.Error("non-autonomous result must still report the turn as finished")
	}
	if s.PendingQuestion() != nil {
		t.Error("non-autonomous run must not set a pending question")
	}
}

func TestAnswerQuestion_ClearsAndReturnsToWorking(t *testing.T) {
	s := New(Params{ID: "p/s", Config: config.SessionConfig{Name: "s", AutoRestart: true}})
	s.handleLine(resultLineWithAskUser("Which budget?"), true)

	pq := s.PendingQuestion()
	if pq == nil {
		t.Fatal("expected a pending question")
	}

	if err := s.AnswerQuestion("wrong-id", "some answer"); err == nil {
		t.Error("expected error answering with a mismatched question id")
	}

	// No live input channel (no process running), so the write itself fails,
	// but the ID check must happen before that — assert the right error path.
	err := s.AnswerQuestion(pq.ID, "Keep the 2ms budget")
	if err == nil {
		t.Fatal("expected an error since no CLI process is actually running to accept stdin")
	}
	if s.PendingQuestion() != nil {
		t.Error("AnswerQuestion must clear the pending question even if the stdin write fails")
	}
}

func TestHandleLine_AskUserMarker_TimeoutAutoAnswersFirstOption(t *testing.T) {
	var logEntries []config.LogEntry
	s := New(Params{
		ID:                 "p/s",
		Config:             config.SessionConfig{Name: "s", AutoRestart: true},
		QuestionTimeoutSec: 1,
		OnEvent: func(_ string, ev SessionEvent) {
			if ev.Type == EvtLog && ev.Entry != nil {
				logEntries = append(logEntries, *ev.Entry)
			}
		},
	})

	if done := s.handleLine(resultLineWithAskUser("Which budget?"), true); done {
		t.Fatal("default-kind ask-user must pause, not finish, the turn")
	}
	if s.PendingQuestion() == nil {
		t.Fatal("expected a pending question before the timeout fires")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if s.PendingQuestion() == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	if s.PendingQuestion() != nil {
		t.Error("expected the timeout fallback to clear the pending question")
	}

	var sawTimeoutLog bool
	for _, e := range logEntries {
		if strings.Contains(e.Message, "auto-selecting the first option: A") {
			sawTimeoutLog = true
		}
	}
	if !sawTimeoutLog {
		t.Error("expected a log entry noting the auto-selected first option")
	}
}

func TestAnswerQuestion_CancelsTimeoutFallback(t *testing.T) {
	var logEntries []config.LogEntry
	s := New(Params{
		ID:                 "p/s",
		Config:             config.SessionConfig{Name: "s", AutoRestart: true},
		QuestionTimeoutSec: 1,
		OnEvent: func(_ string, ev SessionEvent) {
			if ev.Type == EvtLog && ev.Entry != nil {
				logEntries = append(logEntries, *ev.Entry)
			}
		},
	})

	s.handleLine(resultLineWithAskUser("Which budget?"), true)
	pq := s.PendingQuestion()
	if pq == nil {
		t.Fatal("expected a pending question")
	}
	_ = s.AnswerQuestion(pq.ID, "Keep the 2ms budget")

	// Give the (should-be-cancelled) timer time to fire if it wasn't stopped.
	time.Sleep(1500 * time.Millisecond)

	for _, e := range logEntries {
		if strings.Contains(e.Message, "No answer within") {
			t.Error("timeout fallback must not fire after a manual answer")
		}
	}
}
