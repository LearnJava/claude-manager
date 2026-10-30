package session

import (
	"regexp"
	"strings"
	"time"

	"claude-manager/internal/config"
)

// kimiRuntime is the Runtime implementation for the Kimi Code CLI backend.
// One `kimi -p --output-format stream-json` process answers exactly one turn
// and exits; the conversation lives in Kimi's own session store and every
// later turn is a fresh process started with --session <id> (the Hermes
// process model, docs/runtimes.md §4).
type kimiRuntime struct{}

func (kimiRuntime) Name() string { return "kimi" }

// PerTurnProcess is true: `kimi -p` runs a single prompt non-interactively
// and exits after its result line — there is no long-lived stdin conversation.
func (kimiRuntime) PerTurnProcess() bool { return true }

// Args builds `kimi -p <prompt> --output-format stream-json [--session id]
// [-m model]`. The prompt goes inline as the -p value (PromptArg), never over
// stdin. convID/imagePath: convID resumes the Kimi session; images are not
// supported by -p and are dropped by the caller. autonomous is ignored — the
// protocol text rides in front of the first turn's prompt (Preamble).
func (kimiRuntime) Args(s *Session, autonomous bool, convID, imagePath, prompt string) []string {
	return s.buildKimiArgs(convID, prompt)
}

// NewParser returns a fresh kimiStream per process (one per turn): it tracks
// synthetic tool-call ids scoped to a single `kimi -p` invocation.
func (kimiRuntime) NewParser() Parser { return newKimiStream() }

func (kimiRuntime) Binary(s *Session) string { return s.kimiPath }

// Env returns parent unchanged: the Kimi CLI needs no environment scrubbing.
func (kimiRuntime) Env(_ *Session, parent []string) []string { return parent }

// ClassifyStderr maps Kimi's own failure texts onto TurnFailure. The
// Desktop-instance conflict is treated like a lost resume target: the Run
// loop clears the stale id and starts a fresh conversation instead of
// fighting over the session Kimi Desktop holds open.
func (kimiRuntime) ClassifyStderr(line string) *TurnFailure {
	if strings.Contains(line, "Session not found:") || strings.Contains(line, "in use by another Kimi Code Desktop instance") {
		return &TurnFailure{Kind: KindSessionNotFound, Message: line}
	}
	if hasTransientAPIMarker(line) {
		return &TurnFailure{Kind: KindTransientAPI, Message: line}
	}
	return ClassifyStderrLine(line)
}

// Capabilities: resume exists; effort, the permission protocol, USD cost,
// token usage and image attachments do not (kimi -p runs under the auto
// policy and reports no usage/cost on the wire — the UI shows "no data").
func (kimiRuntime) Capabilities() Capabilities {
	return Capabilities{
		Resume: true, Effort: false, PermissionProtocol: false,
		Cost: false, TokenUsage: false, Images: false, PromptArg: true,
	}
}

// Preamble mirrors hermesPreamble: the session's append text and the
// autonomous-run protocol ride in front of the first turn of a fresh
// conversation (a resumed one already has them).
func (kimiRuntime) Preamble(s *Session, autonomous bool) string {
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

// WarnOptions logs, once per run, the Claude-only session options a Kimi
// session silently cannot honour, so the user sees why.
func (kimiRuntime) WarnOptions(s *Session) {
	var ignored []string
	c := s.Config
	if c.Effort != "" {
		ignored = append(ignored, "effort (kimi -p has no reasoning-effort flag)")
	}
	if c.PermissionMode != "" && c.PermissionMode != "bypassPermissions" {
		ignored = append(ignored, "permission_mode="+c.PermissionMode+
			" (kimi -p always runs under the auto policy: no approval questions)")
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
		Message: "Kimi runtime ignores: " + strings.Join(ignored, "; "),
	}})
}

// SessionIDFromStderr extracts a Kimi session id from a resume_hint line
// ("To resume this session: kimi -r session_<uuid>") when the hint lands on
// stderr instead of stdout.
func (kimiRuntime) SessionIDFromStderr(line string) (string, bool) {
	if !strings.Contains(line, "session_") {
		return "", false
	}
	m := kimiSessionIDPattern.FindString(line)
	if m == "" {
		return "", false
	}
	return m, true
}

var kimiSessionIDPattern = regexp.MustCompile(`session_[0-9a-fA-F-]+`)

// buildKimiArgs constructs the argv for one `kimi -p` turn.
func (s *Session) buildKimiArgs(convID, prompt string) []string {
	s.mu.Lock()
	model := s.activeModel
	s.mu.Unlock()
	if model == "" {
		model = s.Config.Model
	}

	args := []string{"-p", prompt, "--output-format", "stream-json"}
	if convID != "" {
		args = append(args, "--session", convID)
	}
	if model != "" {
		args = append(args, "-m", model)
	}
	return args
}
