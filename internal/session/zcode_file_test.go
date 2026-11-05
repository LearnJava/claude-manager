package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claude-manager/internal/config"
)

// eventLog collects emitted events from the run goroutine; tests snapshot
// it from theirs.
type eventLog struct {
	mu  sync.Mutex
	evs []SessionEvent
}

func (e *eventLog) add(ev SessionEvent) {
	e.mu.Lock()
	e.evs = append(e.evs, ev)
	e.mu.Unlock()
}

func (e *eventLog) snapshot() []SessionEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]SessionEvent(nil), e.evs...)
}

// newZcodeTestSession builds a zcode-runtime session rooted at a temp dir
// and collects every emitted event.
func newZcodeTestSession(t *testing.T) (*Session, *eventLog) {
	t.Helper()
	log := &eventLog{}
	s := New(Params{
		ID: "zcode/S1", ProjectName: "zcode", ProjectPath: t.TempDir(),
		RetryDelay: 1,
		Config: config.SessionConfig{
			Name: "S1", Runtime: config.RuntimeZcode,
			Prompt: "Ты программист 1.",
		},
		OnEvent: func(_ string, ev SessionEvent) {
			log.add(ev)
		},
	})
	return s, log
}

// waitForOrder blocks until exactly one order file appears in the inbox.
func waitForOrder(t *testing.T, dir string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		entries, _ := os.ReadDir(filepath.Join(dir, ".zsm", "inbox"))
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".json") {
				return filepath.Join(dir, ".zsm", "inbox", e.Name())
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("no .zsm order file appeared")
	return ""
}

// outboxPathFor derives the outbox path from the order path (same id).
func outboxPathFor(orderPath string) string {
	return filepath.Join(filepath.Dir(filepath.Dir(orderPath)), "outbox", filepath.Base(orderPath))
}

func writeOutbox(t *testing.T, orderPath, body string) {
	t.Helper()
	if err := os.WriteFile(outboxPathFor(orderPath), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readOrderMessages(t *testing.T, orderPath string) []zcodeMessage {
	t.Helper()
	data, err := os.ReadFile(orderPath)
	if err != nil {
		t.Fatal(err)
	}
	var order zcodeOrder
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatal(err)
	}
	return order.Messages
}

func TestZcodeRunTurn_FullFlow(t *testing.T) {
	s, log := newZcodeTestSession(t)
	rt, ok := s.runtime.(TurnRunner)
	if !ok {
		t.Fatal("zcode runtime does not implement TurnRunner")
	}

	type result struct {
		turn hermesTurn
		err  error
	}
	done := make(chan result, 1)
	go func() {
		turn, err := rt.RunTurn(s, context.Background(), "Сделай задачу 1", nil, "", false)
		done <- result{turn, err}
	}()

	orderPath := waitForOrder(t, s.ProjectPath)

	// The order must carry the prompt and resume=false for a fresh turn.
	data, _ := os.ReadFile(orderPath)
	var order zcodeOrder
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatalf("order is not valid JSON: %v", err)
	}
	if order.Prompt != "Сделай задачу 1" || order.Resume || len(order.Messages) != 0 {
		t.Fatalf("order = %+v", order)
	}
	// The test session has no effort configured: omitempty must keep the
	// manager extension out of the wire format (zsm consumers ignore unknown
	// keys, but an absent key keeps the file byte-identical to zsm's).
	if order.ReasoningEffort != "" || strings.Contains(string(data), "reasoning_effort") {
		t.Errorf("empty effort leaked into the order: %q", order.ReasoningEffort)
	}

	writeOutbox(t, orderPath, `{"state":"working","conversation_id":"zc-42",
		"tool_calls":[{"tool":"Bash","summary":"cargo build","ok":true,"duration_ms":120}],
		"usage":{"input":100,"output":50,"cache_read":10,"cache_write":5}}`)
	// The working state's payload is translated on the state change — wait
	// for it before overwriting the outbox with the terminal state.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range log.snapshot() {
			if ev.Type == EvtLog && ev.Entry != nil &&
				ev.Entry.Level == "tool" && ev.Entry.ToolName == "Bash" {
				goto workingSeen
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the working state's tool call was never translated")
workingSeen:
	writeOutbox(t, orderPath, `{"state":"done","conversation_id":"zc-42","text":"Задача 1 готова"}`)

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("RunTurn: %v", r.err)
		}
		if r.turn.failed != "" {
			t.Errorf("turn.failed = %q, want empty", r.turn.failed)
		}
		if r.turn.sessionID != "zc-42" {
			t.Errorf("turn.sessionID = %q, want zc-42", r.turn.sessionID)
		}
		if !r.turn.finished {
			t.Error("turn not reported as finished")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunTurn did not finish")
	}

	// The order file is cleaned up after the turn (the agent's stop signal).
	if _, err := os.Stat(orderPath); !os.IsNotExist(err) {
		t.Errorf("order file still exists after the turn (stat err = %v)", err)
	}

	var sawInit, sawTool, sawUsage, sawResult bool
	var resultText string
	for _, ev := range log.snapshot() {
		switch ev.Type {
		case EvtInit:
			sawInit = ev.Init != nil && ev.Init.SessionID == "zc-42"
		case EvtUsage:
			if ev.Usage != nil && ev.Usage.InputTokens == 100 {
				sawUsage = true
			}
		case EvtLog:
			if ev.Entry != nil && ev.Entry.Level == "tool" && ev.Entry.ToolName == "Bash" {
				sawTool = true
			}
			if ev.Entry != nil && ev.Entry.Level == "result" {
				sawResult = true
				resultText = ev.Entry.Message
			}
		}
	}
	if !sawInit {
		t.Error("no init event with the outbox conversation id")
	}
	if !sawTool {
		t.Error("no tool log entry for the outbox tool call")
	}
	if !sawUsage {
		t.Error("no usage event for the outbox usage object")
	}
	if !sawResult || resultText != "Задача 1 готова" {
		t.Errorf("result log = (%v, %q)", sawResult, resultText)
	}
}

// The session's effort rides the order as reasoning_effort — the manager
// extension of the wire format (GLM-5.3: low, high, max).
func TestZcodeRunTurn_OrderCarriesEffort(t *testing.T) {
	s, _ := newZcodeTestSession(t)
	s.Config.Effort = "low"
	rt := s.runtime.(TurnRunner)

	done := make(chan error, 1)
	go func() {
		_, err := rt.RunTurn(s, context.Background(), "Задача", nil, "", false)
		done <- err
	}()

	orderPath := waitForOrder(t, s.ProjectPath)
	data, _ := os.ReadFile(orderPath)
	var order zcodeOrder
	if err := json.Unmarshal(data, &order); err != nil {
		t.Fatalf("order is not valid JSON: %v", err)
	}
	if order.ReasoningEffort != "low" {
		t.Errorf("order.reasoning_effort = %q, want low", order.ReasoningEffort)
	}

	writeOutbox(t, orderPath, `{"state":"done","conversation_id":"zc-1","text":"ок"}`)
	if err := <-done; err != nil {
		t.Fatalf("RunTurn: %v", err)
	}
}

func TestZcodeRunTurn_PermissionAnsweredInOrder(t *testing.T) {
	s, _ := newZcodeTestSession(t)
	rt := s.runtime.(TurnRunner)

	done := make(chan error, 1)
	go func() {
		_, err := rt.RunTurn(s, context.Background(), "задача", nil, "", false)
		done <- err
	}()
	orderPath := waitForOrder(t, s.ProjectPath)

	writeOutbox(t, orderPath, `{"state":"waiting_permission","conversation_id":"zc-1",
		"permission":{"id":"p1","tool":"Bash","summary":"rm -rf build"}}`)

	// The permission request must surface as pending.
	deadline := time.Now().Add(5 * time.Second)
	for s.PendingPermission() == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := s.PendingPermission(); got == nil {
		t.Fatal("permission request never became pending")
	} else if got.Tool != "Bash" || got.ID != "p1" {
		t.Fatalf("pending permission = %+v", got)
	}

	if err := s.RespondPermission("p1", "allow"); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	msgs := readOrderMessages(t, orderPath)
	if len(msgs) != 1 || msgs[0].Type != "permission_response" ||
		msgs[0].RequestID != "p1" || msgs[0].Decision != "allow" {
		t.Fatalf("order messages = %+v", msgs)
	}

	writeOutbox(t, orderPath, `{"state":"done","conversation_id":"zc-1","text":"ок"}`)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunTurn did not finish")
	}
	if s.PendingPermission() != nil {
		t.Error("permission still pending after the turn")
	}
}

func TestZcodeRunTurn_QuestionAnsweredInOrder(t *testing.T) {
	s, _ := newZcodeTestSession(t)
	rt := s.runtime.(TurnRunner)

	done := make(chan error, 1)
	go func() {
		_, err := rt.RunTurn(s, context.Background(), "задача", nil, "", false)
		done <- err
	}()
	orderPath := waitForOrder(t, s.ProjectPath)

	writeOutbox(t, orderPath, `{"state":"working","conversation_id":"zc-1",
		"question":{"id":"q1","text":"Какой вариант?"}}`)

	deadline := time.Now().Add(5 * time.Second)
	for s.PendingQuestion() == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if got := s.PendingQuestion(); got == nil {
		t.Fatal("question never became pending")
	} else if got.ID != "q1" || got.Question != "Какой вариант?" {
		t.Fatalf("pending question = %+v", got)
	}

	if err := s.AnswerQuestion("q1", "второй"); err != nil {
		t.Fatalf("AnswerQuestion: %v", err)
	}
	msgs := readOrderMessages(t, orderPath)
	if len(msgs) != 1 || msgs[0].Type != "question_response" ||
		msgs[0].QuestionID != "q1" || msgs[0].Text != "второй" {
		t.Fatalf("order messages = %+v", msgs)
	}

	// The agent moves past the question and finishes; the pending question
	// must clear so the run continues.
	writeOutbox(t, orderPath, `{"state":"done","conversation_id":"zc-1","text":"готово"}`)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunTurn: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunTurn did not finish")
	}
	if s.PendingQuestion() != nil {
		t.Error("question still pending after the turn")
	}
}

func TestZcodeRunTurn_FailedStatusFailsTurn(t *testing.T) {
	s, _ := newZcodeTestSession(t)
	rt := s.runtime.(TurnRunner)

	done := make(chan hermesTurn, 1)
	go func() {
		turn, _ := rt.RunTurn(s, context.Background(), "задача", nil, "", false)
		done <- turn
	}()
	orderPath := waitForOrder(t, s.ProjectPath)
	writeOutbox(t, orderPath, `{"state":"failed","conversation_id":"zc-1","text":"нет доступа к репозиторию"}`)

	select {
	case turn := <-done:
		if turn.failed != "нет доступа к репозиторию" {
			t.Errorf("turn.failed = %q", turn.failed)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("RunTurn did not finish")
	}
}

func TestZcodeRunTurn_CancelStopsAndCleansUp(t *testing.T) {
	s, _ := newZcodeTestSession(t)
	rt := s.runtime.(TurnRunner)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := rt.RunTurn(s, ctx, "задача", nil, "", false)
		done <- err
	}()
	orderPath := waitForOrder(t, s.ProjectPath)

	cancel()
	// The stop messages must land in the order — poll for both, since the
	// loop notices the cancellation only on its next tick and the two
	// appends land microseconds apart. Then the agent's done state releases
	// the turn within the grace window.
	var msgs []zcodeMessage
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if msgs = readOrderMessages(t, orderPath); len(msgs) >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(msgs) != 2 || msgs[0].Type != "soft_stop" || msgs[1].Type != "user_message" {
		t.Fatalf("stop messages = %+v", msgs)
	}
	writeOutbox(t, orderPath, `{"state":"done","conversation_id":"zc-1","text":"остановлен"}`)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled RunTurn returned %v, want nil", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("cancelled RunTurn did not return")
	}
	if _, err := os.Stat(orderPath); !os.IsNotExist(err) {
		t.Errorf("order file still exists after cancel (stat err = %v)", err)
	}
}

func TestZcodeRateLimit_ParsesResetAt(t *testing.T) {
	cases := []struct {
		resetAt string
		want    bool // whether ResetsAt should end up non-zero
	}{
		{"+90s", true},
		{"2026-11-05T18:00:00Z", true},
		{"23:59", true},
		{"garbage", false},
	}
	for _, c := range cases {
		info := zcodeRateLimit(&zcodeLimit{Kind: "rate", ResetAt: c.resetAt})
		if !info.throttled() {
			t.Errorf("resetAt=%q: limit not throttled", c.resetAt)
		}
		if c.want && info.ResetsAt == 0 {
			t.Errorf("resetAt=%q: ResetsAt = 0, want parsed", c.resetAt)
		}
		if !c.want && info.ResetsAt != 0 {
			t.Errorf("resetAt=%q: ResetsAt = %d, want 0", c.resetAt, info.ResetsAt)
		}
	}
}
