package session

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

// Args delegates to Session.buildHermesArgs, unchanged. autonomous is
// ignored here — Hermes has no --append-system-prompt equivalent passed as
// an argv flag; the autonomous protocol text instead rides in front of the
// first turn's prompt (hermesPreamble), not in Args.
func (hermesRuntime) Args(s *Session, autonomous bool, convID, imagePath string) []string {
	return s.buildHermesArgs(convID, imagePath)
}

// NewParser returns a fresh hermesStream: it must be new per process (one
// per turn) since it buffers streamed text and tracks synthetic tool-call
// ids scoped to a single `hermes chat` invocation (hermes_parser.go).
func (hermesRuntime) NewParser() Parser { return &hermesParserAdapter{stream: newHermesStream()} }

// hermesParserAdapter adapts *hermesStream to the shared Parser interface.
type hermesParserAdapter struct{ stream *hermesStream }

func (a *hermesParserAdapter) Parse(line string) []ParsedEvent { return a.stream.Parse(line) }
func (a *hermesParserAdapter) Flush() []ParsedEvent            { return a.stream.Flush() }
func (a *hermesParserAdapter) LastError() string               { return a.stream.lastError }
