package session

import (
	"strings"
	"time"

	"claude-manager/internal/config"
)

// zcodeRuntime is the Runtime implementation for ZCode over the
// app-server protocol: each turn spawns `zcode app-server --stdio` (the
// NDJSON protocol the ZCode desktop itself drives its agent with), pushes
// the account provider registry, opens or resumes a protocol session and
// streams session/events until the turn ends — see zcode_appserver.go.
// This replaces the zsm file transport (.zsm inbox/outbox), which needed
// an external ZCode conversation to consume the orders.
type zcodeRuntime struct{}

func (zcodeRuntime) Name() string { return "zcode" }

// PerTurnProcess is true: one RunTurn call spawns one app-server process,
// runs exactly one turn and returns.
func (zcodeRuntime) PerTurnProcess() bool { return true }

// Args returns nil: RunTurn builds its own argv (Electron-as-Node +
// zcode.cjs app-server) — see zcodeDetectBinary.
func (zcodeRuntime) Args(_ *Session, _ bool, _, _, _ string) []string { return nil }

// NewParser returns a no-op parser: the zcode runtime implements TurnRunner,
// so no stdout stream is translated (the seam just has to hold).
func (zcodeRuntime) NewParser() Parser { return zcodeParser{} }

// Binary returns "": RunTurn launches the app-server itself and no other
// call site needs a path. (Detection lives in zcodeDetectBinary.)
func (zcodeRuntime) Binary(*Session) string { return "" }

// Env returns parent unchanged: RunTurn appends ELECTRON_RUN_AS_NODE itself.
func (zcodeRuntime) Env(_ *Session, parent []string) []string { return parent }

// ClassifyStderr never runs (the app-server logs to stdout; its failures
// arrive as turn.failed events, not stderr lines); it keeps the generic
// classifier so the seam stays satisfied.
func (zcodeRuntime) ClassifyStderr(line string) *TurnFailure { return ClassifyStderrLine(line) }

// Capabilities: resume works (session/resume of the persisted conversation);
// effort is native (modelSelection.options.reasoningLevel, GLM-5.3: low,
// high, max); token usage arrives via usage.delta events. Permission mode is
// honored natively: permission_mode maps onto the session mode enum
// (bypassPermissions → "yolo", acceptEdits → "edit", plan → "plan") passed to
// session/create and session/setMode. In non-yolo modes the manager
// registers no interactive gate — interaction/requestPermission is answered
// with a deny decision — so the capability stays off. No USD cost, no image
// attachments on this wire.
func (zcodeRuntime) Capabilities() Capabilities {
	return Capabilities{
		Resume: true, Effort: true, PermissionProtocol: false,
		Cost: false, TokenUsage: true, Images: false, PromptArg: false,
	}
}

// Preamble mirrors kimiRuntime's: the session's append text and the
// autonomous-run protocol ride in front of the first turn of a fresh
// conversation (a resumed one already has them) — the wire has no
// --append-system-prompt equivalent.
func (zcodeRuntime) Preamble(s *Session, autonomous bool) string {
	parts := []string{}
	if a := strings.TrimSpace(s.Config.SystemPromptAppend); a != "" {
		parts = append(parts, a)
	}
	if autonomous {
		parts = append(parts, askUserProtocolPrompt, backgroundTaskWarningPrompt)
	} else if s.Config.AskUserMarker {
		parts = append(parts, askUserInteractivePrompt)
	}
	return strings.Join(parts, "\n\n")
}

// WarnOptions logs, once per run, the session options the app-server
// transport silently cannot honour, so the user sees why.
func (zcodeRuntime) WarnOptions(s *Session) {
	var ignored []string
	c := s.Config
	if c.MaxBudgetUSD > 0 {
		ignored = append(ignored, "max_budget_usd")
	}
	if c.FallbackModel != "" {
		ignored = append(ignored, "fallback_model")
	}
	if len(c.AllowedTools) > 0 || len(c.DisallowedTools) > 0 {
		ignored = append(ignored, "allowed_tools/disallowed_tools")
	}
	if len(c.AddDirs) > 0 {
		ignored = append(ignored, "add_dirs")
	}
	if len(ignored) == 0 {
		return
	}
	s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
		Time: time.Now(), Level: "system", Source: "manager",
		Message: "ZCode runtime ignores: " + strings.Join(ignored, "; "),
	}})
}

// zcodeParser satisfies the Parser seam but never translates anything: the
// zcode runtime drives its turn through RunTurn's app-server protocol, so
// no stdout stream exists.
type zcodeParser struct{}

func (zcodeParser) Parse(string) []ParsedEvent { return nil }
func (zcodeParser) Flush() []ParsedEvent       { return nil }
func (zcodeParser) LastError() string          { return "" }
