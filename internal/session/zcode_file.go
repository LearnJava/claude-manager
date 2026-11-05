package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"claude-manager/internal/config"
	"claude-manager/internal/logger"
)

// The ZCode file transport, ported from zsm's engines/zcode_file.py
// (wire-compatible: the same files a ZCode session driven by zsm reads and
// writes). The manager starts no agent process; it creates the conditions
// under which a ZCode session running on its own executes the pipeline
// protocol:
//
//	.zsm/inbox/<order>.json  — the order for one turn: role, task pointer,
//	                           prompt; control messages are appended to its
//	                           "messages" array (user_message,
//	                           permission_response, question_response,
//	                           soft_stop — zsm events.py).
//	.zsm/outbox/<id>.json    — the status the agent writes (skills/hooks):
//	                           {"state": "working"|"waiting_permission"|
//	                            "done"|"failed", "text": "...",
//	                            "continuation": false, "usage": {...},
//	                            "permission": {...}, "question": {...},
//	                            "limit": {...}, "tool_calls": [...]}
//
// The turn polls the outbox and maps it onto the shared ParsedEvent format.
// When ZCode grows a working headless mode the sessions move to a process
// runtime without a protocol change.

// zcodePoll mirrors zsm's ZCodeFileEngine.POLL: how often the outbox status
// file is re-read.
const zcodePoll = 1 * time.Second

// zcodePickupWarnAfter is how long a turn waits for the first outbox file
// before logging a one-shot hint — the usual cause is no ZCode session
// watching .zsm/inbox in this workdir.
const zcodePickupWarnAfter = 60 * time.Second

// zcodeStopGrace bounds how long a cancelled turn keeps polling after the
// stop messages were appended to the order — zsm's soft→hard stairway,
// shrunk: on this transport removing the order file is always available as
// the force stop.
const zcodeStopGrace = 10 * time.Second

// zcodeMessage is one entry of the order file's "messages" array — the
// manager→agent wire of the file transport (zsm events.py).
type zcodeMessage struct {
	Type string `json:"type"` // user_message | permission_response | question_response | soft_stop
	// Text carries user_message.text and question_response.text.
	Text string `json:"text,omitempty"`
	// RequestID + Decision + Always carry permission_response
	// (decision: allow|deny).
	RequestID string `json:"request_id,omitempty"`
	Decision  string `json:"decision,omitempty"`
	Always    bool   `json:"always,omitempty"`
	// QuestionID carries question_response.
	QuestionID string  `json:"question_id,omitempty"`
	At         float64 `json:"at"`
}

// zcodeOrder is the inbox file: the order for one turn, wire-compatible
// with zsm's ZCodeFileEngine.start(). ReasoningEffort is a manager
// extension (absent in zsm's format): like Model it is carried for the
// consuming ZCode session, which applies it only if it can.
type zcodeOrder struct {
	ID              string         `json:"id"`
	ConversationID  string         `json:"conversation_id"`
	Resume          bool           `json:"resume"`
	Model           string         `json:"model,omitempty"`
	ReasoningEffort string         `json:"reasoning_effort,omitempty"`
	Prompt          string         `json:"prompt"`
	TaskPointer     string         `json:"task_pointer,omitempty"`
	QueueFile       string         `json:"queue_file,omitempty"`
	Messages        []zcodeMessage `json:"messages"`
	CreatedAt       float64        `json:"created_at"`
}

// zcodeStatus is the outbox file the ZCode session writes and the turn
// polls: the agent→manager wire of the file transport.
type zcodeStatus struct {
	State          string         `json:"state"` // working | waiting_permission | done | failed
	Text           string         `json:"text"`
	ConversationID string         `json:"conversation_id"`
	Continuation   bool           `json:"continuation"`
	Usage          *zcodeUsage    `json:"usage"`
	Permission     *zcodePerm     `json:"permission"`
	Question       *zcodeQuestion `json:"question"`
	Limit          *zcodeLimit    `json:"limit"`
	ToolCalls      []zcodeTool    `json:"tool_calls"`
}

// zcodeUsage is the outbox's usage object (zsm events.py turn_ended).
type zcodeUsage struct {
	Input        int `json:"input"`
	Output       int `json:"output"`
	CacheRead    int `json:"cache_read"`
	CacheWrite   int `json:"cache_write"`
	ContextUsed  int `json:"context_used"`
	ContextLimit int `json:"context_limit"`
}

func (u *zcodeUsage) tokenUsage() *TokenUsage {
	if u == nil {
		return nil
	}
	return &TokenUsage{
		InputTokens:              u.Input,
		OutputTokens:             u.Output,
		CacheReadInputTokens:     u.CacheRead,
		CacheCreationInputTokens: u.CacheWrite,
	}
}

type zcodePerm struct {
	ID      string `json:"id"`
	Tool    string `json:"tool"`
	Summary string `json:"summary"`
}

type zcodeQuestion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

// zcodeLimit is the outbox's limit object: kind rate|quota, reset_at as
// "HH:MM" | ISO-8601 | "+Ns" (zsm events.py limit).
type zcodeLimit struct {
	Kind    string `json:"kind"`
	ResetAt string `json:"reset_at"`
	Message string `json:"message"`
}

type zcodeTool struct {
	Tool       string `json:"tool"`
	Summary    string `json:"summary"`
	OK         bool   `json:"ok"`
	DurationMS int    `json:"duration_ms"`
}

// zcodeChannel tracks the current turn's file pair so mid-turn deliveries
// (permission answers, question answers, stop) can reach the order file.
type zcodeChannel struct {
	orderPath string
	mu        sync.Mutex // serializes order-file rewrites
}

// send appends one message to the order file (read-modify-write, like zsm's
// ZCodeFileEngine.send — the files are small).
func (c *zcodeChannel) send(msg zcodeMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	data, err := os.ReadFile(c.orderPath)
	if err != nil {
		return err
	}
	var order zcodeOrder
	if err := json.Unmarshal(data, &order); err != nil {
		return err
	}
	msg.At = float64(time.Now().UnixNano()) / 1e9
	order.Messages = append(order.Messages, msg)
	return writeZcodeOrder(c.orderPath, &order)
}

func writeZcodeOrder(path string, order *zcodeOrder) error {
	data, err := json.MarshalIndent(order, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// readZcodeStatus loads the outbox file, tolerating a missing file and a
// torn (half-written) JSON — both simply mean "try again next tick", exactly
// like zsm's _read_outbox.
func readZcodeStatus(path string) *zcodeStatus {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var st zcodeStatus
	if err := json.Unmarshal(data, &st); err != nil {
		return nil
	}
	return &st
}

// zcodeSend appends a message to the session's current zcode order file.
func (s *Session) zcodeSend(msg zcodeMessage) error {
	s.mu.Lock()
	ch := s.zcode
	s.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("session %s: no zcode order in flight", s.ID)
	}
	return ch.send(msg)
}

// DeliverPermission answers an outbox permission request by appending a
// permission_response to the order file (the file transport's equivalent of
// the stdin permission_response the Claude CLI takes). decision arrives
// already normalized to allow|deny from RespondPermission.
func (zcodeRuntime) DeliverPermission(s *Session, requestID, decision string) error {
	return s.zcodeSend(zcodeMessage{
		Type: "permission_response", RequestID: requestID, Decision: decision,
	})
}

// RunTurn writes one order file and polls its outbox until the agent reports
// done/failed, mapping every state change onto the shared ParsedEvent
// stream. It implements TurnRunner for zcodeRuntime.
func (zcodeRuntime) RunTurn(s *Session, ctx context.Context, prompt string, images []ImageAttachment, convID string, autonomous bool) (hermesTurn, error) {
	var turn hermesTurn
	if len(images) > 0 {
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "system", Source: "manager",
			Message: "zcode runtime does not accept image attachments in this build — sending text only",
		}})
	}

	workDir := s.runDir()
	inbox := filepath.Join(workDir, ".zsm", "inbox")
	outbox := filepath.Join(workDir, ".zsm", "outbox")
	if err := os.MkdirAll(inbox, 0o755); err != nil {
		return turn, fmt.Errorf("zcode inbox: %w", err)
	}
	if err := os.MkdirAll(outbox, 0o755); err != nil {
		return turn, fmt.Errorf("zcode outbox: %w", err)
	}

	orderID := strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	orderPath := filepath.Join(inbox, orderID+".json")
	outboxPath := filepath.Join(outbox, orderID+".json")

	s.mu.Lock()
	model := s.activeModel
	s.mu.Unlock()
	if model == "" {
		model = s.Config.Model
	}

	order := zcodeOrder{
		ID:              orderID,
		ConversationID:  convID,
		Resume:          convID != "",
		Model:           model,
		ReasoningEffort: s.Config.Effort,
		Prompt:          prompt,
		Messages:        []zcodeMessage{},
		CreatedAt:       float64(time.Now().UnixNano()) / 1e9,
	}
	if err := writeZcodeOrder(orderPath, &order); err != nil {
		return turn, fmt.Errorf("zcode order: %w", err)
	}
	ch := &zcodeChannel{orderPath: orderPath}
	s.mu.Lock()
	s.zcode = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.zcode = nil
		s.mu.Unlock()
		// zsm's cleanup(): the vanishing order is the agent's stop signal.
		_ = os.Remove(orderPath)
	}()

	if prompt != "" {
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "user", Source: "zcode", Message: prompt,
		}})
	}
	s.setActivity(Activity{Kind: ActivityThinking})
	logger.L.Info("session.zcode.order", "id", s.ID, "order", orderPath, "resume", order.Resume, "runtime", s.runtime.Name())

	// emit feeds every translated event through the shared handleEvent,
	// capturing the conversation id and a failed result on the way — the
	// same bookkeeping kimi ACP's RunTurn does with its onEvent.
	emit := func(evs ...ParsedEvent) {
		for _, ev := range evs {
			if ev.EventType == EventInit && ev.Init != nil && ev.Init.SessionID != "" {
				turn.sessionID = ev.Init.SessionID
			}
			if ev.EventType == EventResult && ev.Result != nil && ev.Result.StopReason == "error" {
				turn.failed = ev.Result.ResultText
			}
			if s.handleEvent(ev, autonomous) {
				turn.finished = true
			}
		}
	}

	var (
		lastState     string
		lastPermID    string
		lastQuestion  string
		initSeen      bool
		warnedPickup  bool
		orderedAt     = time.Now()
		clearQuestion = func(id string) {
			if id == "" {
				return
			}
			s.mu.Lock()
			pq := s.pendingQuestion
			mine := pq != nil && pq.ID == id
			if mine {
				if s.questionTimer != nil {
					s.questionTimer.Stop()
					s.questionTimer = nil
				}
				s.pendingQuestion = nil
			}
			wasWaiting := s.status == config.StatusWaitingForUser
			s.mu.Unlock()
			if mine && wasWaiting {
				s.setStatus(config.StatusWorking)
			}
		}
	)

	ticker := time.NewTicker(zcodePoll)
	defer ticker.Stop()
	for {
		if data := readZcodeStatus(outboxPath); data != nil {
			if !initSeen {
				initSeen = true
				sid := data.ConversationID
				if sid == "" {
					sid = convID
				}
				if sid == "" {
					sid = orderID
				}
				emit(ParsedEvent{EventType: EventInit, Init: &InitInfo{SessionID: sid}})
			}
			if data.ConversationID != "" {
				turn.sessionID = data.ConversationID
			}
			if data.State != lastState {
				lastState = data.State
				// Like zsm's _emit_state, the per-state payload (tool
				// calls, usage, limit) is translated only on a state
				// change, not on every poll.
				emit(s.zcodeStateEvents(data)...)
				s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
					Time: time.Now(), Level: "system", Source: "zcode",
					Message: "ZCode: " + zcodeStateText(data.State),
				}})
			}
			if p := data.Permission; p != nil && p.ID != lastPermID {
				lastPermID = p.ID
				id := p.ID
				if id == "" {
					id = "perm"
				}
				emit(ParsedEvent{EventType: EventPermission, Permission: &PermissionRequest{
					ID: id, Tool: p.Tool, Description: p.Summary,
				}})
			}
			if q := data.Question; q != nil && q.ID != lastQuestion {
				lastQuestion = q.ID
				s.askZcodeQuestion(q.ID, q.Text)
			} else if q == nil {
				// The agent moved past its question (answered or dropped
				// it): let a still-pending one go so the run continues.
				clearQuestion(lastQuestion)
			}
			switch data.State {
			case "done", "failed":
				emit(s.zcodeResultEvent(data))
				logger.L.Info("session.zcode.done", "id", s.ID, "state", data.State, "order", orderID)
				return turn, nil
			}
		} else if !warnedPickup && time.Since(orderedAt) > zcodePickupWarnAfter {
			warnedPickup = true
			s.emitManagerLog("No status from .zsm/outbox — make sure a ZCode session is running in this workdir and watching .zsm/inbox")
		}

		select {
		case <-ctx.Done():
			// zsm's stairway in miniature: ask the agent to wrap up (the
			// soft_stop message plus its companion user message, exactly
			// what ZCodeFileEngine.send does for soft_stop), give it a
			// short grace window, then remove the order file — on this
			// transport that removal is the force stop.
			_ = s.zcodeSend(zcodeMessage{Type: "soft_stop"})
			_ = s.zcodeSend(zcodeMessage{Type: "user_message", Text: "Команда стоп: заверши текущую задачу и выйди."})
			deadline := time.Now().Add(zcodeStopGrace)
			for time.Now().Before(deadline) {
				time.Sleep(zcodePoll)
				if d := readZcodeStatus(outboxPath); d != nil && (d.State == "done" || d.State == "failed") {
					break
				}
			}
			logger.L.Info("session.zcode.stopped", "id", s.ID, "order", orderID)
			return turn, nil
		case <-ticker.C:
		}
	}
}

// zcodeStateText humanizes an outbox state for the log feed.
func zcodeStateText(state string) string {
	switch state {
	case "working":
		return "работает"
	case "waiting_permission":
		return "ждёт разрешения"
	case "done":
		return "закончил"
	case "failed":
		return "ошибка"
	case "":
		return "неизвестное состояние"
	default:
		return state
	}
}

// zcodeStateEvents translates one outbox state change into shared events:
// the last five tool calls (zsm re-sends them per state change; the agent
// keeps the full list in the file), the usage snapshot and a limit report.
func (s *Session) zcodeStateEvents(data *zcodeStatus) []ParsedEvent {
	var evs []ParsedEvent
	now := time.Now()
	start := len(data.ToolCalls) - 5
	if start < 0 {
		start = 0
	}
	for _, tc := range data.ToolCalls[start:] {
		id := tc.Tool + strconv.Itoa(tc.DurationMS)
		if id == "" {
			id = "?"
		}
		tool := tc.Tool
		if tool == "" {
			tool = "?"
		}
		evs = append(evs,
			ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
				Time: now, Level: "tool", Source: "zcode",
				ToolName: tool, ToolInput: tc.Summary, ToolUseID: id,
			}}, Activity: &Activity{Kind: ActivityTool, Tool: tool}},
			ParsedEvent{EventType: EventLog, Entries: []config.LogEntry{{
				Time: now, Level: "tool_result", Source: "zcode",
				ToolName: tool, Message: tc.Summary, ToolUseID: id,
			}}},
		)
	}
	if u := data.Usage.tokenUsage(); u != nil {
		evs = append(evs, ParsedEvent{EventType: EventLog, Usage: u})
	}
	if l := data.Limit; l != nil {
		evs = append(evs, ParsedEvent{EventType: EventRateLimit, RateLimit: zcodeRateLimit(l)})
	}
	return evs
}

// zcodeResultEvent translates the terminal done/failed status into the
// shared result event. A ```ask-user``` marker in the final text is honoured
// like on every other runtime, and a continuation flag is surfaced as a log
// note for the manager's next-task decision.
func (s *Session) zcodeResultEvent(data *zcodeStatus) ParsedEvent {
	stop := "success"
	if data.State == "failed" {
		stop = "error"
	}
	entries := []config.LogEntry{{
		Time: time.Now(), Level: "result", Source: "zcode", Message: data.Text,
	}}
	if data.Continuation {
		entries = append(entries, config.LogEntry{
			Time: time.Now(), Level: "system", Source: "zcode",
			Message: "continuation: задача не завершена — агент просит следующий виток",
		})
	}
	ev := ParsedEvent{
		EventType: EventResult,
		Entries:   entries,
		Result:    &SessionResult{StopReason: stop, ResultText: data.Text},
		Activity:  &Activity{Kind: ActivityIdle},
	}
	if q := questionFromMarker(data.Text); q != nil {
		ev.Questions = []Question{*q}
	}
	return ev
}

// zcodeRateLimit maps the outbox's limit object onto RateLimitInfo: the
// status is always a rejecting one (the agent only reports limits that hit;
// when reset_at does not parse, onRateLimit's fallback pause applies).
func zcodeRateLimit(l *zcodeLimit) *RateLimitInfo {
	info := &RateLimitInfo{Status: "limited", RateLimitType: l.Kind}
	switch {
	case strings.HasPrefix(l.ResetAt, "+"):
		if d, err := time.ParseDuration(strings.ToLower(l.ResetAt)); err == nil {
			info.ResetsAt = time.Now().Add(d).Unix()
		}
	case strings.Contains(l.ResetAt, "T"):
		if t, err := time.Parse(time.RFC3339, l.ResetAt); err == nil {
			info.ResetsAt = t.Unix()
		}
	case strings.Contains(l.ResetAt, ":"):
		// "HH:MM" — the next occurrence of that wall-clock time.
		var h, m int
		if _, err := fmt.Sscanf(l.ResetAt, "%d:%d", &h, &m); err == nil {
			now := time.Now()
			t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
			if !t.After(now) {
				t = t.Add(24 * time.Hour)
			}
			info.ResetsAt = t.Unix()
		}
	}
	return info
}

// askZcodeQuestion surfaces an outbox question as a pending question whose
// answer is appended to the order file as question_response — the same
// turn keeps running, exactly like zsm's question flow.
func (s *Session) askZcodeQuestion(id, text string) {
	if id == "" {
		id = "q"
	}
	pq := &PendingQuestion{ID: id, Question: text, AskedAt: time.Now()}
	pq.onAnswer = func(answer string) error {
		return s.zcodeSend(zcodeMessage{Type: "question_response", QuestionID: id, Text: answer})
	}
	s.mu.Lock()
	s.pendingQuestion = pq
	s.mu.Unlock()
	s.setStatus(config.StatusWaitingForUser)
	logger.L.Info("session.question", "id", s.ID, "question", text, "source", "zcode")
	cp := *pq
	s.emit(SessionEvent{Type: EvtQuestion, Question: &cp})
}
