package session

// openCodeRuntime is the Runtime implementation for the OpenCode CLI backend
// (v2, `opencode run --format json`). Like hermesRuntime it wraps the
// buildOpenCodeArgs/opencodeStream pair rather than relocating them — those
// stay where opencode_runtime.go's/opencode_parser.go's tests exercise them.
type openCodeRuntime struct{}

func (openCodeRuntime) Name() string { return "opencode" }

// PerTurnProcess is true: one `opencode run` process answers exactly one
// turn and exits; the conversation continues via `--session <id>` on the
// next turn (docs/runtimes.md §4's per-turn process model).
func (openCodeRuntime) PerTurnProcess() bool { return true }

// Args delegates to Session.buildOpenCodeArgs, unchanged. autonomous is
// ignored — opencode has no --append-system-prompt; the autonomous protocol
// text rides in front of the first turn's prompt (hermesPreamble, reused:
// its content is runtime-neutral) instead of Args.
func (openCodeRuntime) Args(s *Session, autonomous bool, convID, imagePath string) []string {
	return s.buildOpenCodeArgs(convID, imagePath)
}

// NewParser returns a fresh opencodeStream: it must be new per process (one
// per turn) since it buffers text and tracks the init-once state scoped to
// a single `opencode run` invocation (opencode_parser.go).
func (openCodeRuntime) NewParser() Parser { return &openCodeParserAdapter{stream: newOpenCodeStream()} }

// openCodeParserAdapter adapts *opencodeStream to the shared Parser interface.
type openCodeParserAdapter struct{ stream *opencodeStream }

func (a *openCodeParserAdapter) Parse(line string) []ParsedEvent { return a.stream.Parse(line) }
func (a *openCodeParserAdapter) Flush() []ParsedEvent            { return a.stream.Flush() }
func (a *openCodeParserAdapter) LastError() string               { return a.stream.lastError }
