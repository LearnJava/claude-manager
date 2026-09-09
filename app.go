package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/config"
	"claude-manager/internal/control"
	"claude-manager/internal/experience"
	"claude-manager/internal/gitutil"
	"claude-manager/internal/logger"
	"claude-manager/internal/optimization"
	"claude-manager/internal/permission"
	"claude-manager/internal/session"
	"claude-manager/internal/store"
	"claude-manager/internal/worker"

	toast "git.sr.ht/~jackmordaunt/go-toast/v2"
	"github.com/getlantern/systray"
	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// toastAppID identifies this app in the Windows Action Centre.
const toastAppID = "Claude Session Manager"

// App is the Wails application struct. All exported methods become callable
// from the frontend via auto-generated JS bindings.
type App struct {
	ctx          context.Context
	cfg          *config.AppConfig
	cfgPath      string
	store        *store.Store
	manager      *session.SessionManager
	wailsEmitter *control.WailsEmitter
	ctrlServer   *control.Server
	closeLog     func() // shuts down the file logger on exit
	trayEnabled  bool   // set by main() before wails.Run; see main.go for why
}

// NewApp creates a new App with the default config path.
func NewApp() *App {
	return &App{
		cfgPath: config.DefaultConfigPath(),
	}
}

// startup is called by Wails when the application starts.
func (a *App) startup(ctx context.Context) {
	a.ctx = ctx

	// Initialise the file logger before anything else so every subsequent
	// event is captured. Log dir lives next to the config file.
	logDir := filepath.Join(filepath.Dir(a.cfgPath), "logs")
	closeLog, err := logger.Init(logDir)
	if err != nil {
		log.Printf("logger init error: %v", err) // fallback to stderr
		closeLog = func() {}
	}
	a.closeLog = closeLog

	logger.L.Info("startup", "cfg", a.cfgPath)

	cfg, err := config.Load(a.cfgPath)
	if err != nil {
		logger.L.Error("config.load", "error", err)
		cfg, _ = config.Load("") // fall back to defaults
	} else {
		logger.L.Info("config.loaded",
			"projects", len(cfg.Projects),
			"claude_path", cfg.Settings.ClaudePath,
			"retry_delay", cfg.Settings.DefaultRetryDelay,
			"rate_limit_pause", cfg.Settings.RateLimitPause,
			"auto_model_routing", cfg.Optimization.AutoModelRouting,
		)
	}
	a.cfg = cfg

	dbPath := defaultDBPath(a.cfgPath)
	logger.L.Info("store.open", "path", dbPath)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		logger.L.Error("store.mkdir", "error", err)
	}
	st, err := store.New(dbPath)
	if err != nil {
		logger.L.Error("store.open", "error", err)
	} else {
		a.store = st
		// Drop persisted logs whose runs are older than the retention window.
		if days := cfg.Settings.LogRetentionDays; days > 0 {
			if err := st.DeleteOldLogs(days); err != nil {
				logger.L.Error("store.cleanup", "error", err)
			} else {
				logger.L.Info("store.cleanup", "retention_days", days)
			}
		}
	}

	// Build the emitter chain. With the control-plane enabled (the default) we
	// fan-out to both Wails and the ControlEmitter so the GUI and the headless
	// bridge receive all events.
	a.wailsEmitter = control.NewWailsEmitter()
	a.wailsEmitter.SetContext(ctx)

	var sessionEmitter session.Emitter = a.wailsEmitter
	var controlEmitter *control.ControlEmitter
	if control.Enabled() {
		controlEmitter = control.NewControlEmitter(200)
		sessionEmitter = control.NewMultiEmitter(a.wailsEmitter, controlEmitter)
	}

	a.manager = session.NewSessionManager(cfg, a.cfgPath, a.store, sessionEmitter)

	// Wire the experience-layer indexer (LEARN-TASKS.md LN-03): finishRun
	// calls this after every run, but only actually opens a transcript when
	// [optimization] experience_tracking is on (checked live against
	// whatever config SetConfig last installed, not just at startup).
	if a.store != nil {
		st := a.store
		a.manager.SetActionIndexer(func(project, sessionName string, runID int64, cliSessionID, projectPath, taskPtr string) error {
			return experience.IngestRun(st, project, sessionName, runID, cliSessionID, projectPath, taskPtr)
		})
	}

	// Wire the context primer (LEARN-TASKS.md LN-05): only ever called for a
	// session with ContextPrimer=true (checked in Session.initialPromptText),
	// so this is safe to wire unconditionally even for projects that never
	// opt in.
	{
		st := a.store
		a.manager.SetPrimerBuilder(func(project, sessionName, projectPath, taskDesc string, gates []string) string {
			return experience.BuildPrimer(experience.PrimerInput{
				Project:     project,
				Session:     sessionName,
				ProjectPath: projectPath,
				TaskDesc:    taskDesc,
				Gates:       gates,
				Store:       st,
			})
		})
	}

	// Wire the project journal (LEARN-TASKS.md LN-06): finishRun only ever
	// calls this for a completed run whose project has Journal=true, so it's
	// safe to wire unconditionally even for projects that never opt in.
	a.manager.SetJournalWriter(func(projectPath string, commit bool, entry session.JournalEntry) error {
		return experience.AppendEntry(projectPath, commit, experience.Entry{
			Date:      entry.Date,
			TaskPtr:   entry.TaskPtr,
			Done:      entry.Done,
			Surprises: entry.Surprises,
			Avoid:     entry.Avoid,
		})
	})

	// Wire the context-handoff distiller (LEARN-TASKS.md LN-15): only ever
	// called for a session with ContextHandoff=true whose own context
	// utilization just crossed the restart threshold (checked in
	// Session.checkContextRestart), so this is safe to wire unconditionally
	// even for sessions that never opt in.
	a.manager.SetHandoffBuilder(func(project, sessionName, projectPath, cliSessionID, taskDesc string, todos []string) (string, error) {
		in := experience.BuildHandoffInput(experience.TranscriptsRoot(), projectPath, cliSessionID, taskDesc, todos)
		res, err := analysis.GenerateHandoff(context.Background(), projectPath, in, analysis.AnalysisConfig{
			ClaudePath: a.cfg.Settings.ClaudePath,
		})
		if err != nil {
			return "", err
		}
		return experience.RenderHandoffPrompt(res), nil
	})

	// Start the control-plane server (no-op when CM_CONTROL disables it).
	if controlEmitter != nil {
		srv, err := control.StartFromEnv(ctx, a.manager, a, controlEmitter)
		if err != nil {
			logger.L.Error("control.start", "error", err)
		} else {
			a.ctrlServer = srv
		}
	}

	// Register the app with Windows so toast notifications can target it.
	// SetAppData is a best-effort call: it writes to the registry on Windows
	// and is a no-op on other platforms.
	if err := toast.SetAppData(toast.AppData{
		AppID: toastAppID,
	}); err != nil {
		logger.L.Warn("toast.register", "error", err)
		runtime.LogWarning(ctx, "toast app registration failed: "+err.Error())
	}

	logger.L.Info("startup.complete")
	runtime.LogInfo(ctx, "Claude Session Manager started")
}

// shutdown is called by Wails when the application is about to quit.
func (a *App) shutdown(ctx context.Context) {
	logger.L.Info("shutdown.begin")
	if a.manager != nil {
		a.manager.Shutdown()
	}
	if a.store != nil {
		_ = a.store.Close()
	}
	if a.ctrlServer != nil {
		// Drop the discovery file here as well as in the server goroutine:
		// shutdown does not necessarily cancel the startup context, and a
		// stale control.json would send the next cm-mcp at a dead port.
		control.RemoveEndpoint()
	}
	if a.trayEnabled {
		systray.Quit()
	}
	logger.L.Info("shutdown.complete")
	runtime.LogInfo(ctx, "Claude Session Manager shutting down")
	if a.closeLog != nil {
		a.closeLog()
	}
}

// defaultDBPath stores the SQLite DB next to the config file.
func defaultDBPath(cfgPath string) string {
	dir := filepath.Dir(cfgPath)
	if dir == "" || dir == "." {
		home, err := os.UserHomeDir()
		if err == nil {
			dir = filepath.Join(home, ".claude-manager")
		}
	}
	return filepath.Join(dir, "history.db")
}

// ---- Config bindings ----

// GetConfig returns the current AppConfig to the frontend (read-only view).
func (a *App) GetConfig() *config.AppConfig {
	return a.cfg
}

// GetProjects returns the list of configured projects.
func (a *App) GetProjects() []config.ProjectConfig {
	if a.cfg == nil {
		return nil
	}
	return a.cfg.Projects
}

// GetAutoModelRouting reports whether auto_model_routing is enabled in config.
func (a *App) GetAutoModelRouting() bool {
	if a.cfg == nil {
		return false
	}
	return a.cfg.Optimization.AutoModelRouting
}

// GetModelRecommendation runs a lightweight pre-flight analysis on the
// session's configured prompt and returns a model/effort recommendation.
// Returns nil (no error) when auto_model_routing is disabled or the session
// has no prompt configured.
func (a *App) GetModelRecommendation(project, name string) (*optimization.ModelRecommendation, error) {
	if a.cfg == nil || !a.cfg.Optimization.AutoModelRouting {
		return nil, nil
	}
	var projPath, prompt string
	for i := range a.cfg.Projects {
		p := &a.cfg.Projects[i]
		if p.Name != project {
			continue
		}
		projPath = p.Path
		for j := range p.Sessions {
			if p.Sessions[j].Name == name {
				prompt = p.Sessions[j].Prompt
				break
			}
		}
		break
	}
	if prompt == "" {
		return nil, nil
	}
	result, err := analysis.RunAnalysis(a.ctx, projPath, prompt, analysis.AnalysisConfig{
		ClaudePath:   a.cfg.Settings.ClaudePath,
		Model:        a.cfg.Settings.PreflightModel,
		MaxBudgetUSD: a.cfg.Settings.PreflightMaxBudget,
	})
	if err != nil {
		return nil, fmt.Errorf("model routing analysis: %w", err)
	}
	router := optimization.NewModelRouter(&a.cfg.Optimization)
	if a.store != nil {
		router.SetOutcomeProvider(a.store)
	}
	rec := router.Route(project, result.RecommendedModel, result.RecommendedEffort,
		result.Feasibility.EstimatedComplexity)
	return &rec, nil
}

// StartSessionWithModel starts a session overriding the configured model and effort.
// Empty strings fall back to the values in config.
//
// The override is also remembered as the session's stored default (see
// rememberSessionModel) — "start it with what I started it with last time" is
// what the sidebar and the next app launch should show, instead of silently
// snapping back to whatever the config said before the override.
func (a *App) StartSessionWithModel(project, name, model, effort string) error {
	if err := a.manager.StartSessionWithOverride(project, name, model, effort); err != nil {
		return err
	}
	a.rememberSessionModel(project, name, model, effort)
	return nil
}

// ---- Pre-flight plan bindings (PLAN.md section 17) ----

// RunPreflight runs the analyst on an ad-hoc task and returns the persisted
// draft plan for review in PlanReview.svelte.
func (a *App) RunPreflight(project, task string) (*analysis.TaskPlan, error) {
	return a.manager.RunPreflight(project, task)
}

// ApprovePlan persists an (edited) plan as approved. The returned plan carries
// the store ID assigned on first save — use it for the ExecutePlan call.
func (a *App) ApprovePlan(plan analysis.TaskPlan) (*analysis.TaskPlan, error) {
	return a.manager.ApprovePlan(&plan)
}

// ExecutePlan executes a persisted plan by ID. Blocks until the plan finishes;
// subtask progress is flushed to the store and can be polled via GetPlan.
func (a *App) ExecutePlan(planID int64) error {
	return a.manager.ExecutePlan(planID)
}

// GetPlan returns a persisted plan with its subtasks, or null if absent.
func (a *App) GetPlan(planID int64) (*analysis.TaskPlan, error) {
	return a.manager.GetPlan(planID)
}

// GenerateRoadmap decomposes a whole project idea into a durable backlog
// (a draft plan with Kind="roadmap") for review in PlanReview.svelte
// (mode="roadmap"). model may be empty to use the default (Opus).
func (a *App) GenerateRoadmap(project, idea, model string) (*analysis.TaskPlan, error) {
	return a.manager.GenerateRoadmap(project, idea, model)
}

// GetLatestDraftRoadmap returns the most recently generated but unapproved
// roadmap plan for a project, or null. Lets the UI recover a plan whose
// GenerateRoadmap call finished on the backend but never made it back to the
// frontend (e.g. a page reload while the request was in flight), without
// paying for a second analyst run.
func (a *App) GetLatestDraftRoadmap(project string) (*analysis.TaskPlan, error) {
	return a.manager.GetLatestDraftRoadmap(project)
}

// ApproveRoadmap materializes an approved roadmap plan into
// <project>/ROADMAP.md + <project>/STATUS-P1.md and bootstraps (or updates)
// a "P1" session pointed at the result, reusing the same GetConfig-mutate-
// UpdateConfig round-trip every other project/session edit already goes
// through. Returns the same "already exists" error as ApproveRoadmapFiles
// when overwrite is false and the files are already present.
func (a *App) ApproveRoadmap(planID int64, overwrite bool) (*analysis.TaskPlan, error) {
	plan, _, _, err := a.manager.ApproveRoadmapFiles(planID, overwrite)
	if err != nil {
		return nil, err
	}
	if a.cfg == nil {
		return plan, nil
	}
	cfg := *a.cfg
	cfg.Projects = append([]config.ProjectConfig(nil), a.cfg.Projects...)
	upsertP1Session(&cfg, plan.Project)
	if err := a.UpdateConfig(cfg); err != nil {
		return nil, err
	}
	return plan, nil
}

// upsertP1Session points project's "P1" session at the freshly written
// STATUS-P1.md, creating it (with Sonnet defaults) if absent. If P1 already
// exists, only the task_source-related fields are forced so a user's manual
// model/prompt edits survive re-generating the roadmap. Operates on cloned
// slices so it never mutates the caller's existing config in place.
func upsertP1Session(cfg *config.AppConfig, project string) {
	for pi := range cfg.Projects {
		if cfg.Projects[pi].Name != project {
			continue
		}
		sessions := append([]config.SessionConfig(nil), cfg.Projects[pi].Sessions...)
		for si := range sessions {
			if sessions[si].Name == "P1" {
				sessions[si].TaskSource = "STATUS-P1.md"
				sessions[si].StopWhenNoTasks = true
				sessions[si].AutoRestart = true
				sessions[si].UseWorktree = false
				cfg.Projects[pi].Sessions = sessions
				return
			}
		}
		permissionMode := cfg.Projects[pi].DefaultPermissionMode
		if permissionMode == "" {
			permissionMode = "bypassPermissions"
		}
		sessions = append(sessions, config.SessionConfig{
			Name:            "P1",
			Model:           "sonnet",
			Effort:          "high",
			PermissionMode:  permissionMode,
			TaskSource:      "STATUS-P1.md",
			StopWhenNoTasks: true,
			AutoRestart:     true,
			// Off deliberately, and forced off above even for a pre-existing P1:
			// the protocol installed into the project (analysis.WriteProtocolFiles)
			// owns the worktree, keeping one persistent slot per developer whose
			// branch survives an interrupted run. A bare --worktree would add a
			// second, anonymous one made fresh from HEAD on every process start,
			// and any work not yet merged would be invisible to the next session.
			UseWorktree:     false,
			Prompt:          analysis.DefaultP1SessionPrompt,
		})
		cfg.Projects[pi].Sessions = sessions
		return
	}
}

// InstallSessionProtocol writes the developer-session protocol
// (docs/git-workflow.md, scripts/worktree-pool.sh, the /cm-task-start and
// /cm-task-finish skills) into an existing project and gitignores the worktree
// pool. Returns the files actually created — existing ones are left untouched,
// so a project that already has its own protocol keeps it and a second call
// creates nothing.
//
// ApproveRoadmap installs the same files for a project whose roadmap this app
// generated; this is the retrofit path for everything else.
func (a *App) InstallSessionProtocol(project string) ([]string, error) {
	return a.manager.InstallProtocol(project)
}

// HasClaudeMd reports whether projectPath already contains a CLAUDE.md.
// Path-based rather than project-name-based: the sidebar already has each
// project's path from GetProjects and can check without a config round-trip.
func (a *App) HasClaudeMd(projectPath string) bool {
	if strings.TrimSpace(projectPath) == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(projectPath, "CLAUDE.md"))
	return err == nil
}

// GenerateClaudeMdSession bootstraps (or re-points) an "Init" session on
// project with analysis.ClaudeMdInitPrompt and starts it immediately, mirror-
// ing the ApproveRoadmap/upsertP1Session pattern above: a canned prompt in a
// named, reusable session rather than a one-off CLI call, since `/init` itself
// only runs inside an interactive session. Re-running this later (e.g. via the
// "Init" session's own ▶) asks Claude to refine the existing CLAUDE.md instead
// of writing a fresh one - the prompt handles both cases.
func (a *App) GenerateClaudeMdSession(project string) error {
	if a.cfg == nil {
		return fmt.Errorf("no config loaded")
	}
	cfg := *a.cfg
	cfg.Projects = append([]config.ProjectConfig(nil), a.cfg.Projects...)
	if !upsertInitSession(&cfg, project) {
		return fmt.Errorf("project %q not found", project)
	}
	if err := a.UpdateConfig(cfg); err != nil {
		return err
	}
	return a.manager.StartSession(project, "Init")
}

// upsertInitSession points project's "Init" session at
// analysis.ClaudeMdInitPrompt, creating it (with Sonnet defaults) if absent.
// Reports whether project was found. Operates on cloned slices so it never
// mutates the caller's existing config in place.
func upsertInitSession(cfg *config.AppConfig, project string) bool {
	for pi := range cfg.Projects {
		if cfg.Projects[pi].Name != project {
			continue
		}
		sessions := append([]config.SessionConfig(nil), cfg.Projects[pi].Sessions...)
		for si := range sessions {
			if sessions[si].Name == "Init" {
				sessions[si].Prompt = analysis.ClaudeMdInitPrompt
				cfg.Projects[pi].Sessions = sessions
				return true
			}
		}
		permissionMode := cfg.Projects[pi].DefaultPermissionMode
		if permissionMode == "" {
			permissionMode = "bypassPermissions"
		}
		sessions = append(sessions, config.SessionConfig{
			Name:           "Init",
			Model:          "sonnet",
			Effort:         "high",
			PermissionMode: permissionMode,
			Prompt:         analysis.ClaudeMdInitPrompt,
		})
		cfg.Projects[pi].Sessions = sessions
		return true
	}
	return false
}

// StartAdHocChatSession bootstraps (or reuses) a plain interactive "Chat"
// session on project and starts it — for when the user just wants to talk to
// Claude and hand it instructions directly, without pre-configuring a session
// or writing a task_source file first. Mirrors the GenerateClaudeMdSession/
// upsertInitSession pattern, but with no canned prompt and no task_source: an
// empty Prompt means runOnce sends nothing on launch (see initialPromptText),
// so the CLI process comes up and just waits on stdin for the user's first
// message via SendMessage.
func (a *App) StartAdHocChatSession(project string) error {
	if a.cfg == nil {
		return fmt.Errorf("no config loaded")
	}
	cfg := *a.cfg
	cfg.Projects = append([]config.ProjectConfig(nil), a.cfg.Projects...)
	if !upsertChatSession(&cfg, project) {
		return fmt.Errorf("project %q not found", project)
	}
	if err := a.UpdateConfig(cfg); err != nil {
		return err
	}
	return a.manager.StartSession(project, "Chat")
}

// upsertChatSession ensures project has a "Chat" SessionConfig, creating a
// bare one (no prompt, no task_source, no auto_restart — Model/Effort/
// PermissionMode fall back to the usual applySessionDefaults) if absent.
// An existing "Chat" session is left untouched, so a user's manual edits
// (model, effort, a prompt they added later) survive repeated clicks.
// Reports whether project was found. Operates on cloned slices so it never
// mutates the caller's existing config in place.
func upsertChatSession(cfg *config.AppConfig, project string) bool {
	for pi := range cfg.Projects {
		if cfg.Projects[pi].Name != project {
			continue
		}
		for _, sc := range cfg.Projects[pi].Sessions {
			if sc.Name == "Chat" {
				return true
			}
		}
		sessions := append([]config.SessionConfig(nil), cfg.Projects[pi].Sessions...)
		sessions = append(sessions, config.SessionConfig{Name: "Chat"})
		cfg.Projects[pi].Sessions = sessions
		return true
	}
	return false
}

// ---- Mixed programming bindings (MIXED-TASKS.md MP-05) ----

// RegisterMixedBrief registers a self-contained brief under id so it can be
// dispatched to a worker (MP-06 generates these; the GUI also lets an operator
// enter one by hand). Returns the id so the frontend can chain DispatchMixedTask.
func (a *App) RegisterMixedBrief(id, task, systemPrompt string) string {
	a.manager.RegisterMixedBrief(id, worker.Brief{Task: task, SystemPrompt: systemPrompt})
	return id
}

// DispatchMixedTask runs the mixed-programming round loop for briefID against
// workerName in project. Blocks until the task reaches done/needs_human, or
// is cancelled via CancelMixedTask; progress can be polled via GetMixedRounds.
func (a *App) DispatchMixedTask(project, briefID, workerName string) (*worker.MixedTask, error) {
	return a.manager.DispatchMixedTask(project, briefID, workerName)
}

// GetMixedRounds returns persisted mixed-programming task state for project.
func (a *App) GetMixedRounds(project string) ([]*worker.MixedTask, error) {
	return a.manager.GetMixedRounds(project)
}

// GetMixedQuality returns the per-worker comparative quality report for
// project, aggregated from persisted mixed-task state (MP-08).
func (a *App) GetMixedQuality(project string) ([]worker.ModelQuality, error) {
	return a.manager.GetMixedQuality(project)
}

// CancelMixedTask cancels a running mixed-programming task by ID.
func (a *App) CancelMixedTask(id string) error {
	return a.manager.CancelMixedTask(id)
}

// ---- Session lifecycle bindings ----

func (a *App) StartSession(project, name string) error {
	return a.manager.StartSession(project, name)
}

func (a *App) StopSession(id string, soft bool) error {
	return a.manager.StopSession(id, soft)
}

func (a *App) StopAll() {
	a.manager.StopAll()
}

func (a *App) RestartSession(id string) error {
	return a.manager.RestartSession(id)
}

func (a *App) ResumeSession(id string) error {
	return a.manager.ResumeSession(id)
}

// ClearSessionState deletes the crash-recovery state for project/session so
// the next StartSession opens a fresh conversation (equivalent to --new).
func (a *App) ClearSessionState(project, name string) {
	if a.manager != nil {
		a.manager.ClearSessionState(project, name)
	}
}

// GetSessionState returns the persisted crash-recovery state (session_id +
// started_at) for project/session, or nil if no state file exists.
func (a *App) GetSessionState(project, name string) *session.PersistedState {
	if a.manager == nil {
		return nil
	}
	return a.manager.GetSessionState(project, name)
}

// InitGitRepo makes projectPath usable by a `use_worktree = true` session:
// it runs `git init` if the folder isn't a repo yet, and creates an initial
// commit if HEAD can't be resolved yet (a bare `git init` alone leaves an
// unborn branch that `--worktree` cannot branch from). Call StartSession or
// RestartSession again afterwards to retry.
func (a *App) InitGitRepo(projectPath string) error {
	return gitutil.EnsureRepoWithCommit(context.Background(), projectPath)
}

// StartProject launches every configured session in project. When
// experience tracking is on and a store is configured, sessions launch in
// cache-affinity order (LEARN-TASKS.md LN-14) instead of plain config order:
// grouped by launch model first (a model switch resets the shared
// system-prompt cache, see "Token Optimization" above), then within a group
// by descending overlap of the files the session's last run touched. With
// tracking off, no store, or a project with no run history yet,
// projectStartOrder returns nil and this falls back to plain
// SessionManager.StartProject unchanged (LEARN-TASKS.md invariant 6).
func (a *App) StartProject(project string) error {
	order := a.projectStartOrder(project)
	if len(order) == 0 {
		return a.manager.StartProject(project)
	}
	for _, name := range order {
		if err := a.manager.StartSession(project, name); err != nil {
			return err
		}
	}
	return nil
}

// projectStartOrder computes the LN-14 cache-affinity launch order for
// project's sessions — see StartProject above. Returns nil when there is
// nothing to reorder by (tracking off, no store) so the caller's fallback
// path runs instead.
func (a *App) projectStartOrder(project string) []string {
	if a.cfg == nil || !a.cfg.Optimization.ExperienceTracking || a.store == nil {
		return nil
	}
	var sessions []config.SessionConfig
	for i := range a.cfg.Projects {
		if a.cfg.Projects[i].Name == project {
			sessions = a.cfg.Projects[i].Sessions
			break
		}
	}
	if len(sessions) == 0 {
		return nil
	}
	inputs := make([]experience.SessionAffinityInput, len(sessions))
	for i, s := range sessions {
		inputs[i] = experience.SessionAffinityInput{
			ID:    s.Name,
			Model: s.Model,
			Files: experience.LastRunFiles(a.store, project, s.Name),
		}
	}
	return experience.OrderByCacheAffinity(inputs)
}

func (a *App) StopProject(project string) error {
	return a.manager.StopProject(project)
}

// ---- Bidirectional streaming + permissions ----

func (a *App) SendMessage(id, message string) error {
	return a.manager.SendMessage(id, message)
}

// SendMessageWithImages writes a user message with optional image
// attachments (pasted from the clipboard) to a running session's stdin.
func (a *App) SendMessageWithImages(id, message string, images []session.ImageAttachment) error {
	return a.manager.SendMessageWithImages(id, message, images)
}

func (a *App) RespondPermission(id, requestID, decision string) error {
	return a.manager.RespondPermission(id, requestID, decision)
}

func (a *App) GetPendingPermissions() []permission.PermissionRequest {
	return a.manager.GetPendingPermissions()
}

func (a *App) AnswerQuestion(id, questionID, answer string) error {
	return a.manager.AnswerQuestion(id, questionID, answer)
}

func (a *App) GetPendingQuestions() []session.QuestionInfo {
	return a.manager.GetPendingQuestions()
}

// SetSessionModel switches a session's model (see CLAUDE.md "Live Model
// Switching") and remembers it as that session's stored default, so the choice
// survives the session stopping and the app restarting.
//
// Persisting happens first: SessionManager.SetSessionModel mutates the very
// SessionConfig this reads (findConfig hands out a pointer into the live
// config) for a session that was never started, which would make the
// "already the stored value" check below pass for a value that is not on disk
// yet.
func (a *App) SetSessionModel(id, model string) error {
	if project, name, ok := splitSessionID(id); ok {
		a.rememberSessionModel(project, name, model, "")
	}
	return a.manager.SetSessionModel(id, model)
}

// splitSessionID splits the "project/session" id used everywhere in this app.
func splitSessionID(id string) (project, name string, ok bool) {
	parts := strings.SplitN(id, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// rememberSessionModel writes model (and effort, when non-empty) into the
// session's persisted config through the same GetConfig-mutate-UpdateConfig
// round-trip every other config edit uses, so the model a session was last
// run with becomes what the sidebar shows and what the next start uses.
//
// A failure to persist is logged, not returned: the model switch / session
// start it follows has already happened, and reporting an error for it would
// tell the user the thing they just watched happen did not.
func (a *App) rememberSessionModel(project, name, model, effort string) {
	model = strings.TrimSpace(model)
	effort = strings.TrimSpace(effort)
	if a.cfg == nil || model == "" {
		return
	}

	cfg := *a.cfg
	cfg.Projects = append([]config.ProjectConfig(nil), a.cfg.Projects...)
	if !setSessionModelInConfig(&cfg, project, name, model, effort) {
		return
	}
	if err := a.UpdateConfig(cfg); err != nil {
		logger.L.Warn("app.remember_session_model",
			"project", project, "session", name, "model", model, "error", err)
		return
	}
	logger.L.Info("app.remember_session_model",
		"project", project, "session", name, "model", model, "effort", effort)
}

// setSessionModelInConfig sets the model/effort of project/session in cfg,
// operating on cloned slices so the caller's config is never mutated in place
// (same rule as upsertChatSession above). Reports whether anything changed —
// false when the session is unknown or already carries these values, so an
// unchanged config is never rewritten to disk.
func setSessionModelInConfig(cfg *config.AppConfig, project, name, model, effort string) bool {
	for pi := range cfg.Projects {
		if cfg.Projects[pi].Name != project {
			continue
		}
		for si, sc := range cfg.Projects[pi].Sessions {
			if sc.Name != name {
				continue
			}
			if sc.Model == model && (effort == "" || sc.Effort == effort) {
				return false
			}
			sessions := append([]config.SessionConfig(nil), cfg.Projects[pi].Sessions...)
			sessions[si].Model = model
			if effort != "" {
				sessions[si].Effort = effort
			}
			cfg.Projects[pi].Sessions = sessions
			return true
		}
		return false
	}
	return false
}

// ---- State / history / metrics ----

func (a *App) GetAllSessions() []session.SessionState {
	return a.manager.GetAllSessions()
}

func (a *App) GetSessionLog(id string, offset, limit int) ([]*store.LogEntry, error) {
	return a.manager.GetSessionLog(id, offset, limit)
}

func (a *App) GetHistory(project string, limit int) ([]*store.SessionRun, error) {
	return a.manager.GetHistory(project, limit)
}

func (a *App) GetSessionMetrics(id string) (session.SessionMetrics, error) {
	return a.manager.GetSessionMetrics(id)
}

func (a *App) GetDailyCost(date string) (float64, error) {
	return a.manager.GetDailyCost(date)
}

func (a *App) GetDailyTokens(date string) (session.DailyTokens, error) {
	return a.manager.GetDailyTokens(date)
}

func (a *App) GetProjectCost(project string, days int) (float64, error) {
	return a.manager.GetProjectCost(project, days)
}

func (a *App) GetProjectTokens(project string, days int) (session.DailyTokens, error) {
	return a.manager.GetProjectTokens(project, days)
}

func (a *App) GetRateLimitStatus() *session.RateLimitInfo {
	return a.manager.GetRateLimitStatus()
}

// ---- Settings / config persistence ----

// UpdateConfig persists the config across its layers and re-applies defaults so
// subsequent reads see normalised values. Per-project settings (sessions, gates,
// the mixed-programming opt-in) are written into <project>/.claude-manager/ so
// they travel with the repo; the global file keeps app-wide settings, the
// project registry (name+path), and the shared worker presets. Workers stay in
// the global file — it lives in the home dir and is never committed to a repo.
func (a *App) UpdateConfig(cfg config.AppConfig) error {
	global := cfg
	global.Projects = make([]config.ProjectConfig, len(cfg.Projects))
	for i, p := range cfg.Projects {
		if p.Path != "" && isDir(p.Path) {
			// Fold this project's context into its folder; the global entry
			// shrinks to a registry pointer that the overlay is merged onto.
			if err := config.SaveProjectOverlay(p.Path, p, nil); err != nil {
				return err
			}
			global.Projects[i] = config.ProjectConfig{Name: p.Name, Path: p.Path}
		} else {
			// No writable project folder — keep the settings inline so they
			// are not lost (e.g. a project whose path does not exist yet).
			global.Projects[i] = p
		}
	}

	if err := config.Save(&global, a.cfgPath); err != nil {
		return err
	}
	// Re-read so applyDefaults/validate run on the merged (global + overlay) result.
	reloaded, err := config.Load(a.cfgPath)
	if err != nil {
		return err
	}
	a.cfg = reloaded
	if a.manager != nil {
		a.manager.SetConfig(reloaded)
	}
	return nil
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// PickDirectory opens the native folder picker and returns the chosen path.
// Returns an empty string when the user cancels.
func (a *App) PickDirectory(title string) (string, error) {
	if title == "" {
		title = "Select folder"
	}
	return runtime.OpenDirectoryDialog(a.ctx, runtime.OpenDialogOptions{
		Title: title,
	})
}

// ---- Polish: window / tray / notifications / export / cleanup ----

// ShowMainWindow makes the window visible and unminimised. Useful from the
// system tray, notifications, or keyboard shortcuts.
func (a *App) ShowMainWindow() {
	if a.ctx == nil {
		return
	}
	runtime.WindowShow(a.ctx)
	runtime.WindowUnminimise(a.ctx)
}

// MinimizeToTray hides the main window so that only the tray icon remains.
// With HideWindowOnClose enabled in main.go, closing the window has the same
// effect.
func (a *App) MinimizeToTray() {
	if a.ctx == nil {
		return
	}
	runtime.WindowHide(a.ctx)
}

// onTrayReady builds the system tray icon and menu (Show / Quit). Runs once
// on the dedicated systray goroutine started in main(), for the process
// lifetime — this is what lets the window disappear on close (X) without
// losing all access to a running session.
func (a *App) onTrayReady() {
	systray.SetIcon(trayIconICO)
	systray.SetTooltip("Claude Session Manager")

	mShow := systray.AddMenuItem("Show", "Show the main window")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Stop all sessions and quit")

	go func() {
		defer logger.Recover("app.tray_menu_loop")
		for {
			select {
			case <-mShow.ClickedCh:
				a.ShowMainWindow()
			case <-mQuit.ClickedCh:
				if a.ctx != nil {
					runtime.Quit(a.ctx)
				}
				return
			}
		}
	}()
}

// Notify pushes a native OS notification (Windows toast). Title and body are
// mandatory; an empty body falls back to title-only. Returns an error if the
// underlying COM call fails (e.g. unsupported platform).
func (a *App) Notify(title, body string) error {
	if strings.TrimSpace(title) == "" {
		return fmt.Errorf("notify: title is required")
	}
	n := toast.Notification{
		AppID: toastAppID,
		Title: title,
		Body:  body,
	}
	return n.Push()
}

// ExportLog writes the given log lines to a user-chosen file via the native
// save dialog. The format argument controls extension and rendering: "md",
// "json", or "txt" (default). Returns the chosen path, or an empty string if
// the user cancelled.
func (a *App) ExportLog(sessionID string, entries []store.LogEntry, format string) (string, error) {
	if a.ctx == nil {
		return "", fmt.Errorf("export: app not initialised")
	}
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		format = "txt"
	}

	defaultName := defaultExportFilename(sessionID, format)
	filters := exportFiltersFor(format)

	path, err := runtime.SaveFileDialog(a.ctx, runtime.SaveDialogOptions{
		Title:           "Export session log",
		DefaultFilename: defaultName,
		Filters:         filters,
	})
	if err != nil {
		return "", err
	}
	if path == "" {
		// User cancelled.
		return "", nil
	}

	data, err := store.RenderExport(sessionID, entries, format)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// CleanOldLogs deletes log entries whose runs are older than retentionDays.
// Exposed so the UI can trigger a manual cleanup from settings. Passing 0
// or negative is a no-op.
func (a *App) CleanOldLogs(retentionDays int) error {
	if a.store == nil {
		return fmt.Errorf("cleanup: store unavailable")
	}
	if retentionDays <= 0 {
		return nil
	}
	return a.store.DeleteOldLogs(retentionDays)
}

// projectPath resolves a project name to its configured folder path, or an
// error if the project isn't found (or has no folder set — projects added
// without a path can't have per-project log files).
func (a *App) projectPath(project string) (string, error) {
	if a.cfg == nil {
		return "", fmt.Errorf("no config loaded")
	}
	for i := range a.cfg.Projects {
		if a.cfg.Projects[i].Name == project {
			if a.cfg.Projects[i].Path == "" {
				return "", fmt.Errorf("project %q has no folder configured", project)
			}
			return a.cfg.Projects[i].Path, nil
		}
	}
	return "", fmt.Errorf("project %q not found", project)
}

// GetSessionRoadmap returns the project's roadmap as the TaskPanel "Roadmap"
// tab renders it: every task with done/current/pending status, derived from
// which pointer lines are still in the session's task_source file. Returns
// (nil, nil) when the session has no task source or the project has no
// readable roadmap — the UI shows a placeholder rather than an error, since
// most sessions (Chat, Init, ad-hoc) legitimately have neither.
func (a *App) GetSessionRoadmap(project, sessionName string) (*analysis.RoadmapView, error) {
	path, err := a.projectPath(project)
	if err != nil {
		return nil, err
	}
	taskSource := a.sessionTaskSource(project, sessionName)
	view, err := analysis.ReadRoadmap(path, taskSource)
	if errors.Is(err, analysis.ErrNoRoadmap) {
		return nil, nil
	}
	return view, err
}

// GetRoadmapTaskDetail returns the body of one tasks/NN-*.md file. relPath is
// the link from the roadmap row; ReadRoadmapTaskDetail confines it to the
// project folder.
func (a *App) GetRoadmapTaskDetail(project, relPath string) (string, error) {
	path, err := a.projectPath(project)
	if err != nil {
		return "", err
	}
	return analysis.ReadRoadmapTaskDetail(path, relPath)
}

// GetRoadmapRowDetail returns the long-form text of one roadmap row (its
// `note` column, else the raw row) for roadmaps that keep task descriptions
// inline instead of in per-task files. Fetched per row on expand: a curated
// roadmap can carry kilobytes of notes per task, far too much to ship with
// every tree refresh.
func (a *App) GetRoadmapRowDetail(project, roadmapFile string, line int) (string, error) {
	path, err := a.projectPath(project)
	if err != nil {
		return "", err
	}
	return analysis.ReadRoadmapRowDetail(path, roadmapFile, line)
}

// sessionTaskSource looks up a session's configured task_source, or "" when
// the project/session isn't in the config (a session started before a config
// edit, say).
func (a *App) sessionTaskSource(project, sessionName string) string {
	if a.cfg == nil {
		return ""
	}
	for i := range a.cfg.Projects {
		if a.cfg.Projects[i].Name != project {
			continue
		}
		for _, s := range a.cfg.Projects[i].Sessions {
			if s.Name == sessionName {
				return s.TaskSource
			}
		}
	}
	return ""
}

// GetTopActions aggregates action_signatures for a project over the last
// days days, most frequent signature first — the "Actions" tab table
// (LEARN-TASKS.md LN-03). Requires [optimization] experience_tracking to have
// been on for some runs; with it off, the table is simply empty.
func (a *App) GetTopActions(project string, days int) ([]store.SignatureStat, error) {
	if a.store == nil {
		return nil, fmt.Errorf("no store")
	}
	return a.store.TopSignatures(project, days, 0)
}

// GetActionSamples returns concrete example rows for one (project, sig) pair
// — the "Actions" tab's click-through from an aggregated signature to what
// it actually covers.
func (a *App) GetActionSamples(project, sig string, limit int) ([]store.ActionRow, error) {
	if a.store == nil {
		return nil, fmt.Errorf("no store")
	}
	return a.store.ActionSamples(project, sig, limit)
}

// GetDurationProfile aggregates action_signatures durations for a project —
// median/p90/max/failure-rate/count per normalized command, most
// time-consuming first — the "Timing" tab of the Experience panel
// (LEARN-TASKS.md LN-18). Requires [optimization] experience_tracking to have
// been on for some runs; with it off, the table is simply empty.
func (a *App) GetDurationProfile(project string) ([]experience.SignatureDuration, error) {
	if a.store == nil {
		return nil, fmt.Errorf("no store")
	}
	return experience.DurationProfile(a.store, project)
}

// GetTokenAttribution aggregates action_signatures result sizes for a
// project into per-signature and per-tool estimated-token attribution — the
// "Cost by tool" tab of the Experience panel (LEARN-TASKS.md LN-12). Requires
// [optimization] experience_tracking to have been on for some runs; with it
// off, the report is simply empty. topN<=0 means no limit on the
// per-signature cut.
func (a *App) GetTokenAttribution(project string, topN int) (experience.AttributionReport, error) {
	if a.store == nil {
		return experience.AttributionReport{}, fmt.Errorf("no store")
	}
	return experience.BuildAttributionReport(a.store, project, topN)
}

// GetPermissionCandidates aggregates permission_events for a project into
// suggested auto-allow rules (LEARN-TASKS.md LN-04) — the "Permissions" tab
// of the Experience panel. Safe are (tool, pattern) pairs that keep asking,
// have been consistently approved, and passed the read-only whitelist
// classifier; NeedsReview holds everything else that still meets the
// frequency/consistency bar (Edit/Write, or a Bash command outside the
// whitelist) — never auto-applied, just surfaced so a human can decide.
// Requires [optimization] experience_tracking to have been on for some runs;
// with it off, both lists are simply empty.
func (a *App) GetPermissionCandidates(project string, days int) (*experience.CandidateSet, error) {
	if a.store == nil {
		return nil, fmt.Errorf("no store")
	}
	stats, err := a.store.TopPermissionEvents(project, days, 0)
	if err != nil {
		return nil, err
	}
	safe, needsReview := experience.Candidates(stats, experience.MinPermissionCount)
	return &experience.CandidateSet{Safe: safe, NeedsReview: needsReview}, nil
}

// AddPermissionRule appends a PermissionRule to one session's config — the
// "Add rule" button on the Permissions tab (LEARN-TASKS.md LN-04). Goes
// through the same GetConfig→mutate→UpdateConfig round-trip every other
// config edit in this app uses; a rule already present for this exact
// (tool, pattern, decision) triple is a no-op rather than a duplicate.
func (a *App) AddPermissionRule(project, sessionName, tool, pattern, decision string) error {
	if a.cfg == nil {
		return fmt.Errorf("no config")
	}
	cfg := *a.cfg
	cfg.Projects = append([]config.ProjectConfig(nil), a.cfg.Projects...)
	if !addPermissionRuleInConfig(&cfg, project, sessionName, tool, pattern, decision) {
		return nil
	}
	return a.UpdateConfig(cfg)
}

// addPermissionRuleInConfig appends the rule to project/sessionName's
// PermissionRules in cfg, operating on cloned slices so the caller's config
// is never mutated in place (same rule as setSessionModelInConfig above).
// Reports whether anything changed, so an unchanged config is never rewritten
// to disk.
func addPermissionRuleInConfig(cfg *config.AppConfig, project, sessionName, tool, pattern, decision string) bool {
	for pi := range cfg.Projects {
		if cfg.Projects[pi].Name != project {
			continue
		}
		for si, sc := range cfg.Projects[pi].Sessions {
			if sc.Name != sessionName {
				continue
			}
			for _, r := range sc.PermissionRules {
				if r.Tool == tool && r.Pattern == pattern && r.Decision == decision {
					return false
				}
			}
			sessions := append([]config.SessionConfig(nil), cfg.Projects[pi].Sessions...)
			rules := append([]config.PermissionRule(nil), sc.PermissionRules...)
			rules = append(rules, config.PermissionRule{Tool: tool, Pattern: pattern, Decision: decision})
			sessions[si].PermissionRules = rules
			cfg.Projects[pi].Sessions = sessions
			return true
		}
		return false
	}
	return false
}

// DistillSkill turns one LN-08 skill candidate into a skill draft and
// persists it as a status=draft row (LEARN-TASKS.md LN-09), streaming
// skill:progress while the distillation CLI run is in flight. gates is
// normally the project's own config.ProjectConfig.Gates. minScore <= 0 falls
// back to analysis.DefaultSkillMinScore; when candidate.Score does not clear
// it, the CLI is never invoked and the error is analysis.ErrBelowThreshold —
// callers driving a batch of candidates should treat that as "skip this one",
// not surface it as a failure.
//
// SessionManager.DistillSkill takes analysis.SkillDistillInput, not
// experience.SkillCandidate/FailureCluster directly — see
// analysis.SkillDistillInput's doc comment for why (internal/session cannot
// import internal/experience without cycling back through it). This method
// does the translation, since app.go already imports both packages.
func (a *App) DistillSkill(project string, candidate experience.SkillCandidate, gates []string, model string, minScore float64) (*store.Skill, error) {
	in, sourceJSON, err := skillDistillInputFromCandidate(candidate, gates)
	if err != nil {
		return nil, err
	}
	return a.manager.DistillSkill(project, in, sourceJSON, candidate.Score, minScore, model)
}

// skillDistillInputFromCandidate translates one LN-08 SkillCandidate (plus
// the project's gates) into analysis.SkillDistillInput, and encodes the
// candidate's own signature sequence as source_json (LEARN-TASKS.md LN-09:
// "source_json — сигнатуры кандидата; по ним LN-11 определяет, сработал ли
// скилл"). Takes one representative example per related failure cluster —
// enough context for the distillation prompt without re-exporting
// FailureCluster's full shape.
func skillDistillInputFromCandidate(c experience.SkillCandidate, gates []string) (analysis.SkillDistillInput, string, error) {
	in := analysis.SkillDistillInput{
		Sig:   append([]string(nil), c.Sig...),
		Gates: append([]string(nil), gates...),
	}
	for _, s := range c.Samples {
		in.Samples = append(in.Samples, analysis.SkillSample{Command: s.Arg})
	}
	for _, f := range c.RelatedFailures {
		if len(f.Examples) == 0 {
			continue
		}
		ex := f.Examples[0]
		in.RelatedFailures = append(in.RelatedFailures, analysis.SkillFailureSummary{
			ErrorKey:  f.ErrorKey,
			FailedArg: ex.FailedArg,
			FixedArg:  ex.FixedArg,
		})
	}

	srcJSON, err := json.Marshal(c.Sig)
	if err != nil {
		return in, "", fmt.Errorf("distill skill: encode source signatures: %w", err)
	}
	return in, string(srcJSON), nil
}

// GetSkills lists every distilled skill for a project — draft, approved and
// archived alike, newest first — the "Skills" tab's source of truth
// (LEARN-TASKS.md LN-10). The tab itself decides what to show for each
// status (a draft gets review buttons, an approved/archived row is
// read-only).
func (a *App) GetSkills(project string) ([]store.Skill, error) {
	if a.store == nil {
		return nil, fmt.Errorf("no store")
	}
	return a.store.ListSkills(project)
}

// ApproveSkill writes a draft's (possibly reviewer-edited) markdown to
// <project>/.claude/skills/<name>/SKILL.md and marks the row approved
// (LEARN-TASKS.md LN-10). md need not be byte-identical to the skill's
// stored draft — the reviewer may have fixed something in
// SkillReview.svelte before accepting. name is fixed to the row's own
// Skill.Name (not a parameter): the invariant is "an unsafe name can never
// be written", regardless of what a caller passes, and experience.WriteSkillFile
// enforces that by construction. Refuses to overwrite an existing file
// unless overwrite is true (experience.ErrSkillFileExists), so the UI has
// something to catch for its inline "already exists — overwrite?" banner —
// window.confirm() is disabled in Wails WebView2.
func (a *App) ApproveSkill(id int64, md string, overwrite bool) (string, error) {
	if a.store == nil {
		return "", fmt.Errorf("no store")
	}
	sk, err := a.store.GetSkill(id)
	if err != nil {
		return "", err
	}
	if sk == nil {
		return "", fmt.Errorf("skill %d not found", id)
	}
	path, err := a.projectPath(sk.Project)
	if err != nil {
		return "", err
	}
	written, err := experience.WriteSkillFile(path, sk.Name, md, overwrite)
	if err != nil {
		return "", err
	}
	if err := a.store.UpdateSkillApproved(id, md, time.Now()); err != nil {
		return "", err
	}
	return written, nil
}

// ArchiveSkill marks a skill row archived — a rejected draft, or later a
// skill LN-11 proposes as stale (LEARN-TASKS.md LN-10/11). Never touches a
// file already written into the project; archiving only removes the row
// from the "Skills" tab's active list.
func (a *App) ArchiveSkill(id int64) error {
	if a.store == nil {
		return fmt.Errorf("no store")
	}
	return a.store.UpdateSkillArchived(id, time.Now())
}

// GetSkillQuality measures the before/after-approval effect of every
// approved skill in project — the "Skills" tab's quality table
// (LEARN-TASKS.md LN-11). See experience.BuildSkillQualityReport for the
// comparison and staleness rules; this is a thin store-backed wrapper, same
// pattern as GetDurationProfile.
func (a *App) GetSkillQuality(project string) ([]experience.SkillEffect, error) {
	if a.store == nil {
		return nil, fmt.Errorf("no store")
	}
	return experience.BuildSkillQualityReport(a.store, project)
}

// GetProjectLogFiles lists the auto-saved session-log files in
// <project>/.claude-manager/logs (see "Automatic Log Saving" in CLAUDE.md),
// newest first — the Settings project-logs panel uses this to show file
// count and total size.
func (a *App) GetProjectLogFiles(project string) ([]store.LogFileInfo, error) {
	path, err := a.projectPath(project)
	if err != nil {
		return nil, err
	}
	return store.ListProjectLogFiles(path)
}

// ClearProjectLogs deletes every saved log file in
// <project>/.claude-manager/logs and the corresponding session_logs rows in
// SQLite. session_runs rows (History/Dashboard) are left untouched — this
// clears log bodies, not run history.
func (a *App) ClearProjectLogs(project string) error {
	path, err := a.projectPath(project)
	if err != nil {
		return err
	}
	if _, _, err := store.ClearProjectLogFiles(path); err != nil {
		return err
	}
	if a.store == nil {
		return nil
	}
	return a.store.DeleteLogsForProject(project)
}

func defaultExportFilename(sessionID, format string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(sessionID)
	if safe == "" {
		safe = "session"
	}
	stamp := time.Now().Format("20060102-150405")
	return fmt.Sprintf("%s-%s.%s", safe, stamp, format)
}

func exportFiltersFor(format string) []runtime.FileFilter {
	switch format {
	case "md":
		return []runtime.FileFilter{{DisplayName: "Markdown (*.md)", Pattern: "*.md"}}
	case "json":
		return []runtime.FileFilter{{DisplayName: "JSON (*.json)", Pattern: "*.json"}}
	default:
		return []runtime.FileFilter{{DisplayName: "Text (*.txt)", Pattern: "*.txt"}}
	}
}

// Rendering itself lives in store.RenderExport, shared with the automatic
// per-run log save (see internal/session/manager.go:finishRun).
