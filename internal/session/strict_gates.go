package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/worker"
)

// Strict gates for Claude sessions (WASTE-TASKS.md WS-05). Mixed programming
// already runs the project's gates as a blocking check and sends a red result
// back to the worker; a Claude session used to get only the informational
// post_task_hook. With gate_rounds > 0 a task that ended on the done marker is
// checked the same way, with the same GateResult.Feedback() text.

// runGatesFn is worker.RunGates, replaceable in tests.
var runGatesFn = worker.RunGates

// gateVerdict is what the run loop does after checking the gates of a closed task.
type gateVerdict int

const (
	// gatesGreen: nothing to do, the task counts as done.
	gatesGreen gateVerdict = iota
	// gatesRework: the red output was queued as the next prompt; run again.
	gatesRework
	// gatesExhausted: still red after gate_rounds rework rounds; stop.
	gatesExhausted
)

// strictGatesOn reports whether this session's finished tasks are gated:
// gate_rounds set, gates configured, a runtime that can resume its
// conversation as a fresh process (Claude — Hermes keeps its own turn loop).
func (s *Session) strictGatesOn() bool {
	return s.Config.GateRounds > 0 && len(s.gates) > 0 && !s.Config.IsHermes()
}

// gateRoot is the folder the gates run in: the session's work folder (pool
// slot or project root), where the agent's changes are.
func (s *Session) gateRoot() string {
	s.mu.Lock()
	dir := s.workDir
	s.mu.Unlock()
	if dir == "" {
		dir = s.ProjectPath
	}
	return dir
}

// checkStrictGates runs the gates after a task ended. *round counts the rework
// rounds already sent for this task. On red with rounds left it queues the
// gates' output as the next resumed run's prompt and increments *round.
func (s *Session) checkStrictGates(ctx context.Context, round *int) gateVerdict {
	root := s.gateRoot()
	s.emitManagerLog(fmt.Sprintf("Ворота после задачи: %s", strings.Join(s.gates, " && ")))
	res := runGatesFn(ctx, root, s.gates)
	if res.Passed {
		s.emitManagerLog("Ворота зелёные — задача принята")
		return gatesGreen
	}
	if *round >= s.Config.GateRounds {
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "error", Source: "manager",
			Message: fmt.Sprintf("Ворота красные после %d раундов доработки — остановка:\n%s",
				*round, res.Feedback()),
		}})
		return gatesExhausted
	}
	*round++
	s.emitManagerLog(fmt.Sprintf("Ворота красные — возвращаю на доработку (раунд %d из %d)", *round, s.Config.GateRounds))
	s.mu.Lock()
	s.pendingGateFeedback = gateReworkPrompt(res, *round, s.Config.GateRounds)
	s.resumeSessionID = s.CLISessionID
	s.mu.Unlock()
	return gatesRework
}

// gateReworkPrompt is the request that sends a red result back: a short frame
// around the gates' own output, unparaphrased.
func gateReworkPrompt(res worker.GateResult, round, max int) string {
	return fmt.Sprintf("You declared the task done, but the project's gates failed (rework round %d of %d). "+
		"Fix the cause in the code and run the failing command yourself, then finish again with the same end-of-turn block. "+
		"The gates' output:\n\n%s", round, max, res.Feedback())
}
