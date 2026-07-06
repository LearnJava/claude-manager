package session

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/hooks"
	"claude-manager/internal/logger"
	"claude-manager/internal/proc"

	"github.com/google/uuid"
)

// ---- Event types emitted to the manager via callback ----

const (
	EvtStatus     = "status"
	EvtLog        = "log"
	EvtInit       = "init"
	EvtResult     = "result"
	EvtRateLimit  = "rate_limit"
	EvtPermission = "permission"
	EvtUsage      = "usage"
	EvtTaskDone   = "task_done"
	EvtTodo       = "todo"
	EvtError      = "error"
)

// SessionEvent is the payload passed to the manager-provided callback.
// Only fields relevant to the Type are populated.
type SessionEvent struct {
	Type       string
	Status     config.SessionStatus
	Entry      *config.LogEntry
	Init       *InitInfo
	Result     *SessionResult
	RateLimit  *RateLimitInfo
	Permission *PermissionRequest
	Usage      *TokenUsage
	TasksDone  int
	Todos      []TodoItem
	Err        error
}

// EventCallback is invoked by the session for every event. The manager is
// responsible for forwarding these to Wails / the UI.
type EventCallback func(sessionID string, ev SessionEvent)

// ---- Stdin protocol messages ----

// InputMessage is a stream-json user message written to stdin. The Claude CLI
// (--input-format stream-json) expects the Anthropic message envelope, i.e.
// {"type":"user","message":{"role":"user","content":"..."}}. The earlier
// {"type":"user_message","message":"..."} shape is NOT recognised by the CLI
// and makes the process hang waiting on stdin.
type InputMessage struct {
	Type    string       `json:"type"`
	Message InputPayload `json:"message"`
}

// InputPayload is the nested Anthropic message object carried by InputMessage.
type InputPayload struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// userMessage builds the stream-json envelope for a user turn.
func userMessage(text string) InputMessage {
	return InputMessage{Type: "user", Message: InputPayload{Role: "user", Content: text}}
}

// PermissionResponseMessage is the stream-json reply to a permission_request.
type PermissionResponseMessage struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"`
}

// ---- Session ----

// Params is the constructor payload for a new Session.
type Params struct {
	ID                string
	ProjectName       string
	ProjectPath       string
	Config            config.SessionConfig
	ClaudePath        string
	RetryDelay        int // seconds, fallback for non-rate-limit errors
	RateLimitPauseSec int // seconds, fallback when no resetsAt is provided
	OnEvent           EventCallback

	// Crash recovery: if non-nil, the session persists its CLI session ID to
	// disk so it can be resumed after an unexpected app restart.
	StateStore    *StateStore
	CrashRecovery bool
}

// Session is a single Claude CLI process managed by a goroutine.
// All fields are guarded by mu except where noted.
type Session struct {
	ID           string
	ProjectName  string
	ProjectPath  string
	Config       config.SessionConfig
	CLISessionID string // UUID passed via --session-id; reused on --resume

	claudePath        string
	retryDelay        int
	rateLimitPauseSec int
	onEvent           EventCallback

	mu             sync.Mutex
	status         config.SessionStatus
	currentTask    string
	todos          []TodoItem
	branch         string
	tasksDone      int
	startedAt      time.Time
	lastActivity   time.Time
	rateLimitUntil time.Time
	pendingPerm    *PermissionRequest

	// Per-run state.
	cmd          *exec.Cmd
	stdinPipe    io.WriteCloser
	cancelRun    context.CancelFunc
	rateLimited  atomic.Bool
	rateLimitInf atomic.Pointer[RateLimitInfo]
	authErrorHit atomic.Bool

	// inputCh carries already-marshalled JSON lines that the inputWriter
	// goroutine writes to stdin. A nil value is a sentinel to flush/exit.
	inputCh chan []byte

	// softStop is set when the manager calls Stop(true): the current task
	// is allowed to finish, then Run() exits without restarting.
	softStop atomic.Bool

	// Crash recovery state (protected by mu where noted).
	stateStore      *StateStore
	crashRecovery   bool
	resumeSessionID string // set before Run loop; cleared after first runOnce

	// Rate-limit fallback model tracking.
	// activeModel is the model actually passed to --model on the next launch;
	// it switches to FallbackModel on the first rate-limit hit when
	// FallbackModelOnRateLimit is enabled.
	activeModel   string
	usingFallback bool
}

// errRateLimited is the internal sentinel signalling that the run ended
// because of a rate limit (not a generic process error).
var errRateLimited = errors.New("session: rate limited")

// errPreHookFailed is returned by runOnce when the pre_task_hook exits non-zero.
// The Run loop converts this to a Retrying state (see PLAN.md section 9).
var errPreHookFailed = errors.New("session: pre_task_hook failed")

// errAuthError is the sentinel for HTTP 403 / authentication failures detected
// in stderr. The Run loop pauses for 60 seconds before retrying.
var errAuthError = errors.New("session: auth error (403)")

// New constructs a Session and assigns it a fresh CLI session UUID.
func New(p Params) *Session {
	if p.ClaudePath == "" {
		p.ClaudePath = "claude"
	}
	if p.RetryDelay <= 0 {
		p.RetryDelay = 30
	}
	if p.RateLimitPauseSec <= 0 {
		p.RateLimitPauseSec = 300
	}
	return &Session{
		ID:                p.ID,
		ProjectName:       p.ProjectName,
		ProjectPath:       p.ProjectPath,
		Config:            p.Config,
		CLISessionID:      uuid.NewString(),
		claudePath:        p.ClaudePath,
		retryDelay:        p.RetryDelay,
		rateLimitPauseSec: p.RateLimitPauseSec,
		onEvent:           p.OnEvent,
		status:            config.StatusIdle,
		stateStore:        p.StateStore,
		crashRecovery:     p.CrashRecovery,
		activeModel:       p.Config.Model,
	}
}

// Status returns the current status (thread-safe).
func (s *Session) Status() config.SessionStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// TasksDone returns the count of tasks completed in the current Run (thread-safe).
func (s *Session) TasksDone() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tasksDone
}

// PendingPermission returns a snapshot of the pending request (or nil).
func (s *Session) PendingPermission() *PermissionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pendingPerm == nil {
		return nil
	}
	cp := *s.pendingPerm
	return &cp
}

// Snapshot is a point-in-time copy of the session's mutable state, taken
// under the session mutex so the manager can build SessionState views for
// the frontend without racing with the run loop.
type Snapshot struct {
	Status         config.SessionStatus
	StartedAt      time.Time
	LastActivity   time.Time
	RateLimitUntil time.Time
	CurrentTask    string
	Todos          []TodoItem
	Branch         string
	TasksDone      int
	CLISessionID   string
	PendingPerm    *PermissionRequest
}

// Snapshot returns a thread-safe copy of the session's mutable fields.
func (s *Session) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := Snapshot{
		Status:         s.status,
		StartedAt:      s.startedAt,
		LastActivity:   s.lastActivity,
		RateLimitUntil: s.rateLimitUntil,
		CurrentTask:    s.currentTask,
		Todos:          append([]TodoItem(nil), s.todos...),
		Branch:         s.branch,
		TasksDone:      s.tasksDone,
		CLISessionID:   s.CLISessionID,
	}
	if s.pendingPerm != nil {
		cp := *s.pendingPerm
		snap.PendingPerm = &cp
	}
	return snap
}

// Stop requests the session to terminate. If soft is true, the current task
// is allowed to finish before exiting. Otherwise the process is killed.
func (s *Session) Stop(soft bool) {
	if soft {
		s.softStop.Store(true)
		return
	}
	s.mu.Lock()
	cancel := s.cancelRun
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// SendMessage writes a user_message to the session's stdin via the input
// channel. Returns an error if the session is not in a state that accepts
// input.
func (s *Session) SendMessage(msg string) error {
	s.mu.Lock()
	st := s.status
	s.mu.Unlock()
	if st != config.StatusWorking && st != config.StatusWaitingPermission {
		return fmt.Errorf("session %s is not active (status=%s)", s.ID, st)
	}
	data, err := json.Marshal(userMessage(msg))
	if err != nil {
		return err
	}
	return s.queueInput(append(data, '\n'))
}

// RespondPermission writes a permission_response for the given request to
// stdin, clears the pending request, and returns the session to Working.
func (s *Session) RespondPermission(requestID, decision string) error {
	s.mu.Lock()
	if s.pendingPerm == nil || s.pendingPerm.ID != requestID {
		s.mu.Unlock()
		return fmt.Errorf("session %s: no pending permission with id %q", s.ID, requestID)
	}
	s.pendingPerm = nil
	s.mu.Unlock()

	cliDecision := decision
	switch decision {
	case "allow", "allow_session", "allow_similar", "allow_always":
		cliDecision = "allow"
	case "deny", "deny_always":
		cliDecision = "deny"
	}

	data, err := json.Marshal(PermissionResponseMessage{
		Type:      "permission_response",
		RequestID: requestID,
		Decision:  cliDecision,
	})
	if err != nil {
		return err
	}
	if err := s.queueInput(append(data, '\n')); err != nil {
		return err
	}
	s.setStatus(config.StatusWorking)
	return nil
}

// queueInput sends a pre-marshalled JSON line to the input channel.
// Blocks briefly if the writer is slow; returns an error if the channel
// is closed (session not running).
func (s *Session) queueInput(line []byte) error {
	s.mu.Lock()
	ch := s.inputCh
	s.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("session %s: input channel not ready", s.ID)
	}
	defer func() {
		// Recover from send-on-closed-channel races.
		_ = recover()
	}()
	select {
	case ch <- line:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("session %s: input channel send timed out", s.ID)
	}
}

// Run is the session's main loop. It blocks until ctx is cancelled, the
// session is soft-stopped, or auto-restart is disabled and the current
// process exits.
func (s *Session) Run(ctx context.Context) {
	s.mu.Lock()
	s.startedAt = time.Now()
	s.mu.Unlock()

	logger.L.Info("session.run.start", "id", s.ID,
		"model", s.Config.Model,
		"effort", s.Config.Effort,
		"permission_mode", s.Config.PermissionMode,
		"auto_restart", s.Config.AutoRestart,
		"max_tasks", s.Config.MaxTasks,
		"crash_recovery", s.crashRecovery,
		"fallback_on_rate_limit", s.Config.FallbackModelOnRateLimit,
	)

	// Crash recovery: check for a state file saved by a previous interrupted run.
	if s.crashRecovery && s.stateStore != nil {
		if saved, _ := s.stateStore.Load(s.ProjectName, s.Config.Name); saved != nil {
			if saved.SessionID != "" {
				logger.L.Info("session.crash_recovery.found",
					"id", s.ID,
					"saved_session", saved.SessionID,
					"saved_at", saved.StartedAt,
				)
				s.mu.Lock()
				s.resumeSessionID = saved.SessionID
				s.mu.Unlock()
			} else {
				// State file exists but session_id was never written (crash before
				// the first init event). Discard and start fresh.
				logger.L.Info("session.crash_recovery.no_session_id", "id", s.ID)
				s.stateStore.Clear(s.ProjectName, s.Config.Name)
			}
		}
	}

	for {
		if ctx.Err() != nil {
			logger.L.Info("session.run.cancelled", "id", s.ID)
			s.setStatus(config.StatusIdle)
			return
		}
		if s.softStop.Load() {
			logger.L.Info("session.run.soft_stopped", "id", s.ID)
			s.setStatus(config.StatusIdle)
			return
		}
		if s.Config.MaxTasks > 0 && s.TasksDone() >= s.Config.MaxTasks {
			logger.L.Info("session.run.max_tasks_reached", "id", s.ID, "tasks_done", s.TasksDone())
			s.setStatus(config.StatusIdle)
			return
		}

		// Task source check: stop the loop when the configured task file no longer
		// has pending tasks (mirrors orchestrator.py has_tasks()).
		if s.Config.StopWhenNoTasks && s.Config.TaskSource != "" {
			taskPath := s.Config.TaskSource
			if !filepath.IsAbs(taskPath) {
				taskPath = filepath.Join(s.ProjectPath, taskPath)
			}
			if !hasTasks(taskPath) {
				logger.L.Info("session.run.no_tasks", "id", s.ID, "source", taskPath)
				s.setStatus(config.StatusIdle)
				return
			}
		}

		s.setStatus(config.StatusStarting)
		err := s.runOnce(ctx)

		// Clear resumeSessionID after the first runOnce attempt regardless of
		// outcome — subsequent runs in the same process start fresh.
		s.mu.Lock()
		s.resumeSessionID = ""
		s.mu.Unlock()

		if ctx.Err() != nil {
			s.setStatus(config.StatusIdle)
			return
		}

		switch {
		case errors.Is(err, errRateLimited):
			logger.L.Warn("session.run.rate_limited", "id", s.ID, "using_fallback", s.usingFallback)
			s.setStatus(config.StatusRateLimited)
			s.emitErr(err)
			// Fallback model: switch to FallbackModel immediately instead of waiting.
			if s.Config.FallbackModelOnRateLimit && s.Config.FallbackModel != "" && !s.usingFallback {
				s.usingFallback = true
				s.activeModel = s.Config.FallbackModel
				logger.L.Info("session.run.fallback_model_activated",
					"id", s.ID,
					"fallback_model", s.activeModel,
				)
				// Continue immediately — no rate-limit pause needed.
			} else if !s.waitRateLimit(ctx) {
				s.setStatus(config.StatusIdle)
				return
			}
		case errors.Is(err, errAuthError):
			logger.L.Error("session.run.auth_error", "id", s.ID)
			s.setStatus(config.StatusRetrying)
			s.emitErr(err)
			// Clear state — auth errors are not recoverable by resuming.
			if s.crashRecovery && s.stateStore != nil {
				s.stateStore.Clear(s.ProjectName, s.Config.Name)
			}
			// Rotate the CLI session UUID: --session-id with the old value would
			// resume the same on-disk conversation and hit the same 403 again.
			s.mu.Lock()
			s.CLISessionID = uuid.NewString()
			s.mu.Unlock()
			if !s.sleepCtx(ctx, 60*time.Second) {
				s.setStatus(config.StatusIdle)
				return
			}
		case err != nil:
			logger.L.Error("session.run.error", "id", s.ID, "error", err, "retry_delay_sec", s.retryDelay)
			s.setStatus(config.StatusRetrying)
			s.emitErr(err)
			if !s.sleepCtx(ctx, time.Duration(s.retryDelay)*time.Second) {
				s.setStatus(config.StatusIdle)
				return
			}
		default:
			// Successful task completion: clear persisted state.
			if s.crashRecovery && s.stateStore != nil {
				s.stateStore.Clear(s.ProjectName, s.Config.Name)
			}
			s.mu.Lock()
			s.tasksDone++
			done := s.tasksDone
			// Each task gets a fresh CLI session: reusing the previous
			// --session-id would resume the old conversation and drag the whole
			// finished task's dialogue into the next task's context.
			s.CLISessionID = uuid.NewString()
			s.mu.Unlock()
			logger.L.Info("session.run.task_done", "id", s.ID, "tasks_done", done)
			// Post-task hook is informational: failures are logged but do not
			// affect the run loop (PLAN.md section 9).
			s.runPostTaskHook(ctx)
			s.emit(SessionEvent{Type: EvtTaskDone, TasksDone: done})
		}

		if !s.Config.AutoRestart {
			logger.L.Info("session.run.exit_no_restart", "id", s.ID)
			s.setStatus(config.StatusIdle)
			return
		}
	}
}

// taskPointerPattern matches a bare pointer line `<source>:NN` — the canonical
// lumen task format (one open task per line, e.g. "ROADMAP.md:92" or
// "crates/x/src/lib.rs:76").
var taskPointerPattern = regexp.MustCompile(`^\S+:\d+$`)

// hasTasks reports whether a task source file contains pending work.
// It mirrors orchestrator.py has_tasks() and supports both formats:
//
//  1. New (canonical): bare pointer lines `<source>:NN`, one per open task.
//     Headings, quotes and list markers are ignored; any bare pointer line
//     means work exists. Completed tasks are removed from the file.
//  2. Old: the file contains "In progress:" (a task already started) or both
//     "Next:" and "- [" (queued tasks).
func hasTasks(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	content := string(data)
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, ">") ||
			strings.HasPrefix(s, "-") || strings.HasPrefix(s, "_") || strings.HasPrefix(s, "*") {
			continue
		}
		if taskPointerPattern.MatchString(s) {
			return true
		}
	}
	if strings.Contains(content, "In progress:") {
		return true
	}
	return strings.Contains(content, "Next:") && strings.Contains(content, "- [")
}

// isAuthError reports whether a stderr line indicates a 403/authentication failure.
func isAuthError(line string) bool {
	lower := strings.ToLower(line)
	return strings.Contains(line, "403") &&
		(strings.Contains(lower, "forbidden") ||
			strings.Contains(lower, "authenticate") ||
			strings.Contains(lower, "unauthorized"))
}

// runOnce launches one Claude CLI process and pumps its I/O until exit.
func (s *Session) runOnce(ctx context.Context) error {
	// Each run is a fresh CLI conversation (one task per process), so the
	// previous task's todo list no longer describes what this run is doing.
	s.updateTodos(nil)

	if err := s.runPreTaskHook(ctx); err != nil {
		return err
	}

	// Persist state before launch so a crash between here and the first init
	// event is still recoverable (session_id will be filled in by handleLine).
	if s.crashRecovery && s.stateStore != nil {
		st := &PersistedState{StartedAt: time.Now()}
		if err := s.stateStore.Save(s.ProjectName, s.Config.Name, st); err != nil {
			logger.L.Warn("session.state_save_failed", "id", s.ID, "error", err)
		}
	}

	args := s.buildCLIArgs()

	logger.L.Debug("session.launch",
		"id", s.ID,
		"claude", s.claudePath,
		"cwd", s.ProjectPath,
		"args", strings.Join(args, " "),
	)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := exec.CommandContext(runCtx, s.claudePath, args...)
	proc.HideConsole(cmd)
	if s.ProjectPath != "" {
		cmd.Dir = s.ProjectPath
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("stdin pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		logger.L.Error("session.spawn_failed", "id", s.ID, "error", err)
		return fmt.Errorf("start claude: %w", err)
	}
	logger.L.Info("session.spawned", "id", s.ID, "pid", cmd.Process.Pid)

	// Group the CLI with everything it spawns (bash, cargo, subagents) so the
	// whole tree is reaped when this run ends, however it ends.
	job, jobErr := proc.NewJob()
	if jobErr != nil {
		logger.L.Warn("session.job_create_failed", "id", s.ID, "error", jobErr)
	}
	defer job.Close()
	if err := job.Assign(cmd.Process.Pid); err != nil {
		logger.L.Warn("session.job_assign_failed", "id", s.ID, "pid", cmd.Process.Pid, "error", err)
	}

	inputCh := make(chan []byte, 16)
	s.mu.Lock()
	s.cmd = cmd
	s.stdinPipe = stdin
	s.cancelRun = cancel
	s.inputCh = inputCh
	s.mu.Unlock()

	s.rateLimited.Store(false)
	s.rateLimitInf.Store(nil)
	s.authErrorHit.Store(false)

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		inputWriter(stdin, inputCh)
	}()

	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		s.drainStderr(stderr)
	}()

	if p := s.initialPromptText(); p != "" {
		preview := p
		if len(preview) > 200 {
			preview = preview[:200] + "…"
		}
		logger.L.Debug("session.prompt_send", "id", s.ID, "prompt_preview", preview)
		_ = s.sendInitialPrompt(inputCh, p)
	}

	s.setStatus(config.StatusWorking)

	// closeInput signals stdin EOF exactly once (idempotent). It is called both
	// on turn completion (autonomous mode) and after the stdout stream ends.
	var closeInputOnce sync.Once
	closeInput := func() { closeInputOnce.Do(func() { close(inputCh) }) }

	// In autonomous mode the manager owns the session lifecycle: one CLI process
	// handles one task/turn, then restarts with a fresh context (see PLAN.md
	// token optimization). Real Claude CLI keeps its process alive waiting on
	// stdin after each result, so we must close stdin when the turn's `result`
	// event arrives — otherwise the process idles forever and the Run loop never
	// iterates to re-check the task source. Interactive sessions (no auto-restart
	// and no task loop) keep stdin open so the user can continue the conversation.
	autonomous := s.Config.AutoRestart || s.Config.StopWhenNoTasks

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for scanner.Scan() {
		if s.handleLine(scanner.Text()) && autonomous {
			closeInput()
		}
	}
	scanErr := scanner.Err()

	closeInput()
	<-writerDone
	<-stderrDone

	waitErr := cmd.Wait()

	s.mu.Lock()
	s.cmd = nil
	s.stdinPipe = nil
	s.cancelRun = nil
	s.inputCh = nil
	s.mu.Unlock()

	if s.authErrorHit.Load() {
		logger.L.Error("session.process_exit.auth_error", "id", s.ID)
		return errAuthError
	}
	if s.rateLimited.Load() {
		return errRateLimited
	}
	if waitErr != nil {
		logger.L.Error("session.process_exit", "id", s.ID, "error", waitErr)
		return fmt.Errorf("claude exited: %w", waitErr)
	}
	if scanErr != nil {
		logger.L.Error("session.stdout_scan", "id", s.ID, "error", scanErr)
		return fmt.Errorf("stdout scan: %w", scanErr)
	}
	logger.L.Info("session.process_exit", "id", s.ID, "ok", true)
	return nil
}

// initialPromptText returns the prompt to send at session start.
// When recovering from a crash, returns the CrashRecoveryPrompt (or a default).
func (s *Session) initialPromptText() string {
	s.mu.Lock()
	recovering := s.resumeSessionID != ""
	s.mu.Unlock()

	if recovering {
		if p := strings.TrimSpace(s.Config.CrashRecoveryPrompt); p != "" {
			return p
		}
		return "The session was interrupted unexpectedly. Please check git status, review your task file, and continue from where you left off."
	}
	return strings.TrimSpace(s.Config.Prompt)
}

// sendInitialPrompt pushes prompt onto the input channel before any user messages.
func (s *Session) sendInitialPrompt(ch chan<- []byte, prompt string) error {
	data, err := json.Marshal(userMessage(prompt))
	if err != nil {
		return err
	}
	select {
	case ch <- append(data, '\n'):
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("session %s: initial prompt send timed out", s.ID)
	}
}

// handleLine parses one stream-json line and dispatches the event. It returns
// true when the line was a result event, i.e. the current turn/task finished.
func (s *Session) handleLine(line string) bool {
	ev := ParseLine(line)

	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()

	switch ev.EventType {
	case EventInit:
		if ev.Init != nil {
			if ev.Init.SessionID != "" {
				s.mu.Lock()
				s.CLISessionID = ev.Init.SessionID
				s.mu.Unlock()
				// Update persisted state with the confirmed session ID so the
				// next restart can resume this exact conversation.
				if s.crashRecovery && s.stateStore != nil {
					s.stateStore.UpdateSessionID(s.ProjectName, s.Config.Name, ev.Init.SessionID)
				}
			}
			s.emit(SessionEvent{Type: EvtInit, Init: ev.Init})
		}
		for _, e := range ev.Entries {
			entry := e
			s.emit(SessionEvent{Type: EvtLog, Entry: &entry})
		}

	case EventLog:
		for _, e := range ev.Entries {
			entry := e
			s.emit(SessionEvent{Type: EvtLog, Entry: &entry})
		}
		if ev.Todos != nil {
			s.updateTodos(ev.Todos)
		}
		if ev.Usage != nil {
			usage := *ev.Usage
			s.emit(SessionEvent{Type: EvtUsage, Usage: &usage})
		}

	case EventResult:
		for _, e := range ev.Entries {
			entry := e
			s.emit(SessionEvent{Type: EvtLog, Entry: &entry})
		}
		if ev.Result != nil {
			// Auth failures surface as result text on stdout (e.g. "Failed to
			// authenticate. API Error: 403 Request not allowed"), not on stderr.
			if isAuthError(ev.Result.ResultText) {
				s.authErrorHit.Store(true)
				logger.L.Error("session.auth_error_detected", "id", s.ID, "line", ev.Result.ResultText)
			}
			s.emit(SessionEvent{Type: EvtResult, Result: ev.Result})
		}
		return true

	case EventRateLimit:
		if ev.RateLimit != nil {
			s.onRateLimit(ev.RateLimit)
		}

	case EventPermission:
		if ev.Permission != nil {
			req := *ev.Permission
			s.mu.Lock()
			s.pendingPerm = &req
			s.mu.Unlock()
			s.setStatus(config.StatusWaitingPermission)
			s.emit(SessionEvent{Type: EvtPermission, Permission: &req})
		}

	default:
		for _, e := range ev.Entries {
			entry := e
			s.emit(SessionEvent{Type: EvtLog, Entry: &entry})
		}
	}
	return false
}

// updateTodos stores Claude's latest TodoWrite list, derives the current task
// from it, and emits an EvtTodo event. Passing an empty (non-nil handled by the
// caller) or nil slice clears the list — used at the start of each run.
func (s *Session) updateTodos(todos []TodoItem) {
	s.mu.Lock()
	s.todos = append([]TodoItem(nil), todos...)
	s.currentTask = currentTaskFromTodos(todos)
	cp := append([]TodoItem(nil), todos...)
	s.mu.Unlock()
	s.emit(SessionEvent{Type: EvtTodo, Todos: cp})
}

// currentTaskFromTodos picks the human-readable "what is happening now" line:
// the in_progress item (its activeForm reads naturally), else the first pending
// item, else the last completed one.
func currentTaskFromTodos(todos []TodoItem) string {
	for _, t := range todos {
		if t.Status == "in_progress" {
			if t.ActiveForm != "" {
				return t.ActiveForm
			}
			return t.Content
		}
	}
	for _, t := range todos {
		if t.Status == "pending" {
			return t.Content
		}
	}
	if n := len(todos); n > 0 {
		return todos[n-1].Content
	}
	return ""
}

// drainStderr reads stderr line by line, forwarding each line as a log
// entry and also scanning it for rate-limit and auth-error signals.
func (s *Session) drainStderr(r io.Reader) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 32*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		logger.L.Warn("session.stderr", "id", s.ID, "line", line)
		if info, ok := detectRateLimitText(line); ok {
			s.onRateLimit(info)
		}
		if isAuthError(line) {
			s.authErrorHit.Store(true)
			logger.L.Error("session.auth_error_detected", "id", s.ID, "line", line)
		}
		entry := config.LogEntry{
			Time:    time.Now(),
			Level:   "error",
			Source:  "claude",
			Message: line,
		}
		s.emit(SessionEvent{Type: EvtLog, Entry: &entry})
	}
}

// setStatus updates the status under the lock and emits a status event
// when the value changes.
func (s *Session) setStatus(st config.SessionStatus) {
	s.mu.Lock()
	if s.status == st {
		s.mu.Unlock()
		return
	}
	old := s.status
	s.status = st
	s.mu.Unlock()
	logger.L.Info("session.status", "id", s.ID, "from", old.String(), "to", st.String())
	s.emit(SessionEvent{Type: EvtStatus, Status: st})
}

func (s *Session) emit(ev SessionEvent) {
	if s.onEvent == nil {
		return
	}
	s.onEvent(s.ID, ev)
}

func (s *Session) emitErr(err error) {
	if err == nil {
		return
	}
	entry := config.LogEntry{
		Time:    time.Now(),
		Level:   "error",
		Source:  "manager",
		Message: err.Error(),
	}
	s.emit(SessionEvent{Type: EvtError, Err: err, Entry: &entry})
}

// sleepCtx waits for d or ctx cancellation. Returns false if ctx was cancelled.
func (s *Session) sleepCtx(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// buildCLIArgs constructs the argv for the Claude CLI based on SessionConfig
// and global settings (see PLAN.md section 14).
func (s *Session) buildCLIArgs() []string {
	s.mu.Lock()
	resumeID := s.resumeSessionID
	activeModel := s.activeModel
	s.mu.Unlock()

	args := []string{
		"-p",
		"--verbose",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--replay-user-messages",
	}
	if resumeID != "" {
		// Crash recovery: resume the previous conversation by its session ID.
		args = append(args, "--resume", resumeID)
	} else {
		args = append(args, "--session-id", s.CLISessionID)
	}
	if s.Config.Name != "" {
		args = append(args, "--name", s.Config.Name)
	}
	// activeModel may have been switched to FallbackModel after a rate limit.
	model := activeModel
	if model == "" {
		model = s.Config.Model
	}
	if model != "" {
		args = append(args, "--model", model)
	}
	// Only pass --fallback-model when not already using the fallback (avoid
	// circular fallback: fallback-of-fallback would be the same model).
	if s.Config.FallbackModel != "" && !s.usingFallback {
		args = append(args, "--fallback-model", s.Config.FallbackModel)
	}
	if s.Config.Effort != "" {
		args = append(args, "--effort", s.Config.Effort)
	}
	if s.Config.PermissionMode != "" {
		args = append(args, "--permission-mode", s.Config.PermissionMode)
	}
	if s.Config.MaxBudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(s.Config.MaxBudgetUSD, 'f', -1, 64))
	}
	if s.Config.UseWorktree {
		// Pass an explicit name only when configured. A bare `--worktree` lets the
		// CLI create a FRESH worktree from current HEAD on every run — which the
		// autonomous restart loop relies on (a fixed name reuses a possibly-stale
		// worktree, so the task file inside it can lag behind the main repo).
		// `--worktree` takes an optional [name] argument (claude CLI v2.1+).
		if name := strings.TrimSpace(s.Config.WorktreeName); name != "" {
			args = append(args, "--worktree", name)
		} else {
			args = append(args, "--worktree")
		}
	}
	if len(s.Config.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(s.Config.AllowedTools, " "))
	}
	if len(s.Config.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(s.Config.DisallowedTools, " "))
	}
	if s.Config.SystemPromptAppend != "" {
		args = append(args, "--append-system-prompt", s.Config.SystemPromptAppend)
	}
	for _, d := range s.Config.AddDirs {
		if d != "" {
			args = append(args, "--add-dir", d)
		}
	}
	return args
}

// runPreTaskHook executes the configured pre_task_hook and returns
// errPreHookFailed if it exits non-zero. The hook's stdout/stderr is logged
// to the session log buffer for visibility (PLAN.md section 9).
func (s *Session) runPreTaskHook(ctx context.Context) error {
	cmd := strings.TrimSpace(s.Config.PreTaskHook)
	if cmd == "" {
		return nil
	}
	s.emitHookLog("manager", "pre_task_hook: "+cmd, "text")

	res := hooks.RunHookContext(ctx, cmd, s.ProjectPath, 0)
	s.logHookOutput("pre_task_hook", &res)

	if !res.Success() {
		return fmt.Errorf("%w (exit=%d): %v", errPreHookFailed, res.ExitCode, res.Err)
	}
	return nil
}

// runPostTaskHook executes the configured post_task_hook after a successful
// task. Failures are logged but do not propagate (PLAN.md section 9).
func (s *Session) runPostTaskHook(ctx context.Context) {
	cmd := strings.TrimSpace(s.Config.PostTaskHook)
	if cmd == "" {
		return
	}
	s.emitHookLog("manager", "post_task_hook: "+cmd, "text")

	res := hooks.RunHookContext(ctx, cmd, s.ProjectPath, 0)
	s.logHookOutput("post_task_hook", &res)
}

// logHookOutput forwards captured stdout/stderr to the session log stream so
// the operator can see exactly what the hook printed.
func (s *Session) logHookOutput(name string, res *hooks.Result) {
	if out := strings.TrimRight(res.Stdout, "\r\n"); out != "" {
		for _, line := range strings.Split(out, "\n") {
			s.emitHookLog("hook:"+name, strings.TrimRight(line, "\r"), "text")
		}
	}
	if errOut := strings.TrimRight(res.Stderr, "\r\n"); errOut != "" {
		for _, line := range strings.Split(errOut, "\n") {
			s.emitHookLog("hook:"+name, strings.TrimRight(line, "\r"), "error")
		}
	}
	if res.Success() {
		s.emitHookLog("manager", fmt.Sprintf("%s ok (%dms)", name, res.Duration.Milliseconds()), "text")
	} else {
		s.emitHookLog("manager",
			fmt.Sprintf("%s failed: exit=%d duration=%dms", name, res.ExitCode, res.Duration.Milliseconds()),
			"error")
	}
}

func (s *Session) emitHookLog(source, msg, level string) {
	entry := config.LogEntry{
		Time:    time.Now(),
		Level:   level,
		Source:  source,
		Message: msg,
	}
	s.emit(SessionEvent{Type: EvtLog, Entry: &entry})
}
