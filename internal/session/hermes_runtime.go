package session

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/logger"
	"claude-manager/internal/proc"
)

// Hermes CLI runtime (HERMES-TASKS.md HR-04/05).
//
// A session with runtime = "hermes" drives `hermes chat --query-file -
// --format stream-json`. Unlike Claude CLI, one Hermes process answers
// exactly ONE turn and exits after its `result` line — there is no
// long-lived stdin conversation. The conversation lives in Hermes' own
// state.db under the session_id its `init` line reports, and every later
// turn is a fresh process started with `--resume <session_id>`.
//
// runOnceHermes keeps the same contract runOnce has for Run(): it returns
// nil when the task (autonomous) or the conversation (interactive, on Stop)
// is over, and the same sentinel errors for rate limits, auth failures and
// context restarts. For an interactive session it loops: one turn, then wait
// on inputCh for the next user message (SendMessage/AnswerQuestion queue into
// it exactly as they do for Claude), then the next `--resume` turn.

// hermesEfforts are the levels `hermes chat --reasoning` accepts.
var hermesEfforts = map[string]bool{
	"none": true, "minimal": true, "low": true, "medium": true,
	"high": true, "xhigh": true, "max": true, "ultra": true,
}

// hermesKeepEnv are the HERMES_* variables a child `hermes chat` may inherit:
// where Hermes lives and which bash it uses. Everything else HERMES_* is
// per-session state of whatever Hermes process launched the manager.
var hermesKeepEnv = map[string]bool{
	"HERMES_HOME":          true,
	"HERMES_GIT_BASH_PATH": true,
}

// hermesEnv builds the environment of one `hermes chat` process.
//
// The manager may itself be started from inside a Hermes session (a Hermes
// desktop terminal, an agent running `claude-manager.exe`), and then carries
// that session's state in its environment. Two pieces of it break a child:
//   - TERMINAL_CWD beats the process cwd for Hermes' terminal/file tools, so
//     an inherited one (e.g. C:\Users\x) sends every relative path and
//     context-file lookup (AGENTS.md, CLAUDE.md) outside the project —
//     observed: the agent went looking for docs/roles/P6.md across C:\.
//   - HERMES_SESSION_*, HERMES_MAX_ITERATIONS, HERMES_EXEC_ASK, … describe
//     the parent's session, not this one.
//
// TERMINAL_CWD is therefore pinned to the project folder, the parent's other
// HERMES_* variables are dropped, and Python is forced to UTF-8 so Cyrillic
// prompts and tool output survive the Windows ANSI code page.
func hermesEnv(parent []string, projectPath string) []string {
	env := make([]string, 0, len(parent)+4)
	for _, kv := range parent {
		name, _, _ := strings.Cut(kv, "=")
		upper := strings.ToUpper(name)
		if upper == "TERMINAL_CWD" || upper == "PYTHONIOENCODING" || upper == "PYTHONUTF8" {
			continue
		}
		if strings.HasPrefix(upper, "HERMES_") && !hermesKeepEnv[upper] {
			continue
		}
		env = append(env, kv)
	}
	if projectPath != "" {
		env = append(env, "TERMINAL_CWD="+projectPath)
	}
	return append(env, "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
}

// hermesModelAliases maps the Claude CLI aliases a session config typically
// carries (the Settings dropdown writes them) to exact model ids. Hermes
// resolves these aliases against the live models.dev catalog and rejects
// them as ambiguous ("alias 'opus' matches 8 models on anthropic"), so a
// session switched from claude to hermes with model = "opus" failed every
// turn with HTTP 404. The ids are what Claude CLI itself resolves them to on
// this account (session_runs.model).
var hermesModelAliases = map[string]string{
	"opus":   "claude-opus-5-5",
	"sonnet": "claude-sonnet-5",
	"haiku":  "claude-haiku-4-5",
}

// hermesModel returns the model id to pass to `hermes -m`.
func hermesModel(model string) string {
	if id, ok := hermesModelAliases[strings.ToLower(strings.TrimSpace(model))]; ok {
		return id
	}
	return model
}

// hermesTurn is the outcome of one `hermes chat` process.
type hermesTurn struct {
	finished  bool   // handleEvent reported the turn as finished
	sessionID string // Hermes session id from the init line
	failed    string // non-empty when the result reported an error
}

func (s *Session) runOnceHermes(ctx context.Context, forceInteractive bool) error {
	autonomous := (s.Config.AutoRestart || s.Config.StopWhenNoTasks) && !forceInteractive

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	inputCh := make(chan []byte, 16)
	s.mu.Lock()
	s.cancelRun = cancel
	s.inputCh = inputCh
	convID := s.resumeSessionID
	s.lastHermesConv = ""
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancelRun = nil
		s.inputCh = nil
		s.lastHermesConv = convID
		if s.questionTimer != nil {
			s.questionTimer.Stop()
			s.questionTimer = nil
		}
		s.pendingQuestion = nil
		s.mu.Unlock()
	}()

	s.rateLimited.Store(false)
	s.rateLimitInf.Store(nil)
	s.authErrorHit.Store(false)
	s.sessionNotFoundHit.Store(false)
	s.contextRestartHit.Store(false)
	s.continueMarkerHit.Store(false)
	s.stepLimitHit.Store(false)

	prompt := s.initialPromptText(forceInteractive)
	var images []ImageAttachment
	// Hermes has no --append-system-prompt: the session's own append text
	// and the autonomous-run protocol ride in front of the FIRST turn of a
	// fresh conversation instead (a resumed one already has them).
	if convID == "" && prompt != "" {
		if pre := s.hermesPreamble(autonomous); pre != "" {
			prompt = pre + "\n\n---\n\n" + prompt
		}
	}
	s.warnUnsupportedHermesOptions()

	s.setStatus(config.StatusWorking)
	for {
		if prompt == "" && len(images) == 0 {
			// Nothing to send yet (a bare "Chat" session, or between turns):
			// wait for the user.
			select {
			case <-runCtx.Done():
				return nil
			case line := <-inputCh:
				prompt, images = decodeInputLine(line)
				continue
			}
		}

		turn, err := s.hermesRunTurn(runCtx, prompt, images, convID, autonomous)
		prompt, images = "", nil
		if turn.sessionID != "" {
			convID = turn.sessionID
		}
		if ctx.Err() != nil {
			return nil
		}
		if s.authErrorHit.Load() {
			return errAuthError
		}
		if s.sessionNotFoundHit.Load() {
			return errSessionNotFound
		}
		if s.rateLimited.Load() {
			return errRateLimited
		}
		if s.contextRestartHit.Load() {
			return errContextRestart
		}
		if err != nil {
			return err
		}
		if turn.failed != "" {
			return fmt.Errorf("hermes turn failed: %s", turn.failed)
		}
		if autonomous && turn.sessionID != "" && hermesTurnHitStepLimit(s.hermesStateDB(), turn.sessionID) {
			s.stepLimitHit.Store(true)
			logger.L.Warn("session.hermes.step_limit", "id", s.ID, "conversation", turn.sessionID)
			s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
				Time: time.Now(), Level: "system", Source: "manager",
				Message: "Turn stopped by Hermes' step limit (--max-turns) — the agent was cut off mid-task",
			}})
		}
		if autonomous && turn.finished {
			return nil
		}
		// Interactive session, or an autonomous one paused on an ask-user
		// question: the next turn starts when a message/answer arrives.
	}
}

// hermesRunTurn launches one `hermes chat` process for one user turn and
// streams its output through handleEvent.
func (s *Session) hermesRunTurn(ctx context.Context, prompt string, images []ImageAttachment, convID string, autonomous bool) (hermesTurn, error) {
	var turn hermesTurn

	imagePath, cleanup, err := writeHermesImage(images)
	if err != nil {
		logger.L.Warn("session.hermes.image", "id", s.ID, "error", err)
	}
	defer cleanup()

	args := s.buildHermesArgs(convID, imagePath)
	turnStart := time.Now()
	logger.L.Debug("session.launch", "id", s.ID, "hermes", s.hermesPath, "cwd", s.ProjectPath,
		"args", strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, s.hermesPath, args...)
	proc.HideConsole(cmd)
	if s.ProjectPath != "" {
		cmd.Dir = s.ProjectPath
	}
	// The query goes through stdin (--query-file -), never argv: nothing is
	// shell- or argv-quoted, so any prompt text arrives verbatim.
	cmd.Stdin = strings.NewReader(prompt)
	cmd.Env = hermesEnv(os.Environ(), s.ProjectPath)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return turn, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return turn, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		logger.L.Error("session.spawn_failed", "id", s.ID, "error", err)
		return turn, fmt.Errorf("start hermes: %w", err)
	}
	logger.L.Info("session.spawned", "id", s.ID, "pid", cmd.Process.Pid, "runtime", "hermes")

	job, jobErr := proc.NewJob()
	if jobErr != nil {
		logger.L.Warn("session.job_create_failed", "id", s.ID, "error", jobErr)
	}
	defer job.Close()
	if err := job.Assign(cmd.Process.Pid); err != nil {
		logger.L.Warn("session.job_assign_failed", "id", s.ID, "pid", cmd.Process.Pid, "error", err)
	}

	s.mu.Lock()
	s.cmd = cmd
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cmd = nil
		s.mu.Unlock()
	}()

	if prompt != "" {
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "user", Source: "hermes", Message: prompt,
		}})
	}

	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		defer logger.Recover("session.drain_stderr", "id", s.ID)
		s.drainHermesStderr(stderr)
	}()

	stream := newHermesStream()
	dispatch := func(evs []ParsedEvent) {
		for _, ev := range evs {
			if ev.EventType == EventInit && ev.Init != nil {
				turn.sessionID = ev.Init.SessionID
			}
			if ev.EventType == EventResult && ev.Result != nil && ev.Result.StopReason == "error" {
				turn.failed = stream.lastError
				if info, ok := detectRateLimitText(stream.lastError); ok {
					s.onRateLimit(info)
				}
				if isAuthError(stream.lastError) {
					s.authErrorHit.Store(true)
				}
			}
			if s.handleEvent(ev, autonomous) {
				turn.finished = true
			}
		}
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		dispatch(stream.Parse(scanner.Text()))
	}
	dispatch(stream.Flush())
	scanErr := scanner.Err()
	<-stderrDone
	waitErr := cmd.Wait()

	// A failed turn whose own log lines show a 429 was a spent usage limit,
	// whatever error Hermes reported last (see hermesTurnHitRateLimit).
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

	if waitErr != nil && turn.failed == "" && ctx.Err() == nil {
		logger.L.Error("session.process_exit", "id", s.ID, "error", waitErr)
		return turn, fmt.Errorf("hermes exited: %w", waitErr)
	}
	if scanErr != nil {
		return turn, fmt.Errorf("stdout scan: %w", scanErr)
	}
	logger.L.Info("session.process_exit", "id", s.ID, "ok", turn.failed == "", "runtime", "hermes")
	return turn, nil
}

// buildHermesArgs is buildCLIArgs for the Hermes runtime. Every flag here is
// one `hermes chat --help` documents (v0.21).
func (s *Session) buildHermesArgs(convID, imagePath string) []string {
	s.mu.Lock()
	model := s.activeModel
	s.mu.Unlock()
	if model == "" {
		model = s.Config.Model
	}

	var args []string
	if p := strings.TrimSpace(s.Config.HermesProfile); p != "" {
		args = append(args, "-p", p) // global flag: must precede the subcommand
	}
	args = append(args, "chat",
		"--query-file", "-",
		"--format", "stream-json",
		// Keep manager-driven runs out of the user's own `hermes sessions` list.
		"--source", "tool",
	)
	if convID != "" {
		args = append(args, "--resume", convID)
	}
	maxTurns := s.Config.HermesMaxTurns
	if maxTurns <= 0 {
		maxTurns = hermesMaxTurnsDefault
	}
	args = append(args, "--max-turns", strconv.Itoa(maxTurns))
	if model != "" {
		args = append(args, "-m", hermesModel(model))
	}
	if p := strings.TrimSpace(s.Config.HermesProvider); p != "" {
		args = append(args, "--provider", p)
	}
	if e := strings.ToLower(strings.TrimSpace(s.Config.Effort)); hermesEfforts[e] {
		args = append(args, "--reasoning", e)
	}
	for _, sk := range s.Config.HermesSkills {
		if sk = strings.TrimSpace(sk); sk != "" {
			args = append(args, "-s", sk)
		}
	}
	if s.Config.PermissionMode == "bypassPermissions" {
		args = append(args, "--yolo")
	}
	if s.Config.UseWorktree {
		args = append(args, "--worktree")
	}
	if imagePath != "" {
		args = append(args, "--image", imagePath)
	}
	return args
}

// hermesPreamble is the text sent ahead of the first turn in place of
// Claude's --append-system-prompt.
func (s *Session) hermesPreamble(autonomous bool) string {
	parts := []string{}
	if a := strings.TrimSpace(s.Config.SystemPromptAppend); a != "" {
		parts = append(parts, a)
	}
	if autonomous {
		parts = append(parts, askUserProtocolPrompt, backgroundTaskWarningPrompt)
	}
	return strings.Join(parts, "\n\n")
}

// warnUnsupportedHermesOptions logs, once per run, the Claude-only session
// options a Hermes session silently cannot honour, so the user sees why.
func (s *Session) warnUnsupportedHermesOptions() {
	var ignored []string
	c := s.Config
	if c.MaxBudgetUSD > 0 {
		ignored = append(ignored, "max_budget_usd")
	}
	if c.FallbackModel != "" {
		ignored = append(ignored, "fallback_model (use fallback_providers in the Hermes profile)")
	}
	if len(c.AllowedTools) > 0 || len(c.DisallowedTools) > 0 {
		ignored = append(ignored, "allowed_tools/disallowed_tools")
	}
	if len(c.AddDirs) > 0 {
		ignored = append(ignored, "add_dirs")
	}
	if c.PermissionMode != "" && c.PermissionMode != "bypassPermissions" {
		ignored = append(ignored, "permission_mode="+c.PermissionMode+
			" (no interactive approvals: Hermes applies its own approvals policy to dangerous commands)")
	}
	if len(ignored) == 0 {
		return
	}
	s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
		Time: time.Now(), Level: "system", Source: "manager",
		Message: "Hermes runtime ignores: " + strings.Join(ignored, "; "),
	}})
}

// drainHermesStderr is drainStderr for Hermes. Hermes writes diagnostics
// and its own `session_id: …` trailer to stderr on every successful turn, so
// lines are logged as "system", not "error"; rate-limit and auth detection
// still run on every line.
func (s *Session) drainHermesStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 32*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "session_id:") {
			continue
		}
		logger.L.Warn("session.stderr", "id", s.ID, "line", line)
		if info, ok := detectRateLimitText(line); ok {
			s.onRateLimit(info)
		}
		if isAuthError(line) {
			s.authErrorHit.Store(true)
		}
		if isSessionNotFoundError(line) {
			s.sessionNotFoundHit.Store(true)
		}
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "system", Source: "hermes", Message: line,
		}})
	}
}

// decodeInputLine turns one queued stdin line (the Claude stream-json user
// envelope SendMessage/AnswerQuestion marshal) back into text + images.
func decodeInputLine(line []byte) (string, []ImageAttachment) {
	var msg struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return strings.TrimSpace(string(line)), nil
	}
	var text string
	if err := json.Unmarshal(msg.Message.Content, &text); err == nil {
		return text, nil
	}
	var blocks []struct {
		Type   string `json:"type"`
		Text   string `json:"text"`
		Source struct {
			MediaType string `json:"media_type"`
			Data      string `json:"data"`
		} `json:"source"`
	}
	if err := json.Unmarshal(msg.Message.Content, &blocks); err != nil {
		return "", nil
	}
	var texts []string
	var images []ImageAttachment
	for _, b := range blocks {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "image":
			images = append(images, ImageAttachment{MediaType: b.Source.MediaType, DataBase64: b.Source.Data})
		}
	}
	return strings.Join(texts, "\n"), images
}

// writeHermesImage materializes the first pasted image as a temp file for
// `--image` (Hermes takes one local image path per query). Extra images are
// dropped with a log line.
func writeHermesImage(images []ImageAttachment) (string, func(), error) {
	noop := func() {}
	if len(images) == 0 {
		return "", noop, nil
	}
	if len(images) > 1 {
		logger.L.Warn("session.hermes.images_dropped", "kept", 1, "dropped", len(images)-1)
	}
	data, err := base64.StdEncoding.DecodeString(images[0].DataBase64)
	if err != nil {
		return "", noop, err
	}
	ext := ".png"
	switch images[0].MediaType {
	case "image/jpeg":
		ext = ".jpg"
	case "image/gif":
		ext = ".gif"
	case "image/webp":
		ext = ".webp"
	}
	f, err := os.CreateTemp("", "cm-hermes-*"+ext)
	if err != nil {
		return "", noop, err
	}
	path := f.Name()
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil || cerr != nil {
		_ = os.Remove(path)
		return "", noop, fmt.Errorf("write image: %v %v", werr, cerr)
	}
	return filepath.Clean(path), func() { _ = os.Remove(path) }, nil
}
