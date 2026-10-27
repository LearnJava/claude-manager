package control

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"claude-manager/internal/config"
	"claude-manager/internal/experience"
	"claude-manager/internal/optimization"
	"claude-manager/internal/session"
	"claude-manager/internal/store"
)

// LogsApp is the part of *App the logs-and-metrics RPCs need beyond the
// manager: the project log files, the experience tables and the model-routing
// advice. A control server without it (playwright-server, tests) reports these
// methods as unavailable.
type LogsApp interface {
	GetProjectLogFiles(project string) ([]store.LogFileInfo, error)
	ClearProjectLogs(project string) error
	CleanOldLogs(retentionDays int) error
	ImportProjectLogs(project, dir string) (session.ImportStats, error)
	GetTopActions(project string, days int) ([]store.SignatureStat, error)
	GetActionSamples(project, sig string, limit int) ([]store.ActionRow, error)
	GetDurationProfile(project string) ([]experience.SignatureDuration, error)
	GetTokenAttribution(project string, topN int) (experience.AttributionReport, error)
	GetWasteReport(project string) (experience.WasteReport, error)
	GetReferenceReport(project string) (experience.ReferenceReport, error)
	ReplayReferenceCase(project string, caseID int64, model, effort string) (*store.ReferenceReplay, error)
	GetModelRecommendation(project, name string) (*optimization.ModelRecommendation, error)
	GetAutoModelRouting() bool
}

// logBatch is how many entries one read of the session log fetches while a
// whole log is scanned.
const logBatch = 500

// allLogEntries walks the latest run's log of a session from the start, handing
// each entry to visit until it returns false or the log ends.
func (s *Server) allLogEntries(id string, visit func(*store.LogEntry) bool) error {
	for offset := 0; ; offset += logBatch {
		batch, err := s.manager.GetSessionLog(id, offset, logBatch)
		if err != nil {
			return err
		}
		for _, e := range batch {
			if !visit(e) {
				return nil
			}
		}
		if len(batch) < logBatch {
			return nil
		}
	}
}

// exportDirOf is where exported logs are written: ~/.claude-manager/exports.
func (s *Server) exportDirOf() string {
	if s.exportDir != "" {
		return s.exportDir
	}
	return filepath.Join(filepath.Dir(config.DefaultConfigPath()), "exports")
}

// projectArg is the argument of the RPCs that take only a project.
type projectArg struct {
	Project string `json:"project"`
}

// registerLogsRPCs adds the logs-and-metrics methods (MCP-09).
func (s *Server) registerLogsRPCs(reg map[string]handler) {
	m := s.manager
	lapp, _ := s.app.(LogsApp)
	needApp := func(what string) error {
		return fmt.Errorf("%s needs the Claude Manager app (not available in this control server)", what)
	}

	reg["GetDailyTokens"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Date string `json:"date"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		return m.GetDailyTokens(a.Date)
	}

	reg["GetProjectTokens"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
			Days    int    `json:"days"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		return m.GetProjectTokens(a.Project, a.Days)
	}

	// SearchSessionLog filters the latest run's log on the server so an agent
	// does not page through all of it. It returns the matches after skipping
	// offset of them, at most limit.
	reg["SearchSessionLog"] = func(p json.RawMessage) (any, error) {
		var a struct {
			ID       string `json:"id"`
			Level    string `json:"level"`
			Tool     string `json:"tool"`
			Contains string `json:"contains"`
			Offset   int    `json:"offset"`
			Limit    int    `json:"limit"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if a.Level == "" && a.Tool == "" && a.Contains == "" {
			return nil, fmt.Errorf("give at least one filter: level, tool or contains")
		}
		needle := strings.ToLower(a.Contains)
		out := []*store.LogEntry{}
		seen := 0
		err := s.allLogEntries(a.ID, func(e *store.LogEntry) bool {
			if a.Level != "" && e.Level != a.Level {
				return true
			}
			if a.Tool != "" && !strings.EqualFold(e.ToolName, a.Tool) {
				return true
			}
			if needle != "" && !strings.Contains(strings.ToLower(e.Message+"\n"+e.ToolInput), needle) {
				return true
			}
			seen++
			if seen > a.Offset {
				out = append(out, e)
			}
			return a.Limit <= 0 || len(out) < a.Limit
		})
		return out, err
	}

	// ExportLog writes a session's log to a file under the exports folder and
	// returns the path: the GUI picks the path with a save dialog, which an
	// agent cannot answer.
	reg["ExportLog"] = func(p json.RawMessage) (any, error) {
		var a struct {
			ID     string `json:"id"`
			Format string `json:"format"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		format := strings.ToLower(strings.TrimSpace(a.Format))
		if format == "" {
			format = "txt"
		}
		if format != "md" && format != "json" && format != "txt" {
			return nil, fmt.Errorf("format %q is not valid: use md, json or txt", a.Format)
		}
		var entries []store.LogEntry
		if err := s.allLogEntries(a.ID, func(e *store.LogEntry) bool {
			entries = append(entries, *e)
			return true
		}); err != nil {
			return nil, err
		}
		data, err := store.RenderExport(a.ID, entries, format)
		if err != nil {
			return nil, err
		}
		dir := s.exportDirOf()
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		safe := strings.NewReplacer("/", "_", "\\", "_", ":", "_").Replace(a.ID)
		path := filepath.Join(dir, fmt.Sprintf("%s-%s.%s", safe, time.Now().Format("20060102-150405"), format))
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return nil, err
		}
		return map[string]any{"path": path, "format": format, "entries": len(entries), "bytes": len(data)}, nil
	}

	reg["GetProjectLogFiles"] = func(p json.RawMessage) (any, error) {
		var a projectArg
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if lapp == nil {
			return nil, needApp("listing log files")
		}
		files, err := lapp.GetProjectLogFiles(a.Project)
		if files == nil {
			files = []store.LogFileInfo{}
		}
		return files, err
	}

	reg["ClearProjectLogs"] = func(p json.RawMessage) (any, error) {
		var a projectArg
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if lapp == nil {
			return nil, needApp("clearing project logs")
		}
		return nil, lapp.ClearProjectLogs(a.Project)
	}

	reg["CleanOldLogs"] = func(p json.RawMessage) (any, error) {
		var a struct {
			RetentionDays int `json:"retention_days"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if a.RetentionDays <= 0 {
			return nil, fmt.Errorf("retention_days must be at least 1")
		}
		if lapp == nil {
			return nil, needApp("cleaning old logs")
		}
		return nil, lapp.CleanOldLogs(a.RetentionDays)
	}

	reg["ImportProjectLogs"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
			Dir     string `json:"dir"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if lapp == nil {
			return nil, needApp("importing project logs")
		}
		if a.Dir != "" {
			if fi, err := os.Stat(a.Dir); err != nil || !fi.IsDir() {
				return nil, fmt.Errorf("dir %q does not exist or is not a folder", a.Dir)
			}
		}
		st, err := lapp.ImportProjectLogs(a.Project, a.Dir)
		return map[string]int{"files": st.Files, "runs": st.Runs, "actions": st.Actions, "skipped": st.Skipped, "errors": st.Errors}, err
	}

	reg["GetTopActions"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
			Days    int    `json:"days"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if lapp == nil {
			return nil, needApp("the action table")
		}
		rows, err := lapp.GetTopActions(a.Project, a.Days)
		if rows == nil {
			rows = []store.SignatureStat{}
		}
		return rows, err
	}

	reg["GetActionSamples"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
			Sig     string `json:"sig"`
			Limit   int    `json:"limit"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if lapp == nil {
			return nil, needApp("action samples")
		}
		rows, err := lapp.GetActionSamples(a.Project, a.Sig, a.Limit)
		if rows == nil {
			rows = []store.ActionRow{}
		}
		return rows, err
	}

	reg["GetDurationProfile"] = func(p json.RawMessage) (any, error) {
		var a projectArg
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if lapp == nil {
			return nil, needApp("the duration profile")
		}
		rows, err := lapp.GetDurationProfile(a.Project)
		if rows == nil {
			rows = []experience.SignatureDuration{}
		}
		return rows, err
	}

	reg["GetTokenAttribution"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
			TopN    int    `json:"top_n"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if lapp == nil {
			return nil, needApp("token attribution")
		}
		return lapp.GetTokenAttribution(a.Project, a.TopN)
	}

	reg["GetWasteReport"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if lapp == nil {
			return nil, needApp("the waste report")
		}
		return lapp.GetWasteReport(a.Project)
	}

	reg["GetReferenceReport"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if lapp == nil {
			return nil, needApp("the reference report")
		}
		return lapp.GetReferenceReport(a.Project)
	}

	reg["ReplayReferenceCase"] = func(p json.RawMessage) (any, error) {
		var a struct {
			Project string `json:"project"`
			CaseID  int64  `json:"case_id"`
			Model   string `json:"model"`
			Effort  string `json:"effort"`
		}
		if err := json.Unmarshal(p, &a); err != nil {
			return nil, err
		}
		if lapp == nil {
			return nil, needApp("reference case replay")
		}
		return lapp.ReplayReferenceCase(a.Project, a.CaseID, a.Model, a.Effort)
	}

	reg["GetAutoModelRouting"] = func(_ json.RawMessage) (any, error) {
		if lapp == nil {
			return nil, needApp("model routing")
		}
		return lapp.GetAutoModelRouting(), nil
	}

	modelArgs := func(p json.RawMessage) (project, name string, err error) {
		var a struct {
			Project string `json:"project"`
			Session string `json:"session"`
		}
		err = json.Unmarshal(p, &a)
		return a.Project, a.Session, err
	}

	reg["GetModelRecommendation"] = func(p json.RawMessage) (any, error) {
		project, name, err := modelArgs(p)
		if err != nil {
			return nil, err
		}
		if lapp == nil {
			return nil, needApp("a model recommendation")
		}
		return lapp.GetModelRecommendation(project, name)
	}

	// GetModelAdvice answers the question behind both calls at once: is
	// routing on, and what does it recommend for a session's prompt.
	reg["GetModelAdvice"] = func(p json.RawMessage) (any, error) {
		project, name, err := modelArgs(p)
		if err != nil {
			return nil, err
		}
		if lapp == nil {
			return nil, needApp("model advice")
		}
		rec, err := lapp.GetModelRecommendation(project, name)
		return map[string]any{"auto_routing": lapp.GetAutoModelRouting(), "recommendation": rec}, err
	}
}
