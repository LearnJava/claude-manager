package session

import (
	"strings"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/logger"
)

// hermesRuntime is the Runtime implementation for the Hermes CLI backend
// (RUNTIME-TASKS.md RT-05). Like claudeRuntime, it wraps the existing
// buildHermesArgs/hermesStream rather than relocating them — those stay
// exactly where hermes_runtime.go's/hermes_parser.go's own tests already
// exercise them.
type hermesRuntime struct{}

func (hermesRuntime) Name() string { return "hermes" }

// PerTurnProcess is true: hermesRunTurn spawns a fresh `hermes chat` process
// for every single turn and --resume's the conversation by id on the next
// one (docs/runtimes.md §4) — there is no long-lived process to keep a
// conversation "open" the way Claude's is.
func (hermesRuntime) PerTurnProcess() bool { return true }

// Args delegates to Session.buildHermesArgs, unchanged. autonomous/prompt
// are ignored here — Hermes has no --append-system-prompt equivalent passed
// as an argv flag; the autonomous protocol text instead rides in front of
// the first turn's prompt (hermesPreamble), and the prompt itself goes over
// stdin (--query-file -), not as an argument.
func (hermesRuntime) Args(s *Session, autonomous bool, convID, imagePath, prompt string) []string {
	return s.buildHermesArgs(convID, imagePath)
}

// NewParser returns a fresh hermesStream: it must be new per process (one
// per turn) since it buffers streamed text and tracks synthetic tool-call
// ids scoped to a single `hermes chat` invocation (hermes_parser.go).
func (hermesRuntime) NewParser() Parser { return &hermesParserAdapter{stream: newHermesStream()} }

func (hermesRuntime) Binary(s *Session) string { return s.hermesPath }

// Env delegates to hermesEnv: the parent's HERMES_* session state is dropped
// and TERMINAL_CWD pinned to the project folder.
func (hermesRuntime) Env(s *Session, parent []string) []string {
	return hermesEnv(parent, s.ProjectPath)
}

func (hermesRuntime) ClassifyStderr(line string) *TurnFailure {
	return ClassifyHermesStderrLine(line)
}

// Preamble delegates to Session.hermesPreamble: the session's append text and
// the autonomous protocol ride in front of the first turn of a fresh
// conversation (a resumed one already has them).
func (hermesRuntime) Preamble(s *Session, autonomous bool) string {
	return s.hermesPreamble(autonomous)
}

// WarnOptions delegates to Session.warnUnsupportedHermesOptions.
func (hermesRuntime) WarnOptions(s *Session) { s.warnUnsupportedHermesOptions() }

// ExpandPrompt turns a leading "/skill" pick into Hermes' `-s` flags; the log
// keeps showing what the user typed (the caller keeps the original text).
func (hermesRuntime) ExpandPrompt(s *Session, prompt string) (string, []string) {
	if !strings.HasPrefix(prompt, "/") {
		return prompt, nil
	}
	prompt, skills := expandHermesSkills(prompt, hermesSkillCommands(hermesHome(s.Config.HermesProfile)))
	return prompt, skillsToArgs(skills)
}

// skillsToArgs flattens skill names into repeated `-s` flags.
func skillsToArgs(skills []string) []string {
	var args []string
	for _, sk := range skills {
		args = append(args, "-s", sk)
	}
	return args
}

// AfterTurn applies Hermes' post-turn failure checks: the state.db step-limit
// scan and the 429-behind-401 log scrape.
func (hermesRuntime) AfterTurn(s *Session, turn hermesTurn, convID string, turnStart time.Time) {
	if turn.sessionID != "" && hermesTurnHitStepLimit(s.hermesStateDB(), turn.sessionID) {
		s.applyFailure(&TurnFailure{Kind: KindStepLimit})
		logger.L.Warn("session.hermes.step_limit", "id", s.ID, "conversation", turn.sessionID)
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "system", Source: "manager",
			Message: "Turn stopped by Hermes' step limit (--max-turns) — the agent was cut off mid-task",
		}})
	}
	if turn.failed != "" && !s.rateLimited.Load() {
		conv := turn.sessionID
		if conv == "" {
			conv = convID
		}
		if hermesTurnHitRateLimit(s.hermesAgentLog(), conv, turnStart) {
			logger.L.Warn("session.hermes.rate_limit_in_log", "id", s.ID, "conversation", conv, "reported", turn.failed)
			s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
				Time: time.Now(), Level: "system", Source: "manager",
				Message: "Hermes hit the API usage limit (429) — waiting for it to reset, then continuing this conversation",
			}})
			s.onRateLimit(&RateLimitInfo{Status: "exceeded", RateLimitType: "hermes_429", Utilization: 1.0})
		}
	}
}

// Capabilities: resume and effort exist; the permission protocol and USD
// cost do not (its own approval policy, no cost field on the wire). Token
// usage arrives once per result line.
func (hermesRuntime) Capabilities() Capabilities {
	return Capabilities{Resume: true, Effort: true, PermissionProtocol: false, Cost: false, TokenUsage: true, Images: true}
}

// hermesParserAdapter adapts *hermesStream to the shared Parser interface.
type hermesParserAdapter struct{ stream *hermesStream }

func (a *hermesParserAdapter) Parse(line string) []ParsedEvent { return a.stream.Parse(line) }
func (a *hermesParserAdapter) Flush() []ParsedEvent            { return a.stream.Flush() }
func (a *hermesParserAdapter) LastError() string               { return a.stream.lastError }
