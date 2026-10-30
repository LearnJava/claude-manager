package session

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/logger"
	"claude-manager/internal/proc"
)

type acpRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (e *acpRPCError) Error() string { return e.Message }

type acpRPCMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
	Result  json.RawMessage `json:"result"`
	Error   *acpRPCError    `json:"error"`
}

type acpClient struct {
	enc         *json.Encoder
	dec         *json.Decoder
	nextID      int
	thoughtText string
	pendingText string
	resultText  string
	onEvent     func([]ParsedEvent)
}

func (c *acpClient) call(method string, params, result any) error {
	c.nextID++
	id := c.nextID
	if err := c.enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": id, "method": method, "params": params,
	}); err != nil {
		return err
	}
	for {
		var msg acpRPCMessage
		if err := c.dec.Decode(&msg); err != nil {
			return err
		}
		if msg.Method != "" {
			if msg.Method == "session/update" {
				c.onUpdate(msg.Params)
			} else {
				c.onRequest(msg)
			}
			continue
		}
		if string(msg.ID) != strconv.Itoa(id) {
			continue
		}
		if msg.Error != nil {
			return msg.Error
		}
		if result != nil && len(msg.Result) > 0 {
			return json.Unmarshal(msg.Result, result)
		}
		return nil
	}
}

func (c *acpClient) onRequest(msg acpRPCMessage) {
	if msg.Method == "session/request_permission" {
		var p struct {
			Options []struct {
				OptionID string `json:"optionId"`
				Kind     string `json:"kind"`
			} `json:"options"`
		}
		_ = json.Unmarshal(msg.Params, &p)
		optionID := ""
		for _, opt := range p.Options {
			if opt.Kind == "allow_once" || opt.Kind == "allow_always" {
				optionID = opt.OptionID
				break
			}
		}
		if optionID == "" && len(p.Options) > 0 {
			optionID = p.Options[0].OptionID
		}
		_ = c.enc.Encode(map[string]any{
			"jsonrpc": "2.0", "id": msg.ID,
			"result": map[string]any{"outcome": map[string]any{
				"outcome": "selected", "optionId": optionID,
			}},
		})
		return
	}
	_ = c.enc.Encode(map[string]any{
		"jsonrpc": "2.0", "id": msg.ID,
		"error": map[string]any{"code": -32601, "message": "method not found: " + msg.Method},
	})
}

func (c *acpClient) flushThought() {
	if c.thoughtText == "" {
		return
	}
	c.onEvent([]ParsedEvent{{EventType: EventLog, Entries: []config.LogEntry{{
		Time: time.Now(), Level: "thinking", Source: "kimi", Message: c.thoughtText,
	}}, Activity: &Activity{Kind: ActivityThinking}}})
	c.thoughtText = ""
}

func (c *acpClient) flushText() {
	if c.pendingText == "" {
		return
	}
	c.onEvent([]ParsedEvent{{EventType: EventLog, Entries: []config.LogEntry{{
		Time: time.Now(), Level: "text", Source: "kimi", Message: c.pendingText,
	}}, Activity: &Activity{Kind: ActivityWriting}}})
	c.pendingText = ""
}

func (c *acpClient) flush() {
	c.flushThought()
	c.flushText()
}

func (c *acpClient) onUpdate(raw json.RawMessage) {
	var p struct {
		SessionID string `json:"sessionId"`
		Update    struct {
			SessionUpdate string          `json:"sessionUpdate"`
			Content       acpContent      `json:"content"`
			ToolCallID    string          `json:"toolCallId"`
			Title         string          `json:"title"`
			Kind          string          `json:"kind"`
			Status        string          `json:"status"`
			Name          string          `json:"name"`
			RawInput      json.RawMessage `json:"rawInput"`
			RawOutput     json.RawMessage `json:"rawOutput"`
			Usage         *TokenUsage     `json:"usage"`
		} `json:"update"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return
	}
	u := &p.Update
	now := time.Now()
	switch u.SessionUpdate {
	case "agent_thought_chunk":
		c.flushText()
		c.thoughtText += u.Content.text()
	case "agent_message_chunk":
		text := u.Content.text()
		c.pendingText += text
		c.resultText += text
	case "tool_call":
		c.flush()
		name := u.Name
		if name == "" {
			name = u.Kind
		}
		input := strings.TrimSpace(string(u.RawInput))
		if input == "" || input == "null" {
			input = u.Title
		}
		c.onEvent([]ParsedEvent{{EventType: EventLog, Entries: []config.LogEntry{{
			Time: now, Level: "tool", Source: "kimi", ToolName: name, ToolInput: input, ToolUseID: u.ToolCallID,
		}}, Activity: &Activity{Kind: ActivityTool, Tool: name}}})
	case "tool_call_update":
		level := "tool_result"
		if u.Status == "failed" || u.Status == "error" {
			level = "error"
		}
		text := strings.TrimSpace(string(u.RawOutput))
		if text == "" || text == "null" {
			text = u.Title
		}
		c.onEvent([]ParsedEvent{{EventType: EventLog, Entries: []config.LogEntry{{
			Time: now, Level: level, Source: "kimi", ToolName: u.Name, Message: text, ToolUseID: u.ToolCallID,
		}}, Activity: &Activity{Kind: ActivityThinking}}})
	case "usage_update":
		if u.Usage != nil {
			c.onEvent([]ParsedEvent{{EventType: EventLog, Usage: u.Usage}})
		}
	}
}

type acpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (c acpContent) text() string { return c.Text }

func (kimiRuntime) RunTurn(s *Session, ctx context.Context, prompt string, images []ImageAttachment, convID string, autonomous bool) (hermesTurn, error) {
	return s.runKimiACPTurn(ctx, prompt, images, convID, autonomous)
}

func (s *Session) runKimiACPTurn(ctx context.Context, prompt string, images []ImageAttachment, convID string, autonomous bool) (hermesTurn, error) {
	var turn hermesTurn
	if len(images) > 0 {
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "system", Source: "manager",
			Message: "kimi runtime does not accept image attachments in this build — sending text only",
		}})
	}

	workDir := s.runDir()
	turnCtx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	cmd := exec.CommandContext(turnCtx, s.runtime.Binary(s), "acp")
	proc.HideConsole(cmd)
	if workDir != "" {
		cmd.Dir = workDir
	}
	cmd.Env = s.runtime.Env(s, os.Environ())
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return turn, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return turn, fmt.Errorf("stderr pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return turn, fmt.Errorf("stdin pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return turn, fmt.Errorf("start kimi acp: %w", err)
	}
	logger.L.Info("session.spawned", "id", s.ID, "pid", cmd.Process.Pid, "runtime", s.runtime.Name(), "protocol", "acp")
	s.beginStartWatch(cmd.Process.Pid)
	defer s.endStartWatch()

	job, jobErr := proc.NewJob()
	if jobErr != nil {
		logger.L.Warn("session.job_create_failed", "id", s.ID, "error", jobErr)
	}
	defer job.Close()
	if err := job.Assign(cmd.Process.Pid); err != nil {
		logger.L.Warn("session.job_assign_failed", "id", s.ID, "pid", cmd.Process.Pid, "error", err)
	}
	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cmd = nil
		s.mu.Unlock()
	}()

	if prompt != "" {
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "user", Source: "kimi", Message: prompt,
		}})
	}
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		defer logger.Recover("session.drain_stderr", "id", s.ID)
		s.drainHermesStderr(stderr)
	}()

	client := &acpClient{enc: json.NewEncoder(stdin), dec: json.NewDecoder(stdout)}
	client.onEvent = func(evs []ParsedEvent) {
		for _, ev := range evs {
			if ev.EventType == EventInit && ev.Init != nil {
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
	s.setActivity(Activity{Kind: ActivityThinking})

	fail := func(err error) (hermesTurn, error) {
		client.flush()
		cancelTurn()
		_ = stdin.Close()
		<-stderrDone
		waitErr := cmd.Wait()
		if ctx.Err() != nil {
			return turn, nil
		}
		if err == nil {
			err = waitErr
		}
		if err == nil {
			return turn, nil
		}
		text := err.Error()
		if info, ok := detectRateLimitText(text); ok {
			s.onRateLimit(info)
		}
		switch {
		case strings.Contains(text, "Session not found:"), strings.Contains(text, "session.not_found"),
			strings.Contains(text, "in use by another Kimi Code Desktop instance"):
			s.applyFailure(&TurnFailure{Kind: KindSessionNotFound, Message: text})
		case isAuthError(text) || strings.Contains(text, "auth_required"):
			s.applyFailure(&TurnFailure{Kind: KindAuth, Message: text})
		case hasTransientAPIMarker(text):
			s.applyFailure(&TurnFailure{Kind: KindTransientAPI, Message: text})
		}
		return turn, err
	}

	if err := client.call("initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
	}, nil); err != nil {
		return fail(err)
	}

	sessionParams := map[string]any{"cwd": workDir, "mcpServers": []any{}}
	if convID == "" {
		var created struct {
			SessionID string `json:"sessionId"`
		}
		if err := client.call("session/new", sessionParams, &created); err != nil {
			return fail(err)
		}
		convID = created.SessionID
	} else {
		sessionParams["sessionId"] = convID
		if err := client.call("session/resume", sessionParams, nil); err != nil {
			return fail(err)
		}
	}
	turn.sessionID = convID
	client.onEvent([]ParsedEvent{{EventType: EventInit, Init: &InitInfo{SessionID: convID}}})

	if err := client.call("session/set_mode", map[string]any{
		"sessionId": convID, "modeId": "auto",
	}, nil); err != nil {
		return fail(err)
	}
	s.mu.Lock()
	model := s.activeModel
	s.mu.Unlock()
	if model == "" {
		model = s.Config.Model
	}
	if model != "" {
		if err := client.call("session/set_model", map[string]any{
			"sessionId": convID, "modelId": model,
		}, nil); err != nil {
			return fail(err)
		}
	}

	var completed struct {
		StopReason string `json:"stopReason"`
	}
	if err := client.call("session/prompt", map[string]any{
		"sessionId": convID,
		"prompt":    []acpContent{{Type: "text", Text: prompt}},
	}, &completed); err != nil {
		return fail(err)
	}
	stop := completed.StopReason
	if stop == "" || stop == "end_turn" {
		stop = "success"
	}
	client.flush()
	resultEvent := ParsedEvent{
		EventType: EventResult,
		Result:    &SessionResult{StopReason: stop, ResultText: client.resultText},
		Activity:  &Activity{Kind: ActivityIdle},
	}
	if q := questionFromMarker(client.resultText); q != nil {
		resultEvent.Questions = []Question{*q}
	}
	client.onEvent([]ParsedEvent{resultEvent})
	s.mu.Lock()
	waiting := s.pendingQuestion != nil
	s.mu.Unlock()
	if waiting {
		_ = stdin.Close()
		<-stderrDone
		_ = cmd.Wait()
		return turn, nil
	}
	_ = client.call("session/close", map[string]any{"sessionId": convID}, nil)
	_ = stdin.Close()
	<-stderrDone
	waitErr := cmd.Wait()
	if turn.sessionID == "" {
		s.mu.Lock()
		turn.sessionID = s.wireInitID
		s.wireInitID = ""
		s.mu.Unlock()
	}
	if waitErr != nil && turn.failed == "" && ctx.Err() == nil {
		return turn, fmt.Errorf("kimi acp exited: %w", waitErr)
	}
	logger.L.Info("session.process_exit", "id", s.ID, "ok", turn.failed == "", "runtime", s.runtime.Name(), "protocol", "acp")
	return turn, nil
}
