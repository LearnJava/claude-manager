package session

import (
	"strings"
	"time"

	"claude-manager/internal/config"
)

// zcodeRuntime is the Runtime implementation for ZCode on the file transport,
// ported from zsm's engines/zcode_file.py. ZCode has no working external
// headless mode today (checked 2026-11-04, zai-org/feedback#29: three
// stacked authorization bugs), so the manager spawns no CLI process: each
// turn writes an order file under <workdir>/.zsm/inbox/ and a ZCode session
// running on its own picks it up and reports status in
// .zsm/outbox/<id>.json, which the turn polls (see zcode_file.go's RunTurn).
type zcodeRuntime struct{}

func (zcodeRuntime) Name() string { return "zcode" }

// PerTurnProcess is true in the loop's sense: one RunTurn call answers
// exactly one turn and returns; the "process" is the order/outbox file pair.
func (zcodeRuntime) PerTurnProcess() bool { return true }

// Args returns nil: there is no CLI process to build an argv for — the
// turn rides entirely through RunTurn's order files.
func (zcodeRuntime) Args(_ *Session, _ bool, _, _, _ string) []string { return nil }

// NewParser returns a no-op parser: the zcode runtime implements TurnRunner,
// so no stdout stream ever exists to translate (the seam just has to hold).
func (zcodeRuntime) NewParser() Parser { return zcodeParser{} }

// Binary returns "": the file transport launches nothing. Never called —
// RunTurn replaces hermesRunTurn, the only Binary call site.
func (zcodeRuntime) Binary(*Session) string { return "" }

// Env returns parent unchanged: with no child process there is nothing to
// scrub.
func (zcodeRuntime) Env(_ *Session, parent []string) []string { return parent }

// ClassifyStderr never runs (no process, no stderr); it keeps the generic
// classifier so the seam stays satisfied.
func (zcodeRuntime) ClassifyStderr(line string) *TurnFailure { return ClassifyStderrLine(line) }

// Capabilities: resume exists (the order carries conversation_id; the outbox
// reports it back); the permission protocol exists (outbox permission
// requests answered through the order file); token usage is reported by the
// outbox. Effort rides the order file as reasoning_effort (GLM-5.3: low,
// high, max) — applying it is up to the consuming ZCode session; no USD
// cost, no image attachments on this wire.
func (zcodeRuntime) Capabilities() Capabilities {
	return Capabilities{
		Resume: true, Effort: true, PermissionProtocol: true,
		Cost: false, TokenUsage: true, Images: false, PromptArg: false,
	}
}

// Preamble mirrors kimiRuntime's: the session's append text and the
// autonomous-run protocol ride in front of the first turn of a fresh
// conversation (a resumed one already has them) — the order file has no
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

// WarnOptions logs, once per run, the session options the file transport
// silently cannot honour, so the user sees why.
func (zcodeRuntime) WarnOptions(s *Session) {
	var ignored []string
	c := s.Config
	if c.PermissionMode != "" && c.PermissionMode != "bypassPermissions" {
		ignored = append(ignored, "permission_mode="+c.PermissionMode+
			" (the ZCode session applies its own policy; permission requests arrive via the outbox)")
	}
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
// zcode runtime drives its turn through RunTurn's order/outbox files, so no
// stdout stream exists.
type zcodeParser struct{}

func (zcodeParser) Parse(string) []ParsedEvent { return nil }
func (zcodeParser) Flush() []ParsedEvent       { return nil }
func (zcodeParser) LastError() string          { return "" }
