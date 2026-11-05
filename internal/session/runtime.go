package session

import (
	"context"
	"time"

	"claude-manager/internal/config"
)

// Capabilities describes what a runtime actually supports, so SessionManager,
// the session loop and the UI can key behaviour on the backend's abilities
// instead of branching on its name (no scattered IsHermes/IsKimi checks).
type Capabilities struct {
	// Resume: a stopped conversation can be continued by id (claude --resume,
	// hermes --resume, kimi --session).
	Resume bool
	// Effort: the CLI accepts a reasoning-effort setting (claude --effort,
	// hermes --reasoning). kimi -p has no such flag.
	Effort bool
	// PermissionProtocol: the wire has mid-turn permission requests the
	// manager can answer (Claude only). Hermes applies its own policy, and
	// kimi -p runs under the auto policy with no approval round-trip.
	PermissionProtocol bool
	// Cost: the wire reports USD cost per turn. Hermes/Kimi are subscription
	// CLIs with no cost field — the UI must show "no data", never $0.
	Cost bool
	// TokenUsage: the wire reports token counts (claude per assistant
	// message, hermes once per result line).
	TokenUsage bool
	// Images: the CLI accepts pasted image attachments (claude content
	// blocks, hermes --image). kimi -p has no image flag.
	Images bool
	// PromptArg: the turn's prompt goes inline as an argv value (kimi -p)
	// rather than over stdin (hermes --query-file -, claude stream-json).
	PromptArg bool
}

// Runtime (RUNTIME-TASKS.md RT-05) is the seam between Session's
// runtime-agnostic machinery and what a specific CLI backend actually is.
// It does not yet unify runOnce (Claude) and runOncePerTurn (Hermes/Kimi)
// into one loop — but it pulls the pieces that differ per-backend (argv
// building, stdout parsing, process granularity, binary path, environment,
// stderr classification, capabilities) behind one interface instead of the
// loops hardcoding CLI details inline. Session picks one in New(), once,
// based on Config.Runtime — no other call site branches on which backend
// it's talking to (PerTurnProcess is the one structural property a caller is
// allowed to switch on, see its own doc comment).
type Runtime interface {
	// Name identifies the backend for logging only ("claude" | "hermes" | "kimi").
	Name() string

	// PerTurnProcess reports whether one CLI process answers exactly one turn
	// and exits (Hermes: hermesRunTurn spawns a fresh process per turn,
	// --resume-ing the conversation each time; Kimi: `kimi -p` per turn,
	// --session-ing it) as opposed to staying alive for the whole
	// conversation, taking further turns over stdin (Claude). This is the one
	// runtime difference that changes what "the turn ended" or "the
	// conversation continues" even means, so callers are expected to branch
	// on it directly (docs/runtimes.md §4, §6) rather than have it hidden.
	PerTurnProcess() bool

	// Args builds the CLI argv for one launch. autonomous mirrors
	// runOnce's/runOncePerTurn's own flag (task_source/auto_restart vs. an
	// interactive session). convID/imagePath only matter to a PerTurnProcess
	// runtime resuming a previous turn or attaching an image; claudeRuntime
	// ignores both — Claude keeps its own resume/session-id bookkeeping on
	// Session. prompt is the turn's prompt text: PerTurnProcess runtimes pipe
	// it via stdin (Hermes) or inline it as an argument (Kimi -p); Claude
	// ignores it (its prompt also goes over stdin).
	Args(s *Session, autonomous bool, convID, imagePath, prompt string) []string

	// NewParser returns a fresh stdout translator for one process: the whole
	// run for Claude (one process per task/session), one turn for Hermes/Kimi
	// (one process per turn). See Parser's own doc comment for why a fresh
	// instance per process is required rather than one shared on the Runtime
	// value itself.
	NewParser() Parser

	// Binary is the CLI executable for this session's runtime.
	Binary(s *Session) string

	// Env builds the child process's environment. The default implementation
	// returns parent unchanged; Hermes scrubs its own session variables and
	// pins TERMINAL_CWD (see hermesEnv).
	Env(s *Session, parent []string) []string

	// ClassifyStderr turns one stderr line into a TurnFailure (rate limit,
	// auth, session-not-found) or nil when the line says nothing actionable.
	// Wired into Session.applyFailure via the runtime's drain goroutine.
	ClassifyStderr(line string) *TurnFailure

	// Capabilities reports the backend's feature set for manager/UI decisions.
	Capabilities() Capabilities

	// Preamble is the text sent ahead of the FIRST turn of a fresh
	// conversation in place of Claude's --append-system-prompt (a PerTurnProcess
	// runtime cannot take that flag; a resumed conversation already has it).
	// claudeRuntime returns "" — Claude uses the argv flag instead.
	Preamble(s *Session, autonomous bool) string

	// WarnOptions logs, once per run, the Claude-only session options this
	// runtime silently cannot honour (effort, permission_mode, budget, …).
	WarnOptions(s *Session)
}

// TurnExpander is an optional Runtime hook: rewrites a turn's prompt before
// launch and may add CLI args (Hermes: a leading "/skill" becomes `-s`).
type TurnExpander interface {
	ExpandPrompt(s *Session, prompt string) (newPrompt string, extraArgs []string)
}

// TurnRunner is an optional Runtime hook for protocols that need bidirectional
// JSON-RPC rather than a one-shot CLI stdout stream (Kimi ACP).
type TurnRunner interface {
	RunTurn(s *Session, ctx context.Context, prompt string, images []ImageAttachment, convID string, autonomous bool) (hermesTurn, error)
}

// TurnFinisher is an optional Runtime hook running after one turn's process
// exited (Hermes: the state.db step-limit scan and the 429-behind-401 log
// scrape). Runtimes without such post-turn checks simply do not implement it.
type TurnFinisher interface {
	AfterTurn(s *Session, turn hermesTurn, convID string, turnStart time.Time)
}

// StderrIniter is an optional Runtime hook for backends whose conversation id
// can arrive on stderr instead of stdout (Kimi sometimes prints its
// session.resume_hint there). It returns the id and true when the line
// carries one, so the drain can emit the EvtInit the resume depends on.
type StderrIniter interface {
	SessionIDFromStderr(line string) (string, bool)
}

// PermissionResponder is an optional Runtime hook for backends whose
// permission answers do not travel over the CLI's stdin as a
// permission_response line (the ZCode file transport appends them to the
// turn's order file instead). RespondPermission hands the already-normalized
// decision ("allow" | "deny") to it and skips the stdin write.
type PermissionResponder interface {
	DeliverPermission(s *Session, requestID, decision string) error
}

// Parser turns one runtime's stdout lines into the shared ParsedEvent format
// (docs/runtimes.md §§2-3) — the format both Session.handleEvent and the
// manager consume identically regardless of which Runtime produced it. A
// fresh instance is required per process because the Hermes translator is
// stateful within one process (buffers streamed text deltas, tracks
// synthetic tool-call ids that only make sense within a single `hermes chat`
// invocation — see hermes_parser.go's hermesStream doc comment); the Claude
// translator has no such state but still implements this interface
// uniformly so a caller never special-cases which runtime it's driving.
type Parser interface {
	// Parse consumes one stdout line, returning zero, one, or more events in
	// order (Hermes can flush a buffered text entry alongside the new one;
	// Claude always returns exactly one).
	Parse(line string) []ParsedEvent
	// Flush returns any buffered-but-not-yet-emitted event at end of stream
	// (Hermes' trailing, not-yet-flushed text delta); always nil for a
	// stateless parser (Claude).
	Flush() []ParsedEvent
	// LastError returns the error text of the most recently parsed failed
	// result line, so a caller can classify a failed turn (rate limit, auth)
	// without needing a concrete reference to the underlying parser type.
	// Always "" for Claude, which instead carries a classified failure on
	// ParsedEvent.Failure itself (see parser.go's handleResult, RT-04).
	LastError() string
}

// newRuntime picks the Runtime implementation for a session's configured
// backend. The only call site of this selection (New()) — everywhere else
// goes through the Runtime interface (RUNTIME-TASKS.md RT-05).
func newRuntime(cfg config.SessionConfig) Runtime {
	switch {
	case cfg.IsHermes():
		return hermesRuntime{}
	case cfg.IsKimi():
		return kimiRuntime{}
	case cfg.IsZcode():
		return zcodeRuntime{}
	default:
		return claudeRuntime{}
	}
}
