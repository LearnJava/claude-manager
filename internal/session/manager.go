package session

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/config"
	"claude-manager/internal/gitutil"
	"claude-manager/internal/logger"
	"claude-manager/internal/permission"
	"claude-manager/internal/store"
	"claude-manager/internal/worker"
)

// Emitter broadcasts named session events to one or more consumers.
// The interface is defined here (consumed) and implemented in internal/control
// (WailsEmitter, MultiEmitter, ControlEmitter) without an import cycle.
type Emitter interface {
	Emit(event string, data any)
}

// ActionIndexFunc indexes one finished run's newly-appended CLI transcript
// lines into action_signatures (LEARN-TASKS.md LN-03). Declared as a function
// type here, consumed by finishRun, and wired from app.go to
// internal/experience.IngestRun — a direct import would cycle, since
// internal/experience already imports internal/session for Step/TokenUsage.
type ActionIndexFunc func(project, sessionName string, runID int64, cliSessionID, projectPath, taskPtr string) error

// JournalEntry mirrors experience.Entry (LEARN-TASKS.md LN-06) without
// importing internal/experience — same import-cycle reason as
// ActionIndexFunc above.
type JournalEntry struct {
	Date      time.Time
	TaskPtr   string
	Done      string
	Surprises []string
	Avoid     []string
}

// JournalWriteFunc persists one distilled journal entry for a project
// (LEARN-TASKS.md LN-06). Declared as a function type here, consumed by
// finishRun, and wired from app.go to internal/experience.AppendEntry — a
// direct import would cycle, same as ActionIndexFunc.
type JournalWriteFunc func(projectPath string, commit bool, entry JournalEntry) error

// journalAnalyzeFn abstracts analysis.GenerateJournalEntry so tests can stub
// the analyst CLI (LEARN-TASKS.md LN-06). Unlike indexRun/primerFn, this one
// defaults to the real implementation in NewSessionManager rather than
// starting nil — the distillation call is gated by the project's own Journal
// flag and by journalFn being wired (see finishRun), not by whether this is
// set.
type journalAnalyzeFn func(ctx context.Context, projectPath string, in analysis.JournalInput, cfg analysis.AnalysisConfig) (*analysis.JournalResult, error)

// Event names emitted to the Wails frontend (see PLAN.md section 8).
const (
	EventNameStatus     = "session:status"
	EventNameLog        = "session:log"
	EventNameTaskDone   = "session:task_done"
	EventNameRateLimit  = "session:rate_limit"
	EventNamePermission = "session:permission"
	EventNameQuestion   = "session:question"
	EventNameContext    = "session:context"
	EventNameTodo       = "session:todo"
	EventNameTaskSource = "session:task_source"
	EventNameInit       = "session:init"
	EventNameResult     = "session:result"
	EventNameError      = "session:error"
	EventNameStopReq    = "session:stop_requested"
)

// ---- Event payloads ----

type StatusEvent struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// StopRequestedEvent lets the UI show a persistent "will stop after the
// current task" indicator the instant the soft-stop is registered, instead
// of only finding out once the session actually reaches Idle (which can be
// a long wait — see CLAUDE.md's session lifecycle notes).
type StopRequestedEvent struct {
	ID            string `json:"id"`
	StopRequested bool   `json:"stop_requested"`
}

type LogEvent struct {
	ID    string          `json:"id"`
	Entry config.LogEntry `json:"entry"`
}

type TaskDoneEvent struct {
	ID        string `json:"id"`
	TasksDone int    `json:"tasks_done"`
}

type RateLimitEvent struct {
	ID    string         `json:"id"`
	Info  *RateLimitInfo `json:"info"`
	Until time.Time      `json:"until"`
}

type PermissionEvent struct {
	ID      string                       `json:"id"`
	Request permission.PermissionRequest `json:"request"`
}

// QuestionEvent carries a blocking ask-user question (see PendingQuestion) to
// the frontend, mirroring PermissionEvent's shape.
type QuestionEvent struct {
	ID       string          `json:"id"`
	Question PendingQuestion `json:"question"`
}

type ContextEvent struct {
	ID            string  `json:"id"`
	InputTokens   int     `json:"input_tokens"`
	OutputTokens  int     `json:"output_tokens"`
	CacheRead     int     `json:"cache_read"`
	CacheCreation int     `json:"cache_creation"`
	ContextWindow int     `json:"context_window"`
	Utilization   float64 `json:"utilization"`
}

// TodoEvent carries Claude's own todo list (from TodoWrite) so the UI can show
// what the session is working on and how far along it is.
type TodoEvent struct {
	ID          string     `json:"id"`
	Todos       []TodoItem `json:"todos"`
	CurrentTask string     `json:"current_task"`
}

// TaskSourceEvent carries the description resolved from the session's
// task_source pointer file (a ROADMAP.md row, task-file heading, etc. — see
// resolveTaskSourceDescription), independent of Claude's own TodoWrite
// output, so the UI has something to show before the first TodoWrite call.
type TaskSourceEvent struct {
	ID                    string `json:"id"`
	TaskSourceDescription string `json:"task_source_description"`
}

type InitEvent struct {
	ID   string    `json:"id"`
	Info *InitInfo `json:"info"`
}

type ResultEvent struct {
	ID     string         `json:"id"`
	Result *SessionResult `json:"result"`
}

type ErrorEvent struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

// ---- Frontend-facing snapshots ----

// SessionState is a snapshot of a session's full state returned to the UI.
type SessionState struct {
	ID             string     `json:"id"`
	Project        string     `json:"project"`
	Name           string     `json:"name"`
	Status         string     `json:"status"`
	Model          string     `json:"model"`
	Effort         string     `json:"effort"`
	PermissionMode string     `json:"permission_mode"`
	StartedAt      time.Time  `json:"started_at"`
	LastActivity   time.Time  `json:"last_activity"`
	RateLimitUntil time.Time  `json:"rate_limit_until"`
	TasksDone      int        `json:"tasks_done"`
	CurrentTask    string     `json:"current_task"`
	TaskSourceDesc string     `json:"task_source_description"`
	Prompt         string     `json:"prompt"`
	Todos          []TodoItem `json:"todos"`
	Branch         string     `json:"branch"`
	CLISessionID   string     `json:"cli_session_id"`
	StopRequested  bool       `json:"stop_requested"`

	PendingPermission *permission.PermissionRequest `json:"pending_permission,omitempty"`
	PendingQuestion   *PendingQuestion              `json:"pending_question,omitempty"`

	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	CacheRead     int64   `json:"cache_read"`
	CacheCreation int64   `json:"cache_creation"`
	NumTurns      int     `json:"num_turns"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
	ContextWindow int     `json:"context_window"`
	ContextUtil   float64 `json:"context_util"`
}

// SessionMetrics is a compact token/cost snapshot for one session.
type SessionMetrics struct {
	SessionID     string  `json:"session_id"`
	InputTokens   int64   `json:"input_tokens"`
	OutputTokens  int64   `json:"output_tokens"`
	CacheRead     int64   `json:"cache_read"`
	CacheCreation int64   `json:"cache_creation"`
	NumTurns      int     `json:"num_turns"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
	ContextWindow int     `json:"context_window"`
	ContextUtil   float64 `json:"context_util"`
	DurationMs    int64   `json:"duration_ms"`
}

// ---- Internal per-session tracking ----

// managedSession bundles the running Session with manager-local state
// (current run id, accumulated metrics, log buffer, cancel func).
type managedSession struct {
	session *Session
	project string
	name    string

	mu     sync.Mutex
	cancel context.CancelFunc

	// Current run state. runID == 0 means no run is currently in flight.
	runID         int64
	runStartedAt  time.Time
	runModel      string
	runCLISession string

	// Accumulated metrics for the current run.
	inputTokens       int64
	outputTokens      int64
	cacheReadTokens   int64
	cacheCreateTokens int64
	numTurns          int
	totalCostUSD      float64
	contextWindow     int
	contextUtil       float64
	durationMs        int64

	rateLimit      *RateLimitInfo
	rateLimitUntil time.Time

	// lastResultText is the most recent `result` event's text — the input
	// finishRun feeds to journal distillation (LEARN-TASKS.md LN-06).
	lastResultText string

	// pendingLogs is flushed to the store when the run ends.
	pendingLogs []store.LogEntry
}

// ---- SessionManager ----

// SessionManager owns all live sessions, the SQLite store, and the link
// between Session events and the Wails event bus.
type SessionManager struct {
	mu sync.Mutex

	cfg     *config.AppConfig
	cfgPath string
	store   *store.Store
	emitter Emitter

	// indexRun is nil unless app.go has wired the experience-layer indexer
	// (LEARN-TASKS.md LN-03); even then, finishRun only calls it when
	// cfg.Optimization.ExperienceTracking is on — see SetActionIndexer.
	indexRun ActionIndexFunc

	// primerFn is nil unless app.go has wired the context primer
	// (LEARN-TASKS.md LN-05); passed through to every new Session's PrimerFn
	// field, which only calls it when that session's own ContextPrimer flag
	// is on — see SetPrimerBuilder.
	primerFn PrimerFunc

	// journalFn is nil unless app.go has wired the project journal
	// (LEARN-TASKS.md LN-06); finishRun only calls it for a completed run
	// whose project has Journal=true — see SetJournalWriter.
	journalFn JournalWriteFunc
	// journalAnalyze abstracts the analyst CLI call so tests can stub it;
	// defaults to analysis.GenerateJournalEntry (see NewSessionManager).
	journalAnalyze journalAnalyzeFn

	// handoffFn is nil unless app.go has wired the context-handoff distiller
	// (LEARN-TASKS.md LN-15); passed through to every new Session's
	// HandoffFn field, which only calls it when that session's own
	// ContextHandoff flag is on — see SetHandoffBuilder.
	handoffFn HandoffFunc

	runtimeRules *permission.RuntimeRuleSet
	queue        *permission.PendingQueue

	sessions   map[string]*managedSession
	stateStore *StateStore // crash-recovery state files

	// lastStartTime tracks the most recent reserved StartSession slot, so we
	// can stagger session starts by session_start_delay seconds (PLAN 20.3.3).
	lastStartTime time.Time

	// Mixed programming (MIXED-TASKS.md MP-05). Guarded by mixedMu, not mu,
	// so a long-running DispatchMixedTask call never blocks session control.
	mixedMu     sync.Mutex
	workerStore *worker.Store                 // worker dialogue persistence (MP-02)
	taskStore   *worker.TaskStore             // round-state persistence (MP-05)
	worktreeDir string                        // base dir mixed-task worktrees are created under
	mixedTasks  map[string]context.CancelFunc // task ID -> cancel, while running
	mixedBriefs map[string]worker.Brief       // in-memory brief registry (MP-06 will replace this)
}

// NewSessionManager constructs a manager. emitter receives all session:*
// events; pass nil for a no-op emitter (useful in tests).
func NewSessionManager(cfg *config.AppConfig, cfgPath string, st *store.Store, emitter Emitter) *SessionManager {
	stateDir := filepath.Join(filepath.Dir(cfgPath), "state")
	return &SessionManager{
		cfg:            cfg,
		cfgPath:        cfgPath,
		store:          st,
		emitter:        emitter,
		sessions:       make(map[string]*managedSession),
		stateStore:     NewStateStore(stateDir),
		journalAnalyze: analysis.GenerateJournalEntry,
		runtimeRules:   permission.NewRuntimeRuleSet(),
		queue:        permission.NewPendingQueue(),
		workerStore:  worker.NewStore(stateDir),
		taskStore:    worker.NewTaskStore(stateDir),
		worktreeDir:  filepath.Join(filepath.Dir(cfgPath), "worktrees"),
		mixedTasks:   make(map[string]context.CancelFunc),
		mixedBriefs:  make(map[string]worker.Brief),
	}
}

// SetContext propagates a Wails runtime context to the emitter when it
// supports it (i.e. *control.WailsEmitter or *control.MultiEmitter).
// This keeps backwards-compatibility with the app.go startup sequence.
func (m *SessionManager) SetContext(ctx context.Context) {
	type ctxSetter interface{ SetContext(context.Context) }
	m.mu.Lock()
	e := m.emitter
	m.mu.Unlock()
	if cs, ok := e.(ctxSetter); ok {
		cs.SetContext(ctx)
	}
}

// SetConfig swaps the in-memory config (e.g. after a UI save).
func (m *SessionManager) SetConfig(cfg *config.AppConfig) {
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
}

// SetActionIndexer wires the experience-layer indexer (LEARN-TASKS.md LN-03).
// Pass nil to disable it entirely (the zero value — no app.go wiring means no
// transcript is ever opened). Even wired, finishRun still gates every call on
// the live experience_tracking config flag, so flipping the setting off stops
// indexing immediately without needing to re-wire anything.
func (m *SessionManager) SetActionIndexer(fn ActionIndexFunc) {
	m.mu.Lock()
	m.indexRun = fn
	m.mu.Unlock()
}

// SetPrimerBuilder wires the experience-layer context primer (LEARN-TASKS.md
// LN-05). Pass nil to disable it entirely. Each session's own ContextPrimer
// flag still gates whether it's ever called for that session (see
// Session.initialPromptText), so this only needs to be set once at startup.
func (m *SessionManager) SetPrimerBuilder(fn PrimerFunc) {
	m.mu.Lock()
	m.primerFn = fn
	m.mu.Unlock()
}

// SetJournalWriter wires the experience-layer project journal (LEARN-TASKS.md
// LN-06). Pass nil to disable it entirely (the zero value — no app.go wiring
// means finishRun never calls the distillation analyst at all). Each
// project's own Journal flag still gates whether it's ever invoked for that
// project (see finishRun), so this only needs to be set once at startup.
func (m *SessionManager) SetJournalWriter(fn JournalWriteFunc) {
	m.mu.Lock()
	m.journalFn = fn
	m.mu.Unlock()
}

// SetHandoffBuilder wires the experience-layer context-handoff distiller
// (LEARN-TASKS.md LN-15). Pass nil to disable it entirely (the zero value —
// no app.go wiring means a context restart still fires and closes the
// process, just without a distilled recap). Each Session gets this same
// function; it only actually runs it when that session's own
// Config.ContextHandoff flag is on — see checkContextRestart.
func (m *SessionManager) SetHandoffBuilder(fn HandoffFunc) {
	m.mu.Lock()
	m.handoffFn = fn
	m.mu.Unlock()
}

// Shutdown stops every session and waits briefly. Safe to call multiple times.
func (m *SessionManager) Shutdown() {
	m.StopAll()
}

// ---- Helpers ----

func sessionID(project, name string) string {
	return project + "/" + name
}

// firstNonEmpty returns a if non-empty, else b.
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func (m *SessionManager) findConfig(project, name string) (*config.ProjectConfig, *config.SessionConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cfg == nil {
		return nil, nil, fmt.Errorf("session manager: no config loaded")
	}
	for i := range m.cfg.Projects {
		p := &m.cfg.Projects[i]
		if p.Name != project {
			continue
		}
		for j := range p.Sessions {
			s := &p.Sessions[j]
			if s.Name == name {
				return p, s, nil
			}
		}
		return p, nil, fmt.Errorf("session %q not found in project %q", name, project)
	}
	return nil, nil, fmt.Errorf("project %q not found", project)
}

func (m *SessionManager) get(id string) *managedSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *SessionManager) emit(name string, payload any) {
	m.mu.Lock()
	e := m.emitter
	m.mu.Unlock()
	if e == nil {
		return
	}
	e.Emit(name, payload)
}

// reserveStartSlot returns the duration the caller should wait before starting
// a session, computed from session_start_delay and the last reserved slot.
// The slot is reserved immediately so concurrent StartSession calls cascade.
func (m *SessionManager) reserveStartSlot() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	delay := time.Duration(m.cfg.Settings.SessionStartDelay) * time.Second
	if delay <= 0 || m.lastStartTime.IsZero() {
		m.lastStartTime = time.Now()
		return 0
	}
	next := m.lastStartTime.Add(delay)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	m.lastStartTime = next
	return next.Sub(now)
}

// ---- StartSession / StopSession / lifecycle ----

// StartSession launches a session by project/session name. Honours the
// cache-warming delay: the returned error is config-level only; the actual
// process launch happens in a goroutine after the stagger delay.
func (m *SessionManager) StartSession(project, name string) error {
	proj, sc, err := m.findConfig(project, name)
	if err != nil {
		logger.L.Error("manager.start_session.config_error", "project", project, "session", name, "error", err)
		return err
	}
	id := sessionID(project, name)
	logger.L.Info("manager.start_session",
		"id", id,
		"path", proj.Path,
		"model", sc.Model,
		"effort", sc.Effort,
		"permission_mode", sc.PermissionMode,
		"auto_restart", sc.AutoRestart,
		"max_tasks", sc.MaxTasks,
		"use_worktree", sc.UseWorktree,
		"prompt_len", len(strings.TrimSpace(sc.Prompt)),
	)

	m.mu.Lock()
	if existing, ok := m.sessions[id]; ok {
		st := existing.session.Status()
		if st != config.StatusIdle && st != config.StatusError {
			m.mu.Unlock()
			return fmt.Errorf("session %q already running (status=%s)", id, st)
		}
		// Drop the old record so we can re-create.
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	ms := &managedSession{
		project: project,
		name:    name,
	}
	m.mu.Lock()
	primerFn := m.primerFn
	handoffFn := m.handoffFn
	m.mu.Unlock()
	sess := New(Params{
		ID:                id,
		ProjectName:       project,
		ProjectPath:       proj.Path,
		Config:            *sc,
		ClaudePath:        m.cfg.Settings.ClaudePath,
		RetryDelay:        m.cfg.Settings.DefaultRetryDelay,
		RateLimitPauseSec: m.cfg.Settings.RateLimitPause,
		StateStore:        m.stateStore,
		CrashRecovery:     m.cfg.Settings.CrashRecovery,
		Gates:             proj.Gates,
		PrimerFn:          primerFn,
		Optimization:      &m.cfg.Optimization,
		HandoffFn:         handoffFn,
		OnEvent: func(sid string, ev SessionEvent) {
			m.onSessionEvent(sid, ev)
		},
	})
	ms.session = sess

	m.mu.Lock()
	m.sessions[id] = ms
	m.mu.Unlock()

	wait := m.reserveStartSlot()

	go func() {
		defer logger.Recover("manager.start_session", "id", id)
		if wait > 0 {
			time.Sleep(wait)
		}
		ctx, cancel := context.WithCancel(context.Background())
		ms.mu.Lock()
		ms.cancel = cancel
		ms.mu.Unlock()
		sess.Run(ctx)
	}()

	return nil
}

// StartSessionWithOverride launches a session like StartSession but applies
// model and effort overrides on top of the configured values.
// Empty strings mean "keep the configured value".
func (m *SessionManager) StartSessionWithOverride(project, name, model, effort string) error {
	proj, sc, err := m.findConfig(project, name)
	if err != nil {
		return err
	}
	id := sessionID(project, name)

	m.mu.Lock()
	if existing, ok := m.sessions[id]; ok {
		st := existing.session.Status()
		if st != config.StatusIdle && st != config.StatusError {
			m.mu.Unlock()
			return fmt.Errorf("session %q already running (status=%s)", id, st)
		}
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	sessionCfg := *sc
	if model != "" {
		sessionCfg.Model = model
	}
	if effort != "" {
		sessionCfg.Effort = effort
	}

	ms := &managedSession{project: project, name: name}
	m.mu.Lock()
	primerFn := m.primerFn
	handoffFn := m.handoffFn
	m.mu.Unlock()
	sess := New(Params{
		ID:                id,
		ProjectName:       project,
		ProjectPath:       proj.Path,
		Config:            sessionCfg,
		ClaudePath:        m.cfg.Settings.ClaudePath,
		RetryDelay:        m.cfg.Settings.DefaultRetryDelay,
		RateLimitPauseSec: m.cfg.Settings.RateLimitPause,
		StateStore:        m.stateStore,
		CrashRecovery:     m.cfg.Settings.CrashRecovery,
		Gates:             proj.Gates,
		PrimerFn:          primerFn,
		Optimization:      &m.cfg.Optimization,
		HandoffFn:         handoffFn,
		OnEvent:           func(sid string, ev SessionEvent) { m.onSessionEvent(sid, ev) },
	})
	ms.session = sess

	m.mu.Lock()
	m.sessions[id] = ms
	m.mu.Unlock()

	wait := m.reserveStartSlot()
	go func() {
		defer logger.Recover("manager.start_session", "id", id)
		if wait > 0 {
			time.Sleep(wait)
		}
		ctx, cancel := context.WithCancel(context.Background())
		ms.mu.Lock()
		ms.cancel = cancel
		ms.mu.Unlock()
		sess.Run(ctx)
	}()
	return nil
}

// StopSession requests termination. If soft is true the current task is
// allowed to finish before exiting; otherwise the process is killed.
func (m *SessionManager) StopSession(id string, soft bool) error {
	ms := m.get(id)
	if ms == nil {
		return fmt.Errorf("session %q not found", id)
	}
	logger.L.Info("manager.stop_session", "id", id, "soft", soft)
	ms.session.Stop(soft)
	if soft {
		m.emit(EventNameStopReq, StopRequestedEvent{ID: id, StopRequested: true})
	}
	m.queue.RemoveBySession(id)
	if !soft {
		ms.mu.Lock()
		cancel := ms.cancel
		ms.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return nil
}

// StopAll stops every running session (soft=false).
func (m *SessionManager) StopAll() {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.StopSession(id, false)
	}
}

// RestartSession stops the session (hard) and starts a fresh one.
func (m *SessionManager) RestartSession(id string) error {
	ms := m.get(id)
	if ms == nil {
		return fmt.Errorf("session %q not found", id)
	}
	project, name := ms.project, ms.name
	if err := m.StopSession(id, false); err != nil {
		return err
	}
	// Give the Run goroutine a moment to exit before restarting.
	for i := 0; i < 50; i++ {
		if ms.session.Status() == config.StatusIdle {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return m.StartSession(project, name)
}

// ResumeSession re-launches a stopped session, reusing the prior CLI session
// id implicitly (Session.Run preserves CLISessionID across restarts within
// its lifetime, but once we recreate the Session this is a fresh CLI session).
// A full resume across process restarts is deferred to a later task.
func (m *SessionManager) ResumeSession(id string) error {
	ms := m.get(id)
	if ms == nil {
		return fmt.Errorf("session %q not found", id)
	}
	return m.StartSession(ms.project, ms.name)
}

// SetSessionModel changes a session's model on the fly.
//
// Autonomous sessions (task_source/auto_restart) already restart between
// tasks — one CLI process equals one task there — so this just updates the
// live override and the next task's launch picks it up on its own, without
// interrupting whatever is in flight right now.
//
// An interactive session has no such boundary: it is one long-lived CLI
// process for the whole conversation. To actually take effect there, this
// soft-restarts the session immediately, resuming the same CLI conversation
// via --resume so the switch doesn't lose context — the tradeoff is that
// whatever tool call is in flight gets interrupted.
//
// A session that has never been started in this app process has no
// managedSession yet (the sidebar still shows it — see GetAllSessions'
// "configured" stub) — there is nothing running to switch, so this instead
// updates the in-memory config default directly, picked up whenever the
// session is first started.
//
// Nothing here touches disk: persisting the choice as the session's stored
// default is App.SetSessionModel's job (see CLAUDE.md "Live Model Switching"),
// since config files are owned by the app layer, not the manager.
func (m *SessionManager) SetSessionModel(id, model string) error {
	model = strings.TrimSpace(model)
	if model == "" {
		return fmt.Errorf("model must not be empty")
	}
	ms := m.get(id)
	if ms == nil {
		parts := strings.SplitN(id, "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("session %q not found", id)
		}
		_, sc, err := m.findConfig(parts[0], parts[1])
		if err != nil {
			return err
		}
		m.mu.Lock()
		sc.Model = model
		m.mu.Unlock()
		logger.L.Info("manager.set_session_model", "id", id, "model", model, "started", false)
		return nil
	}
	sess := ms.session
	sess.SetModel(model)
	logger.L.Info("manager.set_session_model", "id", id, "model", model)

	if sess.Autonomous() {
		return nil
	}

	st := sess.Status()
	if st == config.StatusIdle || st == config.StatusError {
		return nil // nothing running to restart
	}

	resumeID := sess.CLISessionID
	project, name := ms.project, ms.name
	if err := m.StopSession(id, false); err != nil {
		return err
	}
	// Give the Run goroutine a moment to exit before restarting (mirrors
	// RestartSession above).
	for i := 0; i < 50; i++ {
		if ms.session.Status() == config.StatusIdle {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return m.startSessionResuming(project, name, resumeID, model)
}

// startSessionResuming is StartSessionWithOverride plus a pre-seeded
// resumeSessionID, used only by SetSessionModel: the freshly recreated
// Session must resume the exact conversation that was just interrupted
// rather than starting a blank one, regardless of whether crash_recovery is
// enabled for this session.
func (m *SessionManager) startSessionResuming(project, name, resumeID, model string) error {
	proj, sc, err := m.findConfig(project, name)
	if err != nil {
		return err
	}
	id := sessionID(project, name)

	m.mu.Lock()
	if existing, ok := m.sessions[id]; ok {
		st := existing.session.Status()
		if st != config.StatusIdle && st != config.StatusError {
			m.mu.Unlock()
			return fmt.Errorf("session %q already running (status=%s)", id, st)
		}
		delete(m.sessions, id)
	}
	m.mu.Unlock()

	sessionCfg := *sc
	sessionCfg.Model = model

	ms := &managedSession{project: project, name: name}
	m.mu.Lock()
	primerFn := m.primerFn
	handoffFn := m.handoffFn
	m.mu.Unlock()
	sess := New(Params{
		ID:                id,
		ProjectName:       project,
		ProjectPath:       proj.Path,
		Config:            sessionCfg,
		ClaudePath:        m.cfg.Settings.ClaudePath,
		RetryDelay:        m.cfg.Settings.DefaultRetryDelay,
		RateLimitPauseSec: m.cfg.Settings.RateLimitPause,
		StateStore:        m.stateStore,
		CrashRecovery:     m.cfg.Settings.CrashRecovery,
		Gates:             proj.Gates,
		PrimerFn:          primerFn,
		Optimization:      &m.cfg.Optimization,
		HandoffFn:         handoffFn,
		ResumeSessionID:   resumeID,
		OnEvent:           func(sid string, ev SessionEvent) { m.onSessionEvent(sid, ev) },
	})
	ms.session = sess

	m.mu.Lock()
	m.sessions[id] = ms
	m.mu.Unlock()

	wait := m.reserveStartSlot()
	go func() {
		defer logger.Recover("manager.start_session", "id", id)
		if wait > 0 {
			time.Sleep(wait)
		}
		ctx, cancel := context.WithCancel(context.Background())
		ms.mu.Lock()
		ms.cancel = cancel
		ms.mu.Unlock()
		sess.Run(ctx)
	}()
	return nil
}

// StartProject launches every configured session in the project.
func (m *SessionManager) StartProject(project string) error {
	m.mu.Lock()
	var sessions []string
	for i := range m.cfg.Projects {
		p := &m.cfg.Projects[i]
		if p.Name != project {
			continue
		}
		for _, s := range p.Sessions {
			sessions = append(sessions, s.Name)
		}
	}
	m.mu.Unlock()
	if len(sessions) == 0 {
		return fmt.Errorf("project %q has no sessions", project)
	}
	for _, name := range sessions {
		if err := m.StartSession(project, name); err != nil {
			return err
		}
	}
	return nil
}

// StopProject stops every running session in the project.
func (m *SessionManager) StopProject(project string) error {
	m.mu.Lock()
	var ids []string
	for id, ms := range m.sessions {
		if ms.project == project {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.StopSession(id, false)
	}
	return nil
}

// ClearSessionState deletes the persisted crash-recovery state for project/session.
// This is the equivalent of --new in orchestrator.py: the next StartSession
// will launch a fresh conversation instead of resuming an interrupted one.
func (m *SessionManager) ClearSessionState(project, name string) {
	if m.stateStore == nil {
		return
	}
	m.stateStore.Clear(project, name)
	logger.L.Info("manager.clear_session_state", "project", project, "session", name)
}

// GetSessionState returns the persisted crash-recovery state for project/session,
// or nil if none exists. Used by the UI to show a "resume available" indicator.
func (m *SessionManager) GetSessionState(project, name string) *PersistedState {
	if m.stateStore == nil {
		return nil
	}
	st, _ := m.stateStore.Load(project, name)
	return st
}

// ---- Bidirectional input ----

// SendMessage writes a user message to the running session's stdin.
func (m *SessionManager) SendMessage(id, message string) error {
	ms := m.get(id)
	if ms == nil {
		return fmt.Errorf("session %q not found", id)
	}
	return ms.session.SendMessage(message)
}

// SendMessageWithImages writes a user message — optionally with image
// attachments pasted into the message box — to the running session's stdin.
func (m *SessionManager) SendMessageWithImages(id, message string, images []ImageAttachment) error {
	ms := m.get(id)
	if ms == nil {
		return fmt.Errorf("session %q not found", id)
	}
	return ms.session.SendMessageWithImages(message, images)
}

// RespondPermission resolves a pending permission request. decision is one
// of: allow, deny, allow_session, allow_similar, allow_always, deny_always.
func (m *SessionManager) RespondPermission(id, requestID, decision string) error {
	ms := m.get(id)
	if ms == nil {
		return fmt.Errorf("session %q not found", id)
	}
	// Record runtime rules so future identical requests are auto-approved.
	if req, ok := m.queue.Get(requestID); ok {
		switch decision {
		case "allow_session", "allow_similar", "allow_always":
			m.runtimeRules.Add(req.Tool, req.Pattern(), "allow")
		case "deny_always":
			m.runtimeRules.Add(req.Tool, req.Pattern(), "deny")
		}
		m.recordPermissionEvent(ms, req, decision, false)
	}
	m.queue.Remove(requestID)
	return ms.session.RespondPermission(requestID, decision)
}

// GetPendingPermissions returns every queued permission request across all
// sessions, in insertion order.
func (m *SessionManager) GetPendingPermissions() []permission.PermissionRequest {
	return m.queue.GetAll()
}

// AnswerQuestion resolves a pending ask-user question (see PendingQuestion):
// the session writes the answer to stdin as the next turn of the same
// conversation and returns to Working.
func (m *SessionManager) AnswerQuestion(id, questionID, answer string) error {
	ms := m.get(id)
	if ms == nil {
		return fmt.Errorf("session %q not found", id)
	}
	return ms.session.AnswerQuestion(questionID, answer)
}

// QuestionInfo pairs a session ID with its currently pending ask-user question.
type QuestionInfo struct {
	SessionID string          `json:"session_id"`
	Question  PendingQuestion `json:"question"`
}

// GetPendingQuestions returns every session currently blocked on an ask-user
// question (see PendingQuestion). Unlike permissions, there is no separate
// queue: a session holds at most one pending question at a time, so this
// just scans the live sessions.
func (m *SessionManager) GetPendingQuestions() []QuestionInfo {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	var out []QuestionInfo
	for _, id := range ids {
		ms := m.get(id)
		if ms == nil {
			continue
		}
		if q := ms.session.PendingQuestion(); q != nil {
			out = append(out, QuestionInfo{SessionID: id, Question: *q})
		}
	}
	return out
}

// ---- State / history / metrics ----

// GetAllSessions returns a snapshot for every known session, including
// configured sessions that have never been started (and so have no
// managedSession entry yet) — otherwise selecting one in the UI before its
// first run shows nothing at all.
func (m *SessionManager) GetAllSessions() []SessionState {
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	var configured []SessionState
	if m.cfg != nil {
		for _, p := range m.cfg.Projects {
			for _, s := range p.Sessions {
				id := sessionID(p.Name, s.Name)
				if _, ok := m.sessions[id]; ok {
					continue
				}
				configured = append(configured, SessionState{
					ID:             id,
					Project:        p.Name,
					Name:           s.Name,
					Status:         "idle",
					Model:          s.Model,
					Effort:         s.Effort,
					PermissionMode: s.PermissionMode,
					Prompt:         s.Prompt,
					Todos:          []TodoItem{},
				})
			}
		}
	}
	m.mu.Unlock()

	out := make([]SessionState, 0, len(ids)+len(configured))
	for _, id := range ids {
		if st, ok := m.GetSession(id); ok {
			out = append(out, st)
		}
	}
	out = append(out, configured...)
	return out
}

// GetSession returns a single session's snapshot, or (zero,false) if absent.
func (m *SessionManager) GetSession(id string) (SessionState, bool) {
	ms := m.get(id)
	if ms == nil {
		return SessionState{}, false
	}
	snap := ms.session.Snapshot()

	ms.mu.Lock()
	defer ms.mu.Unlock()

	st := SessionState{
		ID:             ms.session.ID,
		Project:        ms.project,
		Name:           ms.name,
		Status:         snap.Status.String(),
		Model:          firstNonEmpty(snap.ActiveModel, ms.session.Config.Model),
		Effort:         ms.session.Config.Effort,
		PermissionMode: ms.session.Config.PermissionMode,
		StartedAt:      snap.StartedAt,
		LastActivity:   snap.LastActivity,
		RateLimitUntil: ms.rateLimitUntil,
		TasksDone:      snap.TasksDone,
		CurrentTask:    snap.CurrentTask,
		TaskSourceDesc: snap.TaskSourceDesc,
		Prompt:         ms.session.Config.Prompt,
		Todos:          snap.Todos,
		Branch:         snap.Branch,
		CLISessionID:   snap.CLISessionID,
		StopRequested:  snap.StopRequested,
		InputTokens:    ms.inputTokens,
		OutputTokens:   ms.outputTokens,
		CacheRead:      ms.cacheReadTokens,
		CacheCreation:  ms.cacheCreateTokens,
		NumTurns:       ms.numTurns,
		TotalCostUSD:   ms.totalCostUSD,
		ContextWindow:  ms.contextWindow,
		ContextUtil:    ms.contextUtil,
	}
	if snap.PendingPerm != nil && snap.Status == config.StatusWaitingPermission {
		req := permission.PermissionRequest{
			ID:          snap.PendingPerm.ID,
			SessionID:   ms.session.ID,
			Tool:        snap.PendingPerm.Tool,
			Description: snap.PendingPerm.Description,
			Command:     snap.PendingPerm.Command,
			FilePath:    snap.PendingPerm.FilePath,
			RiskLevel:   snap.PendingPerm.RiskLevel,
		}
		st.PendingPermission = &req
	}
	if snap.PendingQuestion != nil && snap.Status == config.StatusWaitingForUser {
		q := *snap.PendingQuestion
		st.PendingQuestion = &q
	}
	return st, true
}

// GetSessionLog returns historical log entries for a session from the most
// recent run (querying SQLite by project/session name).
func (m *SessionManager) GetSessionLog(id string, offset, limit int) ([]*store.LogEntry, error) {
	ms := m.get(id)
	if ms == nil {
		return nil, fmt.Errorf("session %q not found", id)
	}
	if m.store == nil {
		return nil, nil
	}
	runs, err := m.store.ListRuns(ms.project, ms.name, 1)
	if err != nil {
		return nil, err
	}
	if len(runs) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = 500
	}
	return m.store.GetLogs(runs[0].ID, offset, limit)
}

// GetHistory returns past runs for a project (or all projects if empty).
func (m *SessionManager) GetHistory(project string, limit int) ([]*store.SessionRun, error) {
	if m.store == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 100
	}
	return m.store.ListRuns(project, "", limit)
}

// GetSessionMetrics returns the current run's accumulated cost / token usage.
func (m *SessionManager) GetSessionMetrics(id string) (SessionMetrics, error) {
	ms := m.get(id)
	if ms == nil {
		return SessionMetrics{}, fmt.Errorf("session %q not found", id)
	}
	ms.mu.Lock()
	defer ms.mu.Unlock()
	return SessionMetrics{
		SessionID:     id,
		InputTokens:   ms.inputTokens,
		OutputTokens:  ms.outputTokens,
		CacheRead:     ms.cacheReadTokens,
		CacheCreation: ms.cacheCreateTokens,
		NumTurns:      ms.numTurns,
		TotalCostUSD:  ms.totalCostUSD,
		ContextWindow: ms.contextWindow,
		ContextUtil:   ms.contextUtil,
		DurationMs:    ms.durationMs,
	}, nil
}

// GetDailyCost returns the total cost across all projects for the given date
// (format YYYY-MM-DD).
func (m *SessionManager) GetDailyCost(date string) (float64, error) {
	if m.store == nil {
		return 0, nil
	}
	m.mu.Lock()
	projects := make([]string, 0)
	if m.cfg != nil {
		for _, p := range m.cfg.Projects {
			projects = append(projects, p.Name)
		}
	}
	m.mu.Unlock()
	var total float64
	for _, p := range projects {
		dm, err := m.store.GetDailyMetrics(date, p)
		if err != nil {
			return 0, err
		}
		if dm != nil {
			total += dm.TotalCost
		}
	}
	return total, nil
}

// DailyTokens is a day's token volume across all projects, split by kind.
// The UI leads with tokens rather than dollars: on a subscription the dollar
// figure is a price-list calculation of something already paid for, while the
// token volume is what the rate limit actually meters. The split matters
// because cache reads are an order of magnitude cheaper than fresh input —
// two days with the same total can differ several-fold in real cost.
type DailyTokens struct {
	Date          string `json:"date"`
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	CacheRead     int64  `json:"cache_read"`
	CacheCreation int64  `json:"cache_creation"`
	Total         int64  `json:"total"`
	CostUSD       float64 `json:"cost_usd"`
}

// GetDailyTokens returns the token volume across all projects for the given
// date (format YYYY-MM-DD). It reads the same daily_metrics rows as
// GetDailyCost, so the token and dollar figures shown side by side always
// cover the same set of runs.
func (m *SessionManager) GetDailyTokens(date string) (DailyTokens, error) {
	out := DailyTokens{Date: date}
	if m.store == nil {
		return out, nil
	}
	m.mu.Lock()
	projects := make([]string, 0)
	if m.cfg != nil {
		for _, p := range m.cfg.Projects {
			projects = append(projects, p.Name)
		}
	}
	m.mu.Unlock()
	for _, p := range projects {
		dm, err := m.store.GetDailyMetrics(date, p)
		if err != nil {
			return DailyTokens{Date: date}, err
		}
		if dm == nil {
			continue
		}
		out.InputTokens += dm.TotalInputTokens
		out.OutputTokens += dm.TotalOutputTokens
		out.CacheRead += dm.TotalCacheReadTokens
		out.CacheCreation += dm.TotalCacheCreationTokens
		out.CostUSD += dm.TotalCost
	}
	out.Total = out.InputTokens + out.OutputTokens + out.CacheRead + out.CacheCreation
	return out, nil
}

// GetProjectCost returns the total cost for a project over the last `days`
// days.
func (m *SessionManager) GetProjectCost(project string, days int) (float64, error) {
	if m.store == nil {
		return 0, nil
	}
	if days <= 0 {
		days = 30
	}
	metrics, err := m.store.ListDailyMetrics(project, days)
	if err != nil {
		return 0, err
	}
	var total float64
	for _, dm := range metrics {
		total += dm.TotalCost
	}
	return total, nil
}

// GetProjectTokens returns the token volume for a project over the last `days`
// days — the token twin of GetProjectCost, reading the same daily_metrics rows
// so the two units describe the same runs.
func (m *SessionManager) GetProjectTokens(project string, days int) (DailyTokens, error) {
	var out DailyTokens
	if m.store == nil {
		return out, nil
	}
	if days <= 0 {
		days = 30
	}
	metrics, err := m.store.ListDailyMetrics(project, days)
	if err != nil {
		return DailyTokens{}, err
	}
	for _, dm := range metrics {
		out.InputTokens += dm.TotalInputTokens
		out.OutputTokens += dm.TotalOutputTokens
		out.CacheRead += dm.TotalCacheReadTokens
		out.CacheCreation += dm.TotalCacheCreationTokens
		out.CostUSD += dm.TotalCost
	}
	out.Total = out.InputTokens + out.OutputTokens + out.CacheRead + out.CacheCreation
	return out, nil
}

// GetRateLimitStatus returns the most-restrictive active rate-limit window
// across all sessions (the one with the latest resetsAt) or nil if none.
func (m *SessionManager) GetRateLimitStatus() *RateLimitInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	var latest *RateLimitInfo
	var latestUntil time.Time
	for _, ms := range m.sessions {
		ms.mu.Lock()
		if ms.rateLimit != nil && ms.rateLimitUntil.After(time.Now()) {
			if ms.rateLimitUntil.After(latestUntil) {
				cp := *ms.rateLimit
				latest = &cp
				latestUntil = ms.rateLimitUntil
			}
		}
		ms.mu.Unlock()
	}
	return latest
}

// ---- Event handling ----

// onSessionEvent is the bridge from a Session goroutine into the manager.
// It runs on the session's goroutine, so handlers must not block on Wails.
func (m *SessionManager) onSessionEvent(id string, ev SessionEvent) {
	ms := m.get(id)
	if ms == nil {
		return
	}

	switch ev.Type {
	case EvtStatus:
		m.handleStatus(ms, ev.Status)

	case EvtInit:
		if ev.Init != nil {
			logger.L.Info("session.init",
				"id", id,
				"model", ev.Init.Model,
				"cli_session_id", ev.Init.SessionID,
				"tools_count", len(ev.Init.Tools),
			)
			ms.mu.Lock()
			ms.runModel = ev.Init.Model
			ms.runCLISession = ev.Init.SessionID
			ms.mu.Unlock()
			m.persistRunHeader(ms)
		}
		m.emit(EventNameInit, InitEvent{ID: id, Info: ev.Init})

	case EvtLog:
		if ev.Entry != nil {
			ms.mu.Lock()
			ms.pendingLogs = append(ms.pendingLogs, store.LogEntry{
				Timestamp: ev.Entry.Time,
				Level:     ev.Entry.Level,
				Message:   ev.Entry.Message,
				ToolName:  ev.Entry.ToolName,
				ToolInput: ev.Entry.ToolInput,
			})
			ms.mu.Unlock()
			m.emit(EventNameLog, LogEvent{ID: id, Entry: *ev.Entry})
		}

	case EvtUsage:
		if ev.Usage != nil {
			m.handleUsage(ms, ev.Usage)
		}

	case EvtTodo:
		m.emit(EventNameTodo, TodoEvent{
			ID:          id,
			Todos:       ev.Todos,
			CurrentTask: currentTaskFromTodos(ev.Todos),
		})

	case EvtTaskSource:
		m.emit(EventNameTaskSource, TaskSourceEvent{
			ID:                    id,
			TaskSourceDescription: ev.TaskSourceDesc,
		})

	case EvtResult:
		if ev.Result != nil {
			logger.L.Info("session.result",
				"id", id,
				"cost_usd", ev.Result.TotalCostUSD,
				"num_turns", ev.Result.NumTurns,
				"duration_ms", ev.Result.DurationMs,
			)
			m.handleResult(ms, ev.Result)
			m.emit(EventNameResult, ResultEvent{ID: id, Result: ev.Result})
		}

	case EvtTaskDone:
		ms.mu.Lock()
		runID := ms.runID
		ms.mu.Unlock()
		if runID != 0 {
			m.finishRun(ms, "completed", "")
		}
		m.emit(EventNameTaskDone, TaskDoneEvent{ID: id, TasksDone: ev.TasksDone})

	case EvtRateLimit:
		if ev.RateLimit != nil {
			until := time.Time{}
			if ev.RateLimit.ResetsAt > 0 {
				until = time.Unix(ev.RateLimit.ResetsAt, 0)
			}
			logger.L.Warn("session.rate_limit",
				"id", id,
				"utilization", ev.RateLimit.Utilization,
				"resets_at", until,
			)
			ms.mu.Lock()
			cp := *ev.RateLimit
			ms.rateLimit = &cp
			ms.rateLimitUntil = until
			ms.mu.Unlock()
			m.emit(EventNameRateLimit, RateLimitEvent{ID: id, Info: ev.RateLimit, Until: until})
		}

	case EvtPermission:
		if ev.Permission != nil {
			logger.L.Info("session.permission_request",
				"id", id,
				"tool", ev.Permission.Tool,
				"description", ev.Permission.Description,
				"risk", ev.Permission.RiskLevel,
			)
			m.handlePermission(ms, ev.Permission)
		}

	case EvtQuestion:
		if ev.Question != nil {
			logger.L.Info("session.question", "id", id, "question", ev.Question.Question)
			m.emit(EventNameQuestion, QuestionEvent{ID: id, Question: *ev.Question})
		}

	case EvtError:
		msg := ""
		if ev.Err != nil {
			msg = ev.Err.Error()
			logger.L.Error("session.error", "id", id, "error", msg)
		}
		ms.mu.Lock()
		runID := ms.runID
		ms.mu.Unlock()
		if runID != 0 {
			m.finishRun(ms, "error", msg)
		}
		m.emit(EventNameError, ErrorEvent{ID: id, Message: msg})
	}
}

// handleStatus emits the status event and brackets run records.
func (m *SessionManager) handleStatus(ms *managedSession, st config.SessionStatus) {
	m.emit(EventNameStatus, StatusEvent{ID: ms.session.ID, Status: st.String()})

	switch st {
	case config.StatusStarting:
		// A fresh run never carries over a previous run's soft-stop request
		// (Session.New starts with softStop=false) — tell the UI so a stale
		// "stop requested" badge doesn't survive a restart.
		m.emit(EventNameStopReq, StopRequestedEvent{ID: ms.session.ID, StopRequested: false})
		// Begin a new run record.
		m.beginRun(ms)
	case config.StatusIdle:
		// Run loop ended without an explicit task/error event — finalize as stopped.
		ms.mu.Lock()
		runID := ms.runID
		ms.mu.Unlock()
		if runID != 0 {
			m.finishRun(ms, "stopped", "")
		}
	}
}

// beginRun inserts a new session_runs row and resets per-run metrics.
func (m *SessionManager) beginRun(ms *managedSession) {
	if m.store == nil {
		return
	}
	ms.mu.Lock()
	if ms.runID != 0 {
		ms.mu.Unlock()
		return // run already in flight
	}
	ms.runStartedAt = time.Now()
	ms.inputTokens = 0
	ms.outputTokens = 0
	ms.cacheReadTokens = 0
	ms.cacheCreateTokens = 0
	ms.numTurns = 0
	ms.totalCostUSD = 0
	ms.contextUtil = 0
	ms.durationMs = 0
	ms.pendingLogs = ms.pendingLogs[:0]
	project, name := ms.project, ms.name
	model := ms.session.Config.Model
	effort := ms.session.Config.Effort
	cliSessionID := ms.session.CLISessionID
	startedAt := ms.runStartedAt
	ms.mu.Unlock()

	run := &store.SessionRun{
		Project:      project,
		Session:      name,
		CLISessionID: cliSessionID,
		Model:        model,
		Effort:       effort,
		StartedAt:    startedAt,
		Status:       "running",
	}
	if err := m.store.InsertRun(run); err != nil {
		return
	}
	ms.mu.Lock()
	ms.runID = run.ID
	ms.mu.Unlock()
}

// persistRunHeader updates the run row with model + cli session id once the
// system/init event arrives (we may have inserted earlier with stale model).
func (m *SessionManager) persistRunHeader(ms *managedSession) {
	if m.store == nil {
		return
	}
	ms.mu.Lock()
	runID := ms.runID
	model := ms.runModel
	cliSessionID := ms.runCLISession
	ms.mu.Unlock()
	if runID == 0 {
		return
	}
	existing, err := m.store.GetRun(runID)
	if err != nil || existing == nil {
		return
	}
	if model != "" {
		existing.Model = model
	}
	if cliSessionID != "" {
		existing.CLISessionID = cliSessionID
	}
	_ = m.store.UpdateRun(existing)
}

// handleUsage accumulates per-turn token usage and emits a context event.
func (m *SessionManager) handleUsage(ms *managedSession, usage *TokenUsage) {
	ms.mu.Lock()
	ms.inputTokens += int64(usage.InputTokens)
	ms.outputTokens += int64(usage.OutputTokens)
	ms.cacheReadTokens += int64(usage.CacheReadInputTokens)
	ms.cacheCreateTokens += int64(usage.CacheCreationInputTokens)
	ms.numTurns++
	total := int(usage.InputTokens) + int(usage.CacheReadInputTokens) + int(usage.CacheCreationInputTokens)
	if ms.contextWindow > 0 {
		ms.contextUtil = float64(total) / float64(ms.contextWindow)
	}
	id := ms.session.ID
	payload := ContextEvent{
		ID:            id,
		InputTokens:   usage.InputTokens,
		OutputTokens:  usage.OutputTokens,
		CacheRead:     usage.CacheReadInputTokens,
		CacheCreation: usage.CacheCreationInputTokens,
		ContextWindow: ms.contextWindow,
		Utilization:   ms.contextUtil,
	}
	ms.mu.Unlock()
	m.emit(EventNameContext, payload)
}

// handleResult captures the per-run final metrics (cost, num_turns, duration,
// contextWindow from modelUsage).
func (m *SessionManager) handleResult(ms *managedSession, res *SessionResult) {
	ms.mu.Lock()
	ms.totalCostUSD = res.TotalCostUSD
	ms.lastResultText = res.ResultText
	if res.NumTurns > 0 {
		ms.numTurns = res.NumTurns
	}
	if res.DurationMs > 0 {
		ms.durationMs = res.DurationMs
	}
	// Pick up contextWindow from any modelUsage entry.
	for _, mu := range res.ModelUsage {
		if mu.ContextWindow > 0 {
			ms.contextWindow = mu.ContextWindow
			break
		}
	}
	if ms.contextWindow > 0 {
		used := ms.inputTokens + ms.cacheReadTokens + ms.cacheCreateTokens
		ms.contextUtil = float64(used) / float64(ms.contextWindow)
	}
	ms.mu.Unlock()
}

// finishRun finalizes the in-flight run record with the given status / error
// and flushes buffered logs.
func (m *SessionManager) finishRun(ms *managedSession, status, errMsg string) {
	if m.store == nil {
		return
	}
	ms.mu.Lock()
	runID := ms.runID
	if runID == 0 {
		ms.mu.Unlock()
		return
	}
	ms.runID = 0

	now := time.Now()
	run := &store.SessionRun{
		ID:                  runID,
		Project:             ms.project,
		Session:             ms.name,
		Model:               ms.runModel,
		FinishedAt:          &now,
		Status:              status,
		TasksDone:           ms.session.TasksDone(),
		ErrorMsg:            errMsg,
		TotalCostUSD:        ms.totalCostUSD,
		InputTokens:         ms.inputTokens,
		OutputTokens:        ms.outputTokens,
		CacheReadTokens:     ms.cacheReadTokens,
		CacheCreationTokens: ms.cacheCreateTokens,
		NumTurns:            ms.numTurns,
		DurationMs:          ms.durationMs,
	}
	if run.Model == "" {
		run.Model = ms.session.Config.Model
	}
	run.Effort = ms.session.Config.Effort
	logs := ms.pendingLogs
	ms.pendingLogs = nil
	totalCost := ms.totalCostUSD
	inTok := ms.inputTokens
	outTok := ms.outputTokens
	cacheReadTok := ms.cacheReadTokens
	cacheCreateTok := ms.cacheCreateTokens
	resultText := ms.lastResultText
	project := ms.project
	ms.mu.Unlock()

	_ = m.store.UpdateRun(run)
	if len(logs) > 0 {
		_ = m.store.InsertLogs(runID, logs)
	}

	// Auto-save this run's log to <project>/.claude-manager/logs/ as markdown
	// so it survives independently of the SQLite history (and of "Clear log"
	// in the UI, which only empties the on-screen buffer). Fire-and-forget:
	// a failure here must never affect the run's own completed/error/stopped
	// status, just get logged.
	if len(logs) > 0 {
		projectPath := ms.session.ProjectPath
		sessID := ms.session.ID
		go func() {
			defer logger.Recover("manager.autosave_log", "id", sessID)
			path, err := store.SaveSessionLogFile(projectPath, sessID, logs)
			if err != nil {
				logger.L.Error("session.log_autosave_failed", "id", sessID, "error", err)
			} else if path != "" {
				logger.L.Info("session.log_autosaved", "id", sessID, "path", path)
			}
		}()
	}

	if status == "completed" {
		_ = m.store.AddDailyMetrics(&store.DailyMetrics{
			Date:                     now.Format("2006-01-02"),
			Project:                  project,
			TotalCost:                totalCost,
			TotalInputTokens:         inTok,
			TotalOutputTokens:        outTok,
			TotalCacheReadTokens:     cacheReadTok,
			TotalCacheCreationTokens: cacheCreateTok,
			TotalRuns:                1,
			TotalTasks:               1,
		})
	}

	// Index this run's transcript into action_signatures for the "Actions"
	// tab (LEARN-TASKS.md LN-03) — same fire-and-forget goroutine pattern as
	// the log autosave above, so a failure here can never affect the run's
	// own completed/error/stopped status. Gated on experience_tracking so the
	// flag being off means a transcript is never opened at all, not just that
	// the result is discarded.
	m.mu.Lock()
	indexRun := m.indexRun
	tracking := m.cfg != nil && m.cfg.Optimization.ExperienceTracking
	m.mu.Unlock()
	if indexRun != nil && tracking {
		sessID := ms.session.ID
		cliSessionID := ms.session.CLISessionID
		projectPath := ms.session.ProjectPath
		taskPtr := ms.session.taskSourceDesc
		sessionName := ms.name
		go func() {
			defer logger.Recover("manager.index_run", "id", sessID)
			if err := indexRun(project, sessionName, runID, cliSessionID, projectPath, taskPtr); err != nil {
				logger.L.Error("session.index_run_failed", "id", sessID, "error", err)
			}
		}()
	}

	// Distill this run into one journal entry for the project's episodic
	// memory (LEARN-TASKS.md LN-06) — same fire-and-forget goroutine pattern
	// as the transcript indexer above. Gated on the project's own Journal
	// flag (off by default) and on a writer actually being wired: with either
	// missing, the analyst is never invoked at all, not just discarded.
	if status == "completed" {
		pcfg := m.findProjectConfig(project)
		m.mu.Lock()
		journalWrite := m.journalFn
		journalAnalyze := m.journalAnalyze
		m.mu.Unlock()
		if pcfg != nil && pcfg.Journal && journalWrite != nil {
			sessID := ms.session.ID
			projectPath := ms.session.ProjectPath
			taskPtr := ms.session.taskSourceDesc
			commit := pcfg.JournalCommit
			files := filesChangedFromLogs(logs)
			go func() {
				defer logger.Recover("manager.journal", "id", sessID)
				m.runJournalEntry(context.Background(), project, projectPath, taskPtr, resultText, files, commit, journalAnalyze)
			}()
		}
	}
}

// filesChangedFromLogs extracts the Edit/Write file paths from one run's
// buffered log entries — the in-memory equivalent of primer.go's
// previousRunFilesSection, but for the run that just finished rather than a
// past one read back through the store. The journal's "this run" needs data
// action_signatures may not have yet: LN-03 ingestion runs in its own async
// goroutine off this same finishRun call, so it cannot be relied on to have
// indexed this run already.
func filesChangedFromLogs(logs []store.LogEntry) []string {
	seen := make(map[string]bool)
	var files []string
	for _, l := range logs {
		if l.Level != "tool" || (l.ToolName != "Edit" && l.ToolName != "Write") {
			continue
		}
		f := strings.TrimSpace(l.ToolInput)
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		files = append(files, f)
	}
	return files
}

// runJournalEntry distills one completed run into a journal entry via
// analyze and persists it through m.journalFn (LEARN-TASKS.md LN-06). Kept as
// its own method (rather than inlined in finishRun's goroutine) so tests can
// call it synchronously with a stub analyzer.
func (m *SessionManager) runJournalEntry(ctx context.Context, project, projectPath, taskPtr, resultText string, filesChanged []string, commit bool, analyze journalAnalyzeFn) {
	m.mu.Lock()
	write := m.journalFn
	acfg := analysis.AnalysisConfig{
		ClaudePath: m.cfg.Settings.ClaudePath,
		Model:      m.cfg.Settings.PreflightModel,
	}
	m.mu.Unlock()
	if write == nil || analyze == nil {
		return
	}

	result, err := analyze(ctx, projectPath, analysis.JournalInput{
		TaskPtr:      taskPtr,
		FilesChanged: filesChanged,
		ResultText:   resultText,
	}, acfg)
	if err != nil {
		logger.L.Error("manager.journal_failed", "project", project, "error", err)
		return
	}

	entry := JournalEntry{
		Date:      time.Now(),
		TaskPtr:   taskPtr,
		Done:      result.Done,
		Surprises: result.Surprises,
		Avoid:     result.Avoid,
	}
	if err := write(projectPath, commit, entry); err != nil {
		logger.L.Error("manager.journal_write_failed", "project", project, "error", err)
	}
}

// handlePermission applies auto-approval rules and emits a UI event when a
// human decision is required.
func (m *SessionManager) handlePermission(ms *managedSession, req *PermissionRequest) {
	permReq := permission.PermissionRequest{
		ID:           req.ID,
		SessionID:    ms.session.ID,
		Tool:         req.Tool,
		Description:  req.Description,
		Command:      req.Command,
		FilePath:     req.FilePath,
		RiskLevel:    req.RiskLevel,
		Timestamp:    time.Now(),
		WaitingSince: time.Now(),
	}

	// 1. Session-config rules (per-session, declared in TOML).
	if dec, _, ok := permission.MatchRules(ms.session.Config.PermissionRules, permReq); ok {
		if dec != "ask" {
			m.recordPermissionEvent(ms, permReq, dec, true)
			_ = ms.session.RespondPermission(permReq.ID, dec)
			return
		}
	}
	// 2. Runtime rules (allow_session / allow_always added at runtime).
	if dec, ok := m.runtimeRules.Match(permReq); ok {
		if dec != "ask" {
			m.recordPermissionEvent(ms, permReq, dec, true)
			_ = ms.session.RespondPermission(permReq.ID, dec)
			return
		}
	}
	// 3. Bypass mode auto-allows everything.
	if ms.session.Config.PermissionMode == "bypassPermissions" {
		m.recordPermissionEvent(ms, permReq, "allow", true)
		_ = ms.session.RespondPermission(permReq.ID, "allow")
		return
	}

	// Otherwise queue + notify the UI.
	m.queue.Add(permReq)
	m.emit(EventNamePermission, PermissionEvent{ID: ms.session.ID, Request: permReq})
}

// recordPermissionEvent persists one resolved permission request into
// permission_events (LEARN-TASKS.md LN-04) — both auto-decided (a rule or
// bypassPermissions, called from handlePermission) and human-decided (called
// from RespondPermission), so a later candidate query can tell "still needs
// asking" apart from "a rule already covers this". Gated on the same
// [optimization] experience_tracking flag as the rest of the experience
// layer (off by default) and a no-op without a store (e.g. playwright-server).
func (m *SessionManager) recordPermissionEvent(ms *managedSession, req permission.PermissionRequest, decision string, auto bool) {
	if m.store == nil || m.cfg == nil || !m.cfg.Optimization.ExperienceTracking {
		return
	}
	ms.mu.Lock()
	var runID *int64
	if ms.runID != 0 {
		v := ms.runID
		runID = &v
	}
	project, name := ms.project, ms.name
	ms.mu.Unlock()

	_ = m.store.InsertPermissionEvent(store.PermissionEvent{
		Project:   project,
		Session:   name,
		RunID:     runID,
		Tool:      req.Tool,
		Pattern:   req.Pattern(),
		Decision:  decision,
		Auto:      auto,
		Timestamp: time.Now(),
	})
}

// ---- Pre-flight plans (PLAN.md section 17) ----

// EventNamePlanStatus notifies the frontend/control-plane about plan lifecycle
// transitions (executing → completed/failed).
const EventNamePlanStatus = "plan:status"

// PlanStatusEvent is the payload of EventNamePlanStatus.
type PlanStatusEvent struct {
	PlanID  int64  `json:"plan_id"`
	Project string `json:"project"`
	Status  string `json:"status"`
}

// analyzeFn abstracts analysis.RunAnalysis so tests can stub the analyst CLI.
type analyzeFn func(ctx context.Context, projectPath, task string, cfg analysis.AnalysisConfig) (*analysis.AnalysisResult, error)

// RunPreflight runs the analyst on an ad-hoc task for the project and persists
// the resulting draft plan. The returned plan carries its store ID.
func (m *SessionManager) RunPreflight(project, task string) (*analysis.TaskPlan, error) {
	return m.runPreflight(context.Background(), project, task, analysis.RunAnalysis)
}

func (m *SessionManager) runPreflight(ctx context.Context, project, task string, analyze analyzeFn) (*analysis.TaskPlan, error) {
	path, err := m.projectPath(project)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	acfg := analysis.AnalysisConfig{
		ClaudePath:   m.cfg.Settings.ClaudePath,
		Model:        m.cfg.Settings.PreflightModel,
		MaxBudgetUSD: m.cfg.Settings.PreflightMaxBudget,
	}
	m.mu.Unlock()

	result, err := analyze(ctx, path, task, acfg)
	if err != nil {
		return nil, fmt.Errorf("preflight: %w", err)
	}
	plan := analysis.NewPlanFromAnalysis(project, task, result)
	if err := analysis.SavePlan(m.store, plan); err != nil {
		return nil, err
	}
	logger.L.Info("manager.preflight",
		"project", project, "plan_id", plan.ID, "subtasks", len(plan.Subtasks))
	return plan, nil
}

// EventNameRoadmapProgress notifies the frontend of activity (assistant text,
// tool calls) while GenerateRoadmap's analyst CLI run is in flight — that run
// takes minutes on Opus and, unlike a normal session, has no other visible
// signal that it is still alive rather than hung.
const EventNameRoadmapProgress = "plan:roadmap_progress"

// RoadmapProgressEvent is the payload of EventNameRoadmapProgress.
type RoadmapProgressEvent struct {
	Project string `json:"project"`
	Text    string `json:"text"`
}

// roadmapAnalyzeFn abstracts analysis.RunAnalysisStreaming so tests can stub
// the analyst CLI. Kept distinct from analyzeFn (used by RunPreflight, which
// has no need for progress reporting) to keep that path unchanged.
type roadmapAnalyzeFn func(ctx context.Context, projectPath, task string, cfg analysis.AnalysisConfig, onProgress analysis.ProgressFunc) (*analysis.AnalysisResult, error)

// GenerateRoadmap decomposes a whole project idea into a durable backlog
// (draft roadmap plan), using Opus by default — unlike RunPreflight's
// single-task triage, roadmap quality is the main lever on every downstream
// session's success, so it defaults to the strongest model. model overrides
// the default when non-empty (e.g. "sonnet"/"haiku" for cheaper iteration).
func (m *SessionManager) GenerateRoadmap(project, idea, model string) (*analysis.TaskPlan, error) {
	return m.generateRoadmap(context.Background(), project, idea, model, analysis.RunAnalysisStreaming)
}

func (m *SessionManager) generateRoadmap(ctx context.Context, project, idea, model string, analyze roadmapAnalyzeFn) (*analysis.TaskPlan, error) {
	path, err := m.projectPath(project)
	if err != nil {
		return nil, err
	}
	if model == "" {
		model = "opus"
	}
	m.mu.Lock()
	acfg := analysis.AnalysisConfig{
		ClaudePath:   m.cfg.Settings.ClaudePath,
		Model:        model,
		Effort:       "high",
		SystemPrompt: analysis.RoadmapSystemPrompt,
		JSONSchema:   analysis.RoadmapJSONSchema,
		MaxBudgetUSD: m.cfg.Settings.PreflightMaxBudget,
	}
	m.mu.Unlock()

	onProgress := func(text string) {
		m.emit(EventNameRoadmapProgress, RoadmapProgressEvent{Project: project, Text: text})
	}
	result, err := analyze(ctx, path, idea, acfg, onProgress)
	if err != nil {
		return nil, fmt.Errorf("roadmap: %w", err)
	}
	plan := analysis.NewPlanFromAnalysis(project, idea, result)
	plan.Kind = analysis.PlanKindRoadmap
	if err := analysis.SavePlan(m.store, plan); err != nil {
		return nil, err
	}
	logger.L.Info("manager.roadmap_generated",
		"project", project, "plan_id", plan.ID, "subtasks", len(plan.Subtasks), "model", model)
	return plan, nil
}

// GetLatestDraftRoadmap returns the most recently generated but not-yet-
// approved roadmap plan for a project, or nil if there isn't one. Recovers a
// plan whose GenerateRoadmap response never reached the frontend (a dropped
// Wails IPC callback, a reloaded page mid-request) without re-running — and
// re-paying for — the analyst call; the plan itself was already durably saved
// by generateRoadmap above before GenerateRoadmap ever returned to its caller.
func (m *SessionManager) GetLatestDraftRoadmap(project string) (*analysis.TaskPlan, error) {
	if m.store == nil {
		return nil, nil
	}
	plans, err := m.store.ListPlans(project, 10)
	if err != nil {
		return nil, err
	}
	for _, p := range plans {
		if p.Kind == string(analysis.PlanKindRoadmap) && p.Status == string(analysis.PlanStatusDraft) {
			return analysis.LoadPlan(m.store, p.ID)
		}
	}
	return nil, nil
}

// EventNameSkillProgress notifies the frontend of activity while
// DistillSkill's analyst CLI run is in flight (LEARN-TASKS.md LN-09) — mirrors
// EventNameRoadmapProgress above for the same reason: a distillation run has
// no other visible sign of being alive rather than hung.
const EventNameSkillProgress = "skill:progress"

// SkillProgressEvent is the payload of EventNameSkillProgress.
type SkillProgressEvent struct {
	Project string `json:"project"`
	Text    string `json:"text"`
}

// skillDistillFn abstracts analysis.DistillSkill so tests can stub the
// analyst CLI, mirroring roadmapAnalyzeFn above.
type skillDistillFn func(ctx context.Context, projectPath string, in analysis.SkillDistillInput, score, minScore float64, cfg analysis.AnalysisConfig, onProgress analysis.ProgressFunc) (*analysis.SkillDraft, error)

// DistillSkill turns one recurring tool-call sequence into a skill draft and
// persists it to the `skills` table as status=draft (LEARN-TASKS.md LN-09).
// in is built by the caller (app.go) from an internal/experience
// SkillCandidate — see analysis.SkillDistillInput's doc comment for why this
// package cannot take that type directly. Returns analysis.ErrBelowThreshold
// when score does not clear minScore (<=0 uses analysis.DefaultSkillMinScore)
// — the caller should treat that as "skip this candidate", not a failure.
func (m *SessionManager) DistillSkill(project string, in analysis.SkillDistillInput, sourceJSON string, score, minScore float64, model string) (*store.Skill, error) {
	return m.distillSkill(context.Background(), project, in, sourceJSON, score, minScore, model, analysis.DistillSkill)
}

func (m *SessionManager) distillSkill(ctx context.Context, project string, in analysis.SkillDistillInput, sourceJSON string, score, minScore float64, model string, distill skillDistillFn) (*store.Skill, error) {
	if m.store == nil {
		return nil, fmt.Errorf("manager: no store configured")
	}
	path, err := m.projectPath(project)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	acfg := analysis.AnalysisConfig{
		ClaudePath:   m.cfg.Settings.ClaudePath,
		Model:        model,
		MaxBudgetUSD: m.cfg.Settings.PreflightMaxBudget,
	}
	m.mu.Unlock()

	onProgress := func(text string) {
		m.emit(EventNameSkillProgress, SkillProgressEvent{Project: project, Text: text})
	}
	draft, err := distill(ctx, path, in, score, minScore, acfg, onProgress)
	if err != nil {
		return nil, err
	}

	draftJSON, err := json.Marshal(draft)
	if err != nil {
		return nil, fmt.Errorf("manager: encode skill draft: %w", err)
	}

	sk := &store.Skill{
		Project:    project,
		Name:       draft.Name,
		Status:     "draft",
		DraftJSON:  string(draftJSON),
		MD:         analysis.RenderSkillMarkdown(*draft),
		SourceJSON: sourceJSON,
		CreatedAt:  time.Now(),
	}
	if err := m.store.InsertSkill(sk); err != nil {
		return nil, err
	}
	logger.L.Info("manager.skill_distilled",
		"project", project, "skill", sk.Name, "id", sk.ID, "cost_usd", draft.CostUSD)
	return sk, nil
}

// ApproveRoadmapFiles materializes an approved roadmap plan into
// <project>/ROADMAP.md + <project>/STATUS-P1.md (see analysis.WriteRoadmapFiles)
// and marks the plan completed. Rejects plans that aren't PlanKindRoadmap, so
// an ad-hoc plan can never be accidentally written as project files, and
// ExecutePlan (below) rejects the reverse case.
func (m *SessionManager) ApproveRoadmapFiles(planID int64, overwrite bool) (plan *analysis.TaskPlan, roadmapPath, statusPath string, err error) {
	plan, err = analysis.LoadPlan(m.store, planID)
	if err != nil {
		return nil, "", "", err
	}
	if plan == nil {
		return nil, "", "", fmt.Errorf("approve roadmap: plan %d not found", planID)
	}
	if plan.Kind != analysis.PlanKindRoadmap {
		return nil, "", "", fmt.Errorf("approve roadmap: plan %d is not a roadmap plan", planID)
	}
	path, err := m.projectPath(plan.Project)
	if err != nil {
		return nil, "", "", err
	}
	roadmapPath, statusPath, err = analysis.WriteRoadmapFiles(path, plan, overwrite)
	if err != nil {
		return nil, "", "", err
	}
	// The roadmap alone is a queue with no rules for working through it. The
	// protocol is what makes an interrupted session harmless: it reserves a task
	// with a branch and merges after every commit, so the next session finds the
	// work instead of starting the task over. Installed here, in the same step
	// that creates the queue, because a project that gets one without the other
	// is exactly the setup that silently re-implements its own tasks.
	protocolFiles, err := analysis.WriteProtocolFiles(path, analysis.ProtocolParams{
		QueueFile:  filepath.Base(statusPath),
		MainBranch: gitutil.MainBranch(context.Background(), path),
		Gates:      m.projectGates(plan.Project),
	})
	if err != nil {
		return nil, "", "", err
	}
	if len(protocolFiles) > 0 {
		logger.L.Info("manager.protocol_written", "project", plan.Project, "files", strings.Join(protocolFiles, ", "))
	}
	plan.Status = analysis.PlanStatusCompleted
	now := time.Now()
	plan.CompletedAt = &now
	if err := analysis.SavePlan(m.store, plan); err != nil {
		return nil, "", "", err
	}
	logger.L.Info("manager.roadmap_written",
		"plan_id", plan.ID, "project", plan.Project, "roadmap", roadmapPath, "status_file", statusPath)
	return plan, roadmapPath, statusPath, nil
}

// ApprovePlan persists an (operator-edited) plan with status approved and
// returns it — the store ID is assigned on first save, so the frontend must
// use the returned plan for the follow-up ExecutePlan call.
func (m *SessionManager) ApprovePlan(plan *analysis.TaskPlan) (*analysis.TaskPlan, error) {
	if plan == nil {
		return nil, fmt.Errorf("approve plan: nil plan")
	}
	plan.Status = analysis.PlanStatusApproved
	if err := analysis.SavePlan(m.store, plan); err != nil {
		return nil, err
	}
	logger.L.Info("manager.plan_approved", "plan_id", plan.ID, "project", plan.Project)
	return plan, nil
}

// ExecutePlan loads plan planID from the store and executes it with the
// one-shot CLI executor. Blocks until the plan completes or fails; progress is
// flushed to the store after every subtask state change (poll GetPlan) and the
// final transition is emitted as EventNamePlanStatus.
func (m *SessionManager) ExecutePlan(planID int64) error {
	m.mu.Lock()
	ex := &analysis.CLIExecutor{ClaudePath: m.cfg.Settings.ClaudePath}
	m.mu.Unlock()
	return m.executePlan(context.Background(), planID, ex)
}

func (m *SessionManager) executePlan(ctx context.Context, planID int64, ex analysis.SubtaskExecutor) error {
	plan, err := analysis.LoadPlan(m.store, planID)
	if err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("execute plan: plan %d not found", planID)
	}
	if plan.Kind == analysis.PlanKindRoadmap {
		return fmt.Errorf("execute plan: plan %d is a roadmap plan; use ApproveRoadmapFiles instead", planID)
	}
	path, err := m.projectPath(plan.Project)
	if err != nil {
		return err
	}

	logger.L.Info("manager.plan_execute", "plan_id", plan.ID, "project", plan.Project, "subtasks", len(plan.Subtasks))
	m.emit(EventNamePlanStatus, PlanStatusEvent{PlanID: plan.ID, Project: plan.Project, Status: string(analysis.PlanStatusExecuting)})

	execErr := analysis.ExecutePlan(ctx, plan, path, ex, m.store)

	m.emit(EventNamePlanStatus, PlanStatusEvent{PlanID: plan.ID, Project: plan.Project, Status: string(plan.Status)})
	if execErr != nil {
		logger.L.Error("manager.plan_failed", "plan_id", plan.ID, "error", execErr)
		return execErr
	}
	logger.L.Info("manager.plan_completed", "plan_id", plan.ID, "cost_usd", plan.TotalCostUSD)
	return nil
}

// GetPlan returns a persisted plan (with subtasks) by ID, or nil if absent.
func (m *SessionManager) GetPlan(planID int64) (*analysis.TaskPlan, error) {
	return analysis.LoadPlan(m.store, planID)
}

// projectPath resolves a configured project name to its filesystem path.
// InstallProtocol writes the developer-session protocol into project's folder
// without generating or touching a roadmap — the retrofit path for a project
// that predates it, or one whose queue was written by hand. Returns the files
// actually created; already-present ones are left alone, so calling it twice is
// a no-op and a project's own edits are never clobbered.
//
// The queue file is taken from the project's first task_source session, since
// that is the file the protocol has to tell the session to read.
func (m *SessionManager) InstallProtocol(project string) ([]string, error) {
	path, err := m.projectPath(project)
	if err != nil {
		return nil, err
	}
	files, err := analysis.WriteProtocolFiles(path, analysis.ProtocolParams{
		QueueFile:  m.projectQueueFile(project),
		MainBranch: gitutil.MainBranch(context.Background(), path),
		Gates:      m.projectGates(project),
	})
	if err != nil {
		return nil, err
	}
	logger.L.Info("manager.protocol_installed", "project", project, "files", strings.Join(files, ", "))
	return files, nil
}

// projectQueueFile returns the task_source of project's first queue-driven
// session, or "" to let ProtocolParams fall back to its default.
func (m *SessionManager) projectQueueFile(project string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.cfg.Projects {
		if m.cfg.Projects[i].Name != project {
			continue
		}
		for _, s := range m.cfg.Projects[i].Sessions {
			if ts := strings.TrimSpace(s.TaskSource); ts != "" {
				return ts
			}
		}
	}
	return ""
}

// projectGates returns project's configured blocking check commands, or nil.
// Used to render the project's real gate into the installed protocol instead
// of a language-agnostic description of one.
func (m *SessionManager) projectGates(project string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.cfg.Projects {
		if m.cfg.Projects[i].Name == project {
			return append([]string(nil), m.cfg.Projects[i].Gates...)
		}
	}
	return nil
}

func (m *SessionManager) projectPath(project string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.cfg.Projects {
		if m.cfg.Projects[i].Name == project {
			return m.cfg.Projects[i].Path, nil
		}
	}
	return "", fmt.Errorf("project %q not found", project)
}

// ---- Mixed programming (MIXED-TASKS.md MP-05) ----

// RegisterMixedBrief makes brief available to DispatchMixedTask under id.
// MP-06 (automatic brief generation) will call this once wired; until then,
// callers (tests, manual dispatch) register briefs directly.
func (m *SessionManager) RegisterMixedBrief(id string, brief worker.Brief) {
	brief.ID = id
	m.mixedMu.Lock()
	m.mixedBriefs[id] = brief
	m.mixedMu.Unlock()
}

func (m *SessionManager) findProjectConfig(project string) *config.ProjectConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.cfg.Projects {
		if m.cfg.Projects[i].Name == project {
			return &m.cfg.Projects[i]
		}
	}
	return nil
}

func (m *SessionManager) findWorkerConfig(name string) *config.WorkerConfig {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.cfg.Workers {
		if m.cfg.Workers[i].Name == name {
			return &m.cfg.Workers[i]
		}
	}
	return nil
}

// DispatchMixedTask runs the mixed-programming round loop (brief -> patches
// -> apply -> gates, MP-02..MP-04) for briefID against workerName, in project.
// Blocks until the task reaches a terminal state (done or needs_human) or is
// cancelled via CancelMixedTask; progress is flushed to disk and emitted as
// worker:round/patch/gate/done so callers can poll GetMixedRounds instead of
// waiting on this call alone.
func (m *SessionManager) DispatchMixedTask(project, briefID, workerName string) (*worker.MixedTask, error) {
	pcfg := m.findProjectConfig(project)
	if pcfg == nil {
		return nil, fmt.Errorf("mixed task: project %q not found", project)
	}
	if !pcfg.MixedProgramming {
		return nil, fmt.Errorf("mixed task: project %q has mixed_programming disabled", project)
	}
	wcfg := m.findWorkerConfig(workerName)
	if wcfg == nil {
		return nil, fmt.Errorf("mixed task: worker %q not configured", workerName)
	}

	m.mixedMu.Lock()
	brief, ok := m.mixedBriefs[briefID]
	m.mixedMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("mixed task: brief %q not registered", briefID)
	}

	apiKey, err := worker.ResolveAPIKey(*wcfg)
	if err != nil {
		return nil, err
	}

	taskID := project + "/" + briefID + "/" + workerName
	task, err := m.taskStore.Load(taskID)
	if err != nil {
		return nil, fmt.Errorf("mixed task: load state: %w", err)
	}
	if task == nil {
		task = &worker.MixedTask{
			ID: taskID, Project: project, BriefID: briefID, WorkerName: workerName,
			MaxRounds: pcfg.MixedMaxRounds,
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	m.mixedMu.Lock()
	m.mixedTasks[taskID] = cancel
	m.mixedMu.Unlock()
	defer func() {
		m.mixedMu.Lock()
		delete(m.mixedTasks, taskID)
		m.mixedMu.Unlock()
	}()

	orch := &worker.RoundOrchestrator{
		Client:      worker.NewClient(*wcfg, apiKey),
		Dialogue:    m.workerStore,
		Tasks:       m.taskStore,
		Emitter:     m.emitter,
		Gates:       pcfg.Gates,
		MaxRounds:   pcfg.MixedMaxRounds,
		WorktreeDir: m.worktreeDir,
	}

	logger.L.Info("manager.mixed_dispatch", "task_id", taskID, "worker", workerName)
	err = orch.RunTask(ctx, pcfg.Path, task, brief)
	if err != nil {
		logger.L.Error("manager.mixed_failed", "task_id", taskID, "error", err)
	} else {
		logger.L.Info("manager.mixed_done", "task_id", taskID, "status", task.Status)
	}
	return task, err
}

// GetMixedRounds returns persisted mixed-programming task state for project.
func (m *SessionManager) GetMixedRounds(project string) ([]*worker.MixedTask, error) {
	return m.taskStore.ListForProject(project)
}

// GetMixedQuality aggregates the project's persisted mixed tasks into the
// per-worker comparative quality report shown by the UI (MP-08).
func (m *SessionManager) GetMixedQuality(project string) ([]worker.ModelQuality, error) {
	tasks, err := m.taskStore.ListForProject(project)
	if err != nil {
		return nil, err
	}
	return worker.BuildQualityReport(tasks), nil
}

// CancelMixedTask cancels a running mixed-programming task by ID (the same ID
// returned in MixedTask.ID / DispatchMixedTask's result). Returns an error if
// the task is not currently running.
func (m *SessionManager) CancelMixedTask(id string) error {
	m.mixedMu.Lock()
	cancel, ok := m.mixedTasks[id]
	m.mixedMu.Unlock()
	if !ok {
		return fmt.Errorf("mixed task %q is not running", id)
	}
	cancel()
	return nil
}
