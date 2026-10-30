package session

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
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
	"sonnet": "claude-sonnet-5-5",
	"haiku":  "claude-haiku-4-5",
}

// hermesModel returns the model id to pass to `hermes -m`.
func hermesModel(model string) string {
	if id, ok := hermesModelAliases[strings.ToLower(strings.TrimSpace(model))]; ok {
		return id
	}
	return model
}

// clarifyQuestions filters ev.Questions down to those sourced from a Hermes
// `clarify` call — the only source that needs the turn cut short mid-stream
// (hermesRunTurn's dispatch), as opposed to the ask-user marker, which only
// ever arrives on the final result line where the process is already exiting
// on its own.
func clarifyQuestions(qs []Question) []Question {
	var out []Question
	for _, q := range qs {
		if q.Source == QuestionSourceHermesClarify {
			out = append(out, q)
		}
	}
	return out
}

// hermesTurn is the outcome of one `hermes chat` process.
type hermesTurn struct {
	finished  bool   // handleEvent reported the turn as finished
	sessionID string // Hermes session id from the init line
	failed    string // non-empty when the result reported an error
	// questions holds a set of questions the turn was cut short on (RT-03:
	// currently only a Hermes `clarify` call — see hermesRunTurn); nil
	// otherwise.
	questions []Question
}

func (s *Session) runOncePerTurn(ctx context.Context, forceInteractive bool) error {
	autonomous := s.Config.AutoRestart && !forceInteractive
	s.emitRunStartLog("Готовлю промпт…")
	s.prepareWorkDir(ctx)

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
		s.questionSet = nil
		s.mu.Unlock()
	}()

	s.resetRunAtomics()

	prompt := s.initialPromptText(forceInteractive)
	var images []ImageAttachment
	// A per-turn runtime has no --append-system-prompt: the session's own
	// append text and the autonomous-run protocol ride in front of the FIRST
	// turn of a fresh conversation instead (a resumed one already has them).
	if convID == "" && prompt != "" {
		if pre := s.runtime.Preamble(s, autonomous); pre != "" {
			prompt = pre + "\n\n---\n\n" + prompt
		}
	}
	s.mu.Lock()
	if s.retryPrompt != "" || len(s.retryImages) > 0 {
		prompt, images = s.retryPrompt, s.retryImages
		s.retryPrompt, s.retryImages = "", nil
	}
	s.mu.Unlock()
	s.runtime.WarnOptions(s)

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

		turnPrompt, turnImages := prompt, images
		var turn hermesTurn
		var err error
		if runner, ok := s.runtime.(TurnRunner); ok {
			turn, err = runner.RunTurn(s, runCtx, prompt, images, convID, autonomous)
		} else {
			turn, err = s.hermesRunTurn(runCtx, prompt, images, convID, autonomous)
		}
		prompt, images = "", nil
		if turn.sessionID != "" {
			convID = turn.sessionID
		}
		if ctx.Err() != nil {
			return nil
		}
		if sentinel := s.classifySentinelError(); sentinel != nil {
			if errors.Is(sentinel, errTransientAPI) {
				s.mu.Lock()
				s.retryPrompt, s.retryImages = turnPrompt, turnImages
				s.mu.Unlock()
			}
			return sentinel
		}
		if err != nil {
			return err
		}
		if turn.failed != "" {
			return fmt.Errorf("hermes turn failed: %s", turn.failed)
		}
		if len(turn.questions) > 0 {
			// The agent asked the user something: wait for the answers,
			// which arrive on inputCh as the next turn's prompt.
			s.askQuestions(turn.questions, autonomous)
			continue
		}
		if autonomous && turn.finished {
			// An ask-user marker means the turn is over but the conversation
			// is paused on a human answer, not done: wait for it on inputCh
			// instead of ending the run (Claude's close-stdin-on-result would
			// have the same effect there).
			s.mu.Lock()
			waiting := s.pendingQuestion != nil
			s.mu.Unlock()
			if !waiting {
				return nil
			}
		}
		// Interactive session, or an autonomous one paused on an ask-user
		// question: the next turn starts when a message/answer arrives.
	}
}

// runOnceHermes is kept for the existing Hermes tests; the loop itself is
// runtime-generic and also drives Kimi (one process per turn).
func (s *Session) runOnceHermes(ctx context.Context, forceInteractive bool) error {
	return s.runOncePerTurn(ctx, forceInteractive)
}

// hermesRunTurn launches one CLI process for one user turn and streams its
// output through handleEvent.
func (s *Session) hermesRunTurn(ctx context.Context, prompt string, images []ImageAttachment, convID string, autonomous bool) (hermesTurn, error) {
	var turn hermesTurn

	imagePath, cleanup, err := writeHermesImage(images)
	if err != nil {
		logger.L.Warn("session.hermes.image", "id", s.ID, "error", err)
	}
	defer cleanup()
	if len(images) > 0 && !s.runtime.Capabilities().Images {
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "system", Source: "manager",
			Message: s.runtime.Name() + " runtime does not accept image attachments — sending text only",
		}})
		imagePath = ""
	}

	shown := prompt
	args := s.runtime.Args(s, autonomous, convID, imagePath, prompt)
	if ex, ok := s.runtime.(TurnExpander); ok {
		var extra []string
		prompt, extra = ex.ExpandPrompt(s, prompt)
		args = append(args, extra...)
	}
	turnStart := time.Now()
	workDir := s.runDir()
	logger.L.Debug("session.launch", "id", s.ID, "runtime", s.runtime.Name(), "binary", s.runtime.Binary(s), "cwd", workDir,
		"args", strings.Join(args, " "))

	// turnCtx lets a `clarify` call end this one process without ending the run.
	turnCtx, cancelTurn := context.WithCancel(ctx)
	defer cancelTurn()
	cmd := exec.CommandContext(turnCtx, s.runtime.Binary(s), args...)
	proc.HideConsole(cmd)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if s.runtime.Capabilities().PromptArg {
		// The prompt is already inline in argv (kimi -p) — nothing on stdin.
		cmd.Stdin = strings.NewReader("")
	} else {
		// The query goes through stdin (--query-file -), never argv: nothing is
		// shell- or argv-quoted, so any prompt text arrives verbatim.
		cmd.Stdin = strings.NewReader(prompt)
	}
	cmd.Env = s.runtime.Env(s, os.Environ())

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
	logger.L.Info("session.spawned", "id", s.ID, "pid", cmd.Process.Pid, "runtime", s.runtime.Name())
	s.beginStartWatch(cmd.Process.Pid)
	defer s.endStartWatch()

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

	if shown != "" {
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "user", Source: "hermes", Message: shown,
		}})
	}

	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		defer logger.Recover("session.drain_stderr", "id", s.ID)
		s.drainHermesStderr(stderr)
	}()

	parser := s.runtime.NewParser()
	s.setActivity(Activity{Kind: ActivityThinking}) // until the first event says otherwise
	dispatch := func(evs []ParsedEvent) {
		for _, ev := range evs {
			if ev.EventType == EventInit && ev.Init != nil {
				turn.sessionID = ev.Init.SessionID
			}
			if ev.EventType == EventResult && ev.Result != nil && ev.Result.StopReason == "error" {
				turn.failed = parser.LastError()
				if info, ok := detectRateLimitText(parser.LastError()); ok {
					s.onRateLimit(info)
				}
				s.applyFailure(ClassifyStderrLine(parser.LastError()))
			}
			if s.handleEvent(ev, autonomous) {
				turn.finished = true
			}
			if qs := clarifyQuestions(ev.Questions); len(qs) > 0 {
				// Headless Hermes answers clarify itself ("no user available,
				// pick the best option") and the agent carries on with its own
				// guess. Hermes has already persisted the call and that
				// answer, so the process is stopped here and the real answers
				// continue the conversation via --resume.
				turn.questions = qs
				logger.L.Info("session.hermes.clarify", "id", s.ID, "questions", len(qs))
				cancelTurn()
				_ = stdout.Close()
				return
			}
		}
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for turn.questions == nil && scanner.Scan() {
		dispatch(parser.Parse(scanner.Text()))
	}
	if turn.questions == nil {
		dispatch(parser.Flush())
	}
	scanErr := scanner.Err()
	<-stderrDone
	waitErr := cmd.Wait()
	if turn.sessionID == "" {
		s.mu.Lock()
		turn.sessionID = s.wireInitID
		s.wireInitID = ""
		s.mu.Unlock()
	}

	if fin, ok := s.runtime.(TurnFinisher); ok {
		fin.AfterTurn(s, turn, convID, turnStart)
	}

	if turn.questions != nil {
		s.setActivity(Activity{Kind: ActivityIdle})
		logger.L.Info("session.process_exit", "id", s.ID, "clarify", true, "runtime", s.runtime.Name())
		return turn, nil
	}
	if waitErr != nil && turn.failed == "" && ctx.Err() == nil {
		logger.L.Error("session.process_exit", "id", s.ID, "error", waitErr)
		return turn, fmt.Errorf("%s exited: %w", s.runtime.Name(), waitErr)
	}
	if scanErr != nil {
		return turn, fmt.Errorf("stdout scan: %w", scanErr)
	}
	logger.L.Info("session.process_exit", "id", s.ID, "ok", turn.failed == "", "runtime", s.runtime.Name())
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
		parts = append(parts, askUserProtocolPrompt, hermesBackgroundTaskWarningPrompt)
	} else if s.Config.AskUserMarker {
		parts = append(parts, askUserInteractivePrompt)
	}
	return strings.Join(parts, "\n\n")
}

// hermesBackgroundTaskWarningPrompt is Hermes's counterpart of
// backgroundTaskWarningPrompt, whose premise ("killed the moment your reply
// ends, you will never be notified") is false here: a one-shot `hermes chat`
// lingers after the final reply for every notify_on_complete process still
// running (terminal.oneshot_completion_wait_seconds, 600s by default) and
// feeds each completion back as one more turn. Observed on S4: a baseline
// run left in the background held the process ~20 minutes past the agent's
// continue_session marker, then produced an extra reply without the marker.
const hermesBackgroundTaskWarningPrompt = "A background process still running when your reply ends keeps this " +
	"session alive until it finishes, and its completion notice comes back to you as one more turn — time spent " +
	"waiting, not working, and the next task cannot start meanwhile. So before you end your turn, and always " +
	"before the continue_session block: wait (process tool, action \"wait\") for any background process whose " +
	"result you still need, and kill (action \"kill\") every one you no longer need. If some work genuinely " +
	"cannot finish in this session, record its progress in a file your next session (a fresh process) can read " +
	"and resume from, instead of leaving it running."

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

// drainHermesStderr is drainStderr for a per-turn runtime. Lines are logged
// as "system" (Hermes writes diagnostics and its own `session_id: …` trailer
// to stderr on every successful turn); rate-limit and auth detection still
// run on every line, classified by the session's own runtime.
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
		if initer, ok := s.runtime.(StderrIniter); ok {
			if sid, found := initer.SessionIDFromStderr(line); found {
				s.mu.Lock()
				s.wireInitID = sid
				s.mu.Unlock()
				s.emit(SessionEvent{Type: EvtInit, Init: &InitInfo{SessionID: sid}})
			}
		}
		s.applyFailure(s.runtime.ClassifyStderr(line))
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "system", Source: s.runtime.Name(), Message: line,
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

// clarifyAnswerMessage lives in session.go (RT-03: shared with the ask-user
// marker's single-question path via pendingQuestionSet).
