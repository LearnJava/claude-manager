package session

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"claude-manager/internal/config"
	"claude-manager/internal/logger"
)

// ControlRequestMessage is a stream-json control request written to stdin.
// The Claude CLI (--input-format stream-json) answers it with a
// control_response line; for subtype "interrupt" it also aborts the turn in
// flight — the running tool call or generation — and ends that turn with a
// result event, keeping the process and its conversation alive for the next
// user message.
type ControlRequestMessage struct {
	Type      string                `json:"type"`
	RequestID string                `json:"request_id"`
	Request   ControlRequestPayload `json:"request"`
}

// ControlRequestPayload is the body of a ControlRequestMessage.
type ControlRequestPayload struct {
	Subtype string `json:"subtype"`
}

// interruptMessage builds the control request that aborts the current turn.
func interruptMessage() ControlRequestMessage {
	return ControlRequestMessage{
		Type:      "control_request",
		RequestID: "interrupt-" + uuid.NewString(),
		Request:   ControlRequestPayload{Subtype: "interrupt"},
	}
}

// Interrupt aborts the action in flight — the current turn — without
// stopping the session: the CLI process and its conversation stay alive and
// the session waits for the user's next message (StatusWaitingForUser). Even
// an autonomous session keeps its process open after an interrupted turn
// (see onInterrupted); the turn the user's next message starts ends the
// usual way.
//
// Returns an error when there is nothing to interrupt (the turn already
// ended), no live stdin, or the runtime starts a process per turn (Hermes),
// where there is no control channel to send the request through.
func (s *Session) Interrupt() error {
	if s.runtime != nil && s.runtime.PerTurnProcess() {
		return fmt.Errorf("session %s: the %s runtime cannot interrupt a turn", s.ID, s.runtime.Name())
	}
	s.mu.Lock()
	st := s.status
	idle := s.activity.Kind == ActivityIdle
	s.mu.Unlock()
	if st != config.StatusWorking && st != config.StatusWaitingPermission {
		return fmt.Errorf("session %s is not running a turn (status=%s)", s.ID, st)
	}
	if idle && st != config.StatusWaitingPermission {
		return fmt.Errorf("session %s: no action in progress", s.ID)
	}
	data, err := json.Marshal(interruptMessage())
	if err != nil {
		return err
	}
	s.interruptPending.Store(true)
	if err := s.queueInput(append(data, '\n')); err != nil {
		s.interruptPending.Store(false)
		return err
	}
	// A permission prompt belongs to the aborted tool call.
	s.mu.Lock()
	s.pendingPerm = nil
	s.mu.Unlock()
	logger.L.Info("session.interrupt", "id", s.ID)
	s.emitManagerLog("Interrupt requested — aborting the current action; the session stays open")
	return nil
}

// markTurnStarted marks a session between turns as thinking once a user
// message is queued (the Hermes runtime does the same): the CLI's first
// stream event can take seconds, and until then the idle activity left by
// the last result would hide the turn — and its ⏹ button — from the UI and
// make Interrupt refuse. A turn already in flight keeps its activity.
func (s *Session) markTurnStarted() {
	s.mu.Lock()
	idle := s.activity.Kind == ActivityIdle
	s.mu.Unlock()
	if idle {
		s.setActivity(Activity{Kind: ActivityThinking})
	}
}

// onInterrupted handles the result that ends an interrupted turn: the turn
// is not a failure and not a finished task, so no failure classification
// (the CLI reports it as error_during_execution) and no stdin close — the
// session waits for the user's next message instead.
func (s *Session) onInterrupted(res *SessionResult) {
	s.mu.Lock()
	s.interrupted = true
	s.mu.Unlock()
	s.setStatus(config.StatusWaitingForUser)
	s.emitManagerLog("Action interrupted — waiting for your next message")
	s.emit(SessionEvent{Type: EvtResult, Result: res})
}
