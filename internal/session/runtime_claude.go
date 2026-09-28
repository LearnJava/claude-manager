package session

// claudeRuntime is the Runtime implementation for the Claude CLI backend
// (RUNTIME-TASKS.md RT-05). It intentionally does not move buildCLIArgs or
// ParseLine out of session.go/parser.go — those stay exactly where the
// existing TestBuildCLIArgs_*/parser tests already exercise them (RT-05's
// "готово когда" requires those tests to pass unmodified) — it just exposes
// them through the shared interface so a caller never has to know it's
// talking to Claude specifically.
type claudeRuntime struct{}

func (claudeRuntime) Name() string { return "claude" }

// PerTurnProcess is false: runOnce spawns one process for the whole
// task/session and keeps stdin open across turns (docs/runtimes.md §4).
func (claudeRuntime) PerTurnProcess() bool { return false }

// Args delegates to Session.buildCLIArgs, unchanged. convID/imagePath are
// ignored — Claude's resume/session-id bookkeeping lives on Session itself
// (resumeSessionID, CLISessionID), and it has no per-turn image flag.
func (claudeRuntime) Args(s *Session, autonomous bool, convID, imagePath string) []string {
	return s.buildCLIArgs(autonomous)
}

// NewParser wraps the stateless ParseLine so it satisfies Parser. Claude
// never buffers across lines, so Flush/LastError are no-ops.
func (claudeRuntime) NewParser() Parser { return &claudeParser{} }

type claudeParser struct{}

func (*claudeParser) Parse(line string) []ParsedEvent { return []ParsedEvent{ParseLine(line)} }
func (*claudeParser) Flush() []ParsedEvent             { return nil }
func (*claudeParser) LastError() string                { return "" }
