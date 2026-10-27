package session

import (
	"encoding/json"
	"fmt"

	"claude-manager/internal/logger"
	"claude-manager/internal/optimization"
)

// Loop detection ([optimization] loop_detection, PLAN.md 20.3.8). Every tool
// call of a run goes through optimization.LoopDetector; when one tool+input
// repeats loop_threshold times within the last loop_window calls the
// configured loop_action runs:
//
//   - warn: a manager line in the log and an alert:loop toast.
//   - send_hint: the same, plus loop_hint sent into the live stdin as a user
//     message. A runtime with a process per turn (Hermes) has no live stdin,
//     so there it stays a warning.
//   - restart: the same warning, then the process is stopped and a fresh one
//     starts with a note about the loop in front of the session's prompt.
//     The run that restart starts only warns, so a task that loops again is
//     not restarted forever.
//
// After a detection the detector's window is cleared, so the same history
// does not fire again on the very next call.

// EventNameLoopAlert is emitted to the frontend for every detected loop; it
// shows a toast.
const EventNameLoopAlert = "alert:loop"

// LoopAlertEvent is the alert:loop payload.
type LoopAlertEvent struct {
	ID string `json:"id"`
	LoopAlert
}

// LoopAlert is the EvtLoop payload (and, with the session id, alert:loop).
type LoopAlert struct {
	Tool   string `json:"tool"`
	Input  string `json:"input"`
	Count  int    `json:"count"`
	Window int    `json:"window"`
	Action string `json:"action"`
}

// observeLoops feeds the tool calls of one parsed event to the detector.
func (s *Session) observeLoops(ev *ParsedEvent) {
	if s.loopDetector == nil || !s.loopDetector.Enabled() {
		return
	}
	for _, e := range ev.Entries {
		if e.Level != "tool" || e.ToolName == "" {
			continue
		}
		hit := s.loopDetector.Observe(e.ToolName, optimization.NormalizeInput(e.ToolName, e.ToolArgs))
		if hit == nil {
			continue
		}
		s.loopDetector.Reset()
		s.onLoop(hit)
		return
	}
}

func (s *Session) onLoop(hit *optimization.LoopDetected) {
	action := hit.Action
	if action == optimization.LoopActionSendHint && s.runtime.PerTurnProcess() {
		action = optimization.LoopActionWarn
	}
	if action == optimization.LoopActionRestart && s.afterLoopRestart.Load() {
		action = optimization.LoopActionWarn
	}
	what := hit.Tool
	if hit.Input != "" {
		what += " " + hit.Input
	}
	logger.L.Warn("session.loop_detected", "id", s.ID, "tool", hit.Tool, "input", hit.Input,
		"count", hit.Count, "action", action)

	msg := fmt.Sprintf("Possible loop: `%s` repeated %d times in the last %d tool calls.", what, hit.Count, hit.WindowSize)
	if hit.Far {
		msg = fmt.Sprintf("Possible loop: `%s` repeated %d times in this run.", what, hit.Count)
	}
	// What broke this loop the last time, if it was seen before (loop_memory).
	// Asked before the new event is stored, so a loop never answers itself.
	var past string
	if s.loopMemory != nil {
		past = s.loopMemory.Hint(hit.Tool, hit.Input)
		s.loopMemory.Record(hit)
	}
	if past != "" {
		msg += " " + past
	}
	switch action {
	case optimization.LoopActionSendHint:
		msg += " Sending the agent a hint."
	case optimization.LoopActionRestart:
		msg += " Restarting the session with a note about the loop."
	}
	s.emitManagerLog(msg)
	s.emit(SessionEvent{Type: EvtLoop, Loop: &LoopAlert{
		Tool: hit.Tool, Input: hit.Input, Count: hit.Count, Window: hit.WindowSize, Action: action,
	}})

	switch action {
	case optimization.LoopActionSendHint:
		hint := fmt.Sprintf("%s (`%s` was repeated %d times in your last %d tool calls.)",
			hit.Hint, what, hit.Count, hit.WindowSize)
		if hit.Far {
			hint = fmt.Sprintf("%s (`%s` was repeated %d times in this run.)", hit.Hint, what, hit.Count)
		}
		if past != "" {
			hint += " " + past
		}
		data, err := json.Marshal(userMessage(hint))
		if err == nil {
			err = s.queueInput(append(data, '\n'))
		}
		if err != nil {
			logger.L.Warn("session.loop_hint_failed", "id", s.ID, "error", err)
		}
	case optimization.LoopActionRestart:
		s.mu.Lock()
		s.pendingLoopNote = fmt.Sprintf(
			"The session manager stopped the previous run of this session: it repeated `%s` %d times within %d tool calls "+
				"without getting further. Do not repeat that. Check git status and where the task stands, then continue "+
				"it with a different approach.", what, hit.Count, hit.WindowSize)
		if past != "" {
			s.pendingLoopNote += " " + past
		}
		cancel := s.cancelRun
		s.mu.Unlock()
		s.loopRestartHit.Store(true)
		if cancel != nil {
			cancel()
		}
	}
}
