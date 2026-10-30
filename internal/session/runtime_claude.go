package session

// claudeRuntime is the Runtime implementation for the Claude CLI backend
// (RUNTIME-TASKS.md RT-05). It intentionally does not move buildCLIArgs or
// ParseLine out of session.go/parser.go — those stay exactly where the
// existing TestBuildCLIArgs_*/parser tests already exercise them — it just
// exposes them through the shared interface so a caller never has to know
// it's talking to Claude specifically.
type claudeRuntime struct{}

func (claudeRuntime) Name() string { return "claude" }

// PerTurnProcess is false: runOnce spawns one process for the whole
// task/session and keeps stdin open across turns (docs/runtimes.md §4).
func (claudeRuntime) PerTurnProcess() bool { return false }

// Args delegates to Session.buildCLIArgs, unchanged. convID/imagePath/prompt
// are ignored — Claude's resume/session-id bookkeeping lives on Session
// itself and its prompt goes over stdin.
func (claudeRuntime) Args(s *Session, autonomous bool, convID, imagePath, prompt string) []string {
	return s.buildCLIArgs(autonomous)
}

// NewParser wraps the stateless ParseLine so it satisfies Parser. Claude
// never buffers across lines, so Flush/LastError are no-ops.
func (claudeRuntime) NewParser() Parser { return &claudeParser{} }

func (claudeRuntime) Binary(s *Session) string { return s.claudePath }

// Env returns parent unchanged: Claude needs no scrubbing beyond what the
// session's own launch flags already set (utf8Env handles Python separately).
func (claudeRuntime) Env(_ *Session, parent []string) []string { return parent }

func (claudeRuntime) ClassifyStderr(line string) *TurnFailure { return ClassifyStderrLine(line) }

// Preamble returns "": Claude's prompt augmentation goes through the
// --append-system-prompt argv flag (buildCLIArgs), not the first turn's text.
func (claudeRuntime) Preamble(_ *Session, _ bool) string { return "" }

// WarnOptions is a no-op: every session option is meaningful to Claude.
func (claudeRuntime) WarnOptions(_ *Session) {}

// Capabilities: the full protocol — resume, effort, permission requests,
// USD cost and per-message token usage.
func (claudeRuntime) Capabilities() Capabilities {
	return Capabilities{Resume: true, Effort: true, PermissionProtocol: true, Cost: true, TokenUsage: true, Images: true}
}

type claudeParser struct{}

func (*claudeParser) Parse(line string) []ParsedEvent { return []ParsedEvent{ParseLine(line)} }
func (*claudeParser) Flush() []ParsedEvent            { return nil }
func (*claudeParser) LastError() string               { return "" }
