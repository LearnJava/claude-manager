package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claude-manager/internal/analysis"
	"claude-manager/internal/config"
	"claude-manager/internal/control"
	"claude-manager/internal/logger"
	"claude-manager/internal/optimization"
	"claude-manager/internal/permission"
	"claude-manager/internal/session"
	"claude-manager/internal/store"

	toast "git.sr.ht/~jackmordaunt/go-toast/v2"
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

	// Build the emitter chain. If CM_CONTROL=1 we fan-out to both Wails and
	// the ControlEmitter so the GUI and the headless bridge receive all events.
	a.wailsEmitter = control.NewWailsEmitter()
	a.wailsEmitter.SetContext(ctx)

	var sessionEmitter session.Emitter = a.wailsEmitter
	var controlEmitter *control.ControlEmitter
	if os.Getenv("CM_CONTROL") == "1" {
		controlEmitter = control.NewControlEmitter(200)
		sessionEmitter = control.NewMultiEmitter(a.wailsEmitter, controlEmitter)
	}

	a.manager = session.NewSessionManager(cfg, a.cfgPath, a.store, sessionEmitter)

	// Start the control-plane server (no-op when CM_CONTROL != "1").
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
	rec := router.Route(result.RecommendedModel, result.RecommendedEffort,
		result.Feasibility.EstimatedComplexity)
	return &rec, nil
}

// StartSessionWithModel starts a session overriding the configured model and effort.
// Empty strings fall back to the values in config.
func (a *App) StartSessionWithModel(project, name, model, effort string) error {
	return a.manager.StartSessionWithOverride(project, name, model, effort)
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

func (a *App) StartProject(project string) error {
	return a.manager.StartProject(project)
}

func (a *App) StopProject(project string) error {
	return a.manager.StopProject(project)
}

// ---- Bidirectional streaming + permissions ----

func (a *App) SendMessage(id, message string) error {
	return a.manager.SendMessage(id, message)
}

func (a *App) RespondPermission(id, requestID, decision string) error {
	return a.manager.RespondPermission(id, requestID, decision)
}

func (a *App) GetPendingPermissions() []permission.PermissionRequest {
	return a.manager.GetPendingPermissions()
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

func (a *App) GetProjectCost(project string, days int) (float64, error) {
	return a.manager.GetProjectCost(project, days)
}

func (a *App) GetRateLimitStatus() *session.RateLimitInfo {
	return a.manager.GetRateLimitStatus()
}

// ---- Settings / config persistence ----

// UpdateConfig replaces the in-memory config, writes it to disk as TOML, and
// re-applies defaults so that subsequent reads see normalised values.
func (a *App) UpdateConfig(cfg config.AppConfig) error {
	if err := config.Save(&cfg, a.cfgPath); err != nil {
		return err
	}
	// Re-read so applyDefaults/validate run on the saved file.
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

// MinimizeToTray hides the main window so that only the (future) tray icon
// remains. With HideWindowOnClose enabled in main.go, closing the window has
// the same effect.
func (a *App) MinimizeToTray() {
	if a.ctx == nil {
		return
	}
	runtime.WindowHide(a.ctx)
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

	data, err := renderExport(sessionID, entries, format)
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

func renderExport(sessionID string, entries []store.LogEntry, format string) ([]byte, error) {
	switch format {
	case "json":
		return json.MarshalIndent(struct {
			Session string           `json:"session"`
			Entries []store.LogEntry `json:"entries"`
		}{Session: sessionID, Entries: entries}, "", "  ")

	case "md":
		var b strings.Builder
		fmt.Fprintf(&b, "# Session log — %s\n\n", sessionID)
		fmt.Fprintf(&b, "_Exported %s, %d entries._\n\n", time.Now().Format(time.RFC3339), len(entries))
		for _, e := range entries {
			fmt.Fprintf(&b, "- `%s` **%s** %s\n",
				e.Timestamp.Format("15:04:05"),
				e.Level,
				escapeMarkdown(e.Message),
			)
			if e.ToolName != "" {
				fmt.Fprintf(&b, "  - tool: `%s` %s\n", e.ToolName, escapeMarkdown(e.ToolInput))
			}
		}
		return []byte(b.String()), nil

	default: // txt
		var b strings.Builder
		fmt.Fprintf(&b, "Session log — %s\n", sessionID)
		fmt.Fprintf(&b, "Exported %s\n\n", time.Now().Format(time.RFC3339))
		for _, e := range entries {
			fmt.Fprintf(&b, "[%s] %-7s %s\n",
				e.Timestamp.Format("15:04:05"),
				e.Level,
				e.Message,
			)
			if e.ToolName != "" {
				fmt.Fprintf(&b, "        tool=%s %s\n", e.ToolName, e.ToolInput)
			}
		}
		return []byte(b.String()), nil
	}
}

func escapeMarkdown(s string) string {
	// Single-line: collapse newlines so list items render correctly.
	return strings.NewReplacer("\r", " ", "\n", "  ").Replace(s)
}
