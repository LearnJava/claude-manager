package session

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/logger"
	"claude-manager/internal/proc"
)

// OpenCode CLI runtime (`opencode run --format json`, opencode v2).
//
// Like Hermes, one process answers exactly ONE turn and exits. The
// conversation lives in opencode's own store under the session id its first
// wire line reports, and every later turn is a fresh process started with
// `--session <id>` (one flag both creates and continues — no separate
// resume flag). The manager mints "ses"-prefixed ids because the CLI
// refuses session ids that do not start with "ses".
//
// runOnceOpenCode keeps the same contract runOnceHermes has for Run(): it
// returns nil when the task (autonomous) or the conversation (interactive,
// on Stop) is over, and the same sentinel errors for rate limits and auth
// failures. Hermes-only extras (its state.db step-limit scrape and
// agent.log 429 scrape) have no opencode counterpart and are simply absent.
//
// Headless `run` has no interactive permission prompt, so every launch
// passes --auto (auto-approve everything not explicitly denied); there is
// nothing to click "yes" on.

// openCodeVariants are the reasoning variants opencode models expose as
// `model#variant` selectors (observed on the catalog: low/medium/high/
// xhigh/max). The session's Effort maps onto one.
var openCodeVariants = map[string]bool{
	"low": true, "medium": true, "high": true, "xhigh": true, "max": true,
}

// openCodeTurn is the outcome of one `opencode run` process.
type openCodeTurn struct {
	finished  bool   // handleEvent reported the turn as finished
	sessionID string // opencode session id from the first wire line
	failed    string // non-empty when the turn reported an error
}

func (s *Session) runOnceOpenCode(ctx context.Context, forceInteractive bool) error {
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
	s.lastConvID = ""
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.cancelRun = nil
		s.inputCh = nil
		s.lastConvID = convID
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
	// opencode has no --append-system-prompt: the session's own append text
	// and the autonomous-run protocol ride in front of the FIRST turn of a
	// fresh conversation instead (a resumed one already has them). Same
	// preamble Hermes uses — its content is runtime-neutral.
	if convID == "" && prompt != "" {
		if pre := s.hermesPreamble(autonomous); pre != "" {
			prompt = pre + "\n\n---\n\n" + prompt
		}
	}
	s.warnUnsupportedOpenCodeOptions()

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

		turn, err := s.openCodeRunTurn(runCtx, prompt, images, convID, autonomous)
		prompt, images = "", nil
		if turn.sessionID != "" {
			convID = turn.sessionID
		}
		if ctx.Err() != nil {
			return nil
		}
		if sentinel := s.classifySentinelError(); sentinel != nil {
			return sentinel
		}
		if err != nil {
			return err
		}
		if turn.failed != "" {
			return fmt.Errorf("opencode turn failed: %s", turn.failed)
		}
		if autonomous && turn.finished {
			return nil
		}
		// Interactive session, or an autonomous one paused on an ask-user
		// question: the next turn starts when a message/answer arrives.
	}
}

// openCodeRunTurn launches one `opencode run` process for one user turn and
// streams its output through handleEvent.
func (s *Session) openCodeRunTurn(ctx context.Context, prompt string, images []ImageAttachment, convID string, autonomous bool) (openCodeTurn, error) {
	var turn openCodeTurn

	imagePath, cleanup, err := writeHermesImage(images)
	if err != nil {
		logger.L.Warn("session.opencode.image", "id", s.ID, "error", err)
	}
	defer cleanup()

	args := s.runtime.Args(s, autonomous, convID, imagePath)
	workDir := s.runDir()
	logger.L.Debug("session.launch", "id", s.ID, "opencode", s.openCodePath, "cwd", workDir,
		"args", strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, s.openCodePath, args...)
	proc.HideConsole(cmd)
	if workDir != "" {
		cmd.Dir = workDir
	}
	// The query goes through stdin (run reads it when no message argument is
	// given), never argv: nothing is shell- or argv-quoted, so any prompt
	// text arrives verbatim.
	cmd.Stdin = strings.NewReader(prompt)
	// hermesEnv is runtime-neutral in effect: pin TERMINAL_CWD to the
	// project, drop the parent's HERMES_* session state, force UTF-8.
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
		return turn, fmt.Errorf("start opencode: %w", err)
	}
	logger.L.Info("session.spawned", "id", s.ID, "pid", cmd.Process.Pid, "runtime", "opencode")
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

	if prompt != "" {
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "user", Source: "opencode", Message: prompt,
		}})
	}

	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		defer logger.Recover("session.drain_stderr", "id", s.ID)
		s.drainOpenCodeStderr(stderr)
	}()

	stream := newOpenCodeStream()
	s.setActivity(Activity{Kind: ActivityThinking}) // until the first event says otherwise
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
				s.applyFailure(ClassifyStderrLine(stream.lastError))
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

	if waitErr != nil && turn.failed == "" && ctx.Err() == nil {
		logger.L.Error("session.process_exit", "id", s.ID, "error", waitErr)
		return turn, fmt.Errorf("opencode exited: %w", waitErr)
	}
	if scanErr != nil {
		return turn, fmt.Errorf("stdout scan: %w", scanErr)
	}
	// A clean process exit ends the turn even when the last step_finish said
	// "tool-calls" (a truncated stream must not park an autonomous run loop
	// waiting for input no one will send). Interactive sessions keep
	// finished=false — the next turn is the user's next message.
	if autonomous && !turn.finished && turn.failed == "" && ctx.Err() == nil {
		turn.finished = true
	}
	logger.L.Info("session.process_exit", "id", s.ID, "ok", turn.failed == "", "runtime", "opencode")
	return turn, nil
}

// buildOpenCodeArgs is buildHermesArgs for the OpenCode runtime. Every flag
// here is one `opencode run --help` documents (v2.2.7).
func (s *Session) buildOpenCodeArgs(convID, imagePath string) []string {
	s.mu.Lock()
	model := s.activeModel
	effort := strings.ToLower(strings.TrimSpace(s.Config.Effort))
	s.mu.Unlock()
	if model == "" {
		model = s.Config.Model
	}

	args := []string{
		"run",
		"--format", "json",
		// Headless: no interactive permission prompt exists, so auto-approve
		// everything not explicitly denied.
		"--auto",
		// Keep manager-driven runs out of the user's own session lists.
		"--archived",
	}
	if convID != "" {
		args = append(args, "--session", convID)
	}
	if model != "" {
		selector := model
		if openCodeVariants[effort] && !strings.Contains(model, "#") {
			selector += "#" + effort
		}
		args = append(args, "--model", selector)
	}
	if imagePath != "" {
		args = append(args, "--file", imagePath)
	}
	return args
}

// warnUnsupportedOpenCodeOptions logs, once per run, the Claude-only session
// options an opencode session silently cannot honour, so the user sees why.
func (s *Session) warnUnsupportedOpenCodeOptions() {
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
		Message: "OpenCode runtime ignores: " + strings.Join(ignored, "; "),
	}})
}

// drainOpenCodeStderr is drainHermesStderr for opencode: diagnostics go to
// the feed as "system" lines; rate-limit and auth detection run on every
// line.
func (s *Session) drainOpenCodeStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 32*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		logger.L.Warn("session.stderr", "id", s.ID, "line", line)
		if info, ok := detectRateLimitText(line); ok {
			s.onRateLimit(info)
		}
		s.applyFailure(ClassifyStderrLine(line))
		s.emit(SessionEvent{Type: EvtLog, Entry: &config.LogEntry{
			Time: time.Now(), Level: "system", Source: "opencode", Message: line,
		}})
	}
}
