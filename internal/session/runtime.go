package session

import "claude-manager/internal/config"

// Runtime (RUNTIME-TASKS.md RT-05) is the seam between Session's
// runtime-agnostic machinery and what a specific CLI backend actually is.
// It does not yet unify runOnce (Claude) and runOnceHermes (Hermes) into one
// loop — RT-06 does that — but it pulls the pieces that already differ
// per-backend (argv building, stdout parsing, process granularity) behind
// one interface instead of the two loops hardcoding CLI details inline.
// claudeRuntime and hermesRuntime are the two implementations; Session picks
// one in New(), once, based on Config.Runtime — no other call site branches
// on which backend it's talking to (RUNTIME-TASKS.md's "ядро не знает имён
// рантаймов" invariant; PerTurnProcess is the one structural property a
// caller is allowed to switch on, see its own doc comment).
type Runtime interface {
	// Name identifies the backend for logging only ("claude" | "hermes").
	Name() string

	// PerTurnProcess reports whether one CLI process answers exactly one turn
	// and exits (Hermes: hermesRunTurn spawns a fresh process per turn,
	// --resume-ing the conversation each time) as opposed to staying alive
	// for the whole conversation, taking further turns over stdin (Claude:
	// runOnce spawns one process for the whole task/session). This is the
	// one runtime difference that changes what "the turn ended" or "the
	// conversation continues" even means, so callers are expected to branch
	// on it directly (docs/runtimes.md §4, §6) rather than have it hidden.
	PerTurnProcess() bool

	// Args builds the CLI argv for one launch. autonomous mirrors
	// runOnce's/runOnceHermes's own flag (task_source/auto_restart vs. an
	// interactive session). convID/imagePath only matter to a
	// PerTurnProcess runtime resuming a previous turn or attaching an image
	// (Hermes' --resume/--image); claudeRuntime ignores both — Claude keeps
	// its own resume/session-id bookkeeping on Session and has no per-turn
	// image flag today.
	Args(s *Session, autonomous bool, convID, imagePath string) []string

	// NewParser returns a fresh stdout translator for one process: the whole
	// run for Claude (one process per task/session), one turn for Hermes
	// (one process per turn). See Parser's own doc comment for why a fresh
	// instance per process is required rather than one shared on the
	// Runtime value itself.
	NewParser() Parser
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
	if cfg.IsHermes() {
		return hermesRuntime{}
	}
	return claudeRuntime{}
}
