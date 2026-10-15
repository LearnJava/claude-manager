package control

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
)

// Tools of the logs-and-metrics area (MCP-09): session logs and their search,
// saved log files, cost and token figures, and the experience tables. Every
// list is cut to a page and by bytes (MaxResponseBytes).

type historyIn struct {
	Project string `json:"project,omitempty" jsonschema:"Project name, e.g. lumen; omit for the runs of every project"`
	pageIn
}

type searchLogIn struct {
	sessionRef
	pageIn
	Level    string `json:"level,omitempty" jsonschema:"Keep only entries of this kind: text, tool, error, result or system"`
	Tool     string `json:"tool,omitempty" jsonschema:"Keep only entries of this tool, e.g. Bash or Edit (case does not matter)"`
	Contains string `json:"contains,omitempty" jsonschema:"Keep only entries whose message or tool input contains this text (case does not matter), e.g. permission denied"`
}

type costIn struct {
	Scope   string `json:"scope" jsonschema:"day for one date across all projects, project for one project over a period"`
	Date    string `json:"date,omitempty" jsonschema:"Day as YYYY-MM-DD for scope day, e.g. 2026-10-15; default today"`
	Project string `json:"project,omitempty" jsonschema:"Project name for scope project, e.g. lumen"`
	Period  int64  `json:"period,omitempty" jsonschema:"Number of days for scope project, 1-365 (default 7)"`
}

type exportIn struct {
	sessionRef
	Format string `json:"format,omitempty" jsonschema:"File format: md, json or txt (default txt)"`
}

type importLogsIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	Dir     string `json:"dir,omitempty" jsonschema:"Absolute path of a folder of saved CLI logs on the machine running Claude Manager, e.g. D:\\logs\\lumen; default the project's own .claude-manager/logs"`
}

type logFilesIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	pageIn
}

type clearLogsIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	Confirm bool   `json:"confirm" jsonschema:"Must be true: acknowledges that the project's saved log files and stored log lines are deleted"`
}

type cleanLogsIn struct {
	RetentionDays int64 `json:"retention_days" jsonschema:"Delete log lines of runs older than this many days, at least 1, e.g. 30"`
	Confirm       bool  `json:"confirm" jsonschema:"Must be true: acknowledges that old log lines of every project are deleted"`
}

type topActionsIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	Days    int64  `json:"days,omitempty" jsonschema:"Look back this many days, 1-365 (default 30)"`
	pageIn
}

type actionSamplesIn struct {
	Project   string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	Signature string `json:"signature" jsonschema:"Action signature, the Sig value of a get_top_actions row, e.g. Bash:go test"`
	pageIn
}

type durationIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	pageIn
}

type attributionIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	TopN    int64  `json:"top_n,omitempty" jsonschema:"Keep this many most expensive signatures, 1-100 (default 20)"`
}

type adviceIn struct {
	sessionRef
}

type historyOut struct {
	Runs []any `json:"runs" jsonschema:"Past runs, newest first: project, session, model, started and finished times, status, cost and token counts"`
	pageOut
}

type metricsOneOut struct {
	SessionID string `json:"session_id" jsonschema:"Session ID in project/session form"`
	Metrics   any    `json:"metrics" jsonschema:"Token counts, num_turns, total_cost_usd, context_window, context_util and duration_ms of the session"`
}

type costOut struct {
	Scope         string  `json:"scope" jsonschema:"day or project, as asked"`
	Date          string  `json:"date,omitempty" jsonschema:"The day, for scope day"`
	Project       string  `json:"project,omitempty" jsonschema:"The project, for scope project"`
	Days          int64   `json:"days,omitempty" jsonschema:"Length of the period in days, for scope project"`
	CostUSD       float64 `json:"cost_usd" jsonschema:"Cost in USD"`
	InputTokens   int64   `json:"input_tokens" jsonschema:"Input tokens"`
	OutputTokens  int64   `json:"output_tokens" jsonschema:"Output tokens"`
	CacheRead     int64   `json:"cache_read" jsonschema:"Cache read tokens"`
	CacheCreation int64   `json:"cache_creation" jsonschema:"Cache creation tokens"`
	Total         int64   `json:"total" jsonschema:"All tokens together"`
}

type exportOut struct {
	Path    string `json:"path" jsonschema:"Absolute path of the written file on the machine running Claude Manager; read it from there"`
	Format  string `json:"format" jsonschema:"md, json or txt"`
	Entries int64  `json:"entries" jsonschema:"Number of log entries written"`
	Bytes   int64  `json:"bytes" jsonschema:"File size in bytes"`
}

type importOut struct {
	Files   int64 `json:"files" jsonschema:"Log files read"`
	Runs    int64 `json:"runs" jsonschema:"Runs found in them"`
	Actions int64 `json:"actions" jsonschema:"Actions added to the experience tables"`
	Skipped int64 `json:"skipped" jsonschema:"Files skipped because an earlier import already covered them"`
	Errors  int64 `json:"errors" jsonschema:"Files that could not be read"`
}

type logFilesOut struct {
	Files []any `json:"files" jsonschema:"Saved log files, newest first: name, path, size in bytes and mod_time"`
	pageOut
}

type cleanAck struct {
	OK            bool  `json:"ok" jsonschema:"True when the cleanup finished"`
	RetentionDays int64 `json:"retention_days" jsonschema:"The age limit that was applied"`
}

type actionsOut struct {
	Actions []any `json:"actions" jsonschema:"Action signatures, most frequent first: Sig, Tool, Count, DistinctRuns, ErrorRate, SumOutTokens, SampleArgs, FirstSeen, LastSeen"`
	pageOut
}

type samplesOut struct {
	Samples []any `json:"samples" jsonschema:"Concrete calls behind the signature: Session, Tool, Arg, IsError, OutTokens and when they ran"`
	pageOut
}

type durationOut struct {
	Durations []any `json:"durations" jsonschema:"Per signature: Sig, Tool, Count, MedianSec, P90Sec, MaxSec, TotalSec, FailRate; slowest first"`
	pageOut
}

type attributionOut struct {
	Report any `json:"report" jsonschema:"TotalEstTokens, TotalLifetimeTokens and the BySignature, ByTool and ByLength tables, most expensive first"`
}

type adviceOut struct {
	AutoRouting    bool `json:"auto_routing" jsonschema:"True when auto_model_routing is on in the config"`
	Recommendation any  `json:"recommendation" jsonschema:"model, effort, complexity and reason for the session's prompt; null when routing is off or the session has no prompt"`
}

// fetchWindow validates the paging arguments of a list whose source takes a
// size: it returns that size, one more than the page reaches so that a further
// page is detected.
func fetchWindow(p pageIn) (int, error) {
	offset, limit, err := p.window()
	return offset + limit + 1, err
}

func needProject(project string) error {
	if project == "" {
		return fmt.Errorf(`project is required, e.g. "lumen"; list projects with get_projects`)
	}
	return nil
}

func logsTools() []Tool {
	ro := ToolAnnotations{ReadOnly: true}
	gone := ToolAnnotations{Destructive: true, Idempotent: true}
	const (
		deleteFx = "deletes data for good and cannot be undone."
		days     = 365
	)

	return []Tool{
		define(spec[historyIn, historyOut]{
			Name: "get_history", Title: "List past runs",
			Doc: Doc{
				What:    "List the past runs of a project's sessions, or of all projects, newest first.",
				UseWhen: "you want to know what ran before, how it ended and what it cost.",
				NotFor:  "a running session (use get_session) or what a run said (use get_session_logs).",
				Returns: "runs, with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{"project":"lumen","limit":20}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetHistory"},
			Translate: func(in historyIn) (string, any, error) {
				n, err := fetchWindow(in.pageIn)
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetHistory", map[string]any{"project": in.Project, "limit": n}), nil
			},
			Result: func(in historyIn, raw any) historyOut {
				page, p := listPage(in.pageIn, raw)
				return historyOut{Runs: page, pageOut: p}
			},
			Summary: func(_ historyIn, out historyOut) string {
				return plural(len(out.Runs), "run", "runs") + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[searchLogIn, logsOut]{
			Name: "search_session_log", Title: "Search session log",
			Doc: Doc{
				What:    "Find log entries of a session's latest run by kind, tool or text, oldest first, one page at a time.",
				UseWhen: "you look for an error, a tool call or a phrase in a long log without reading all of it.",
				NotFor:  "reading the log in order (use get_session_logs).",
				Returns: "matching entries, with next_cursor and truncated when more match.",
				Effects: "none, read only.",
				Example: `{"session_id":"lumen/P1","level":"error"}`,
			},
			Input: tuneAll(tunePage, func(props map[string]*jsonschema.Schema) {
				props["level"].Enum = []any{"text", "tool", "error", "result", "system"}
			}),
			Level: LevelRead, Annotations: ro, Covers: nil,
			Translate: func(in searchLogIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				if in.Level == "" && in.Tool == "" && in.Contains == "" {
					return "", nil, fmt.Errorf("give at least one filter: level, tool or contains; to read the whole log use get_session_logs")
				}
				offset, limit, err := in.pageIn.window()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("SearchSessionLog", map[string]any{
					"id": id, "level": in.Level, "tool": in.Tool, "contains": in.Contains,
					"offset": offset, "limit": limit + 1,
				}), nil
			},
			Result: func(in searchLogIn, raw any) logsOut {
				id, _ := in.id()
				offset, limit, _ := in.pageIn.window()
				page, next, more := pageOf(list(raw), 0, limit)
				return logsOut{SessionID: id, Entries: page, pageOut: pageInfo(offset, next, more)}
			},
			Summary: func(_ searchLogIn, out logsOut) string {
				return fmt.Sprintf("%s matched in %s.", plural(len(out.Entries), "log entry", "log entries"), out.SessionID) + moreHint(out.pageOut)
			},
		}),
		define(spec[sessionIn, metricsOneOut]{
			Name: "get_session_metrics", Title: "Get session metrics",
			Doc: Doc{
				What:    "Get a session's token counts, turns, cost, context use and duration.",
				UseWhen: "you check what one session has spent or how full its context is.",
				NotFor:  "totals over days or projects (use get_cost).",
				Returns: "session_id and metrics.",
				Effects: "none, read only.",
				Example: `{"session_id":"lumen/P1"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetSessionMetrics"},
			Translate: func(in sessionIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetSessionMetrics", map[string]string{"id": id}), nil
			},
			Result: func(in sessionIn, raw any) metricsOneOut {
				id, _ := in.id()
				return metricsOneOut{SessionID: id, Metrics: raw}
			},
			Summary: func(_ sessionIn, out metricsOneOut) string {
				m, _ := out.Metrics.(map[string]any)
				cost, _ := m["total_cost_usd"].(float64)
				return fmt.Sprintf("%s: $%.2f.", out.SessionID, cost)
			},
		}),
		define(spec[costIn, costOut]{
			Name: "get_cost", Title: "Get cost and tokens",
			Doc: Doc{
				What:    "Get the cost and token volume of one day across all projects, or of one project over a period.",
				UseWhen: "you check spending against a budget, or compare days or projects.",
				NotFor:  "one session (use get_session_metrics) or the rate-limit status (use get_metrics without a project).",
				Returns: "cost_usd and the input, output, cache and total token counts.",
				Effects: "none, read only.",
				Example: `{"scope":"project","project":"lumen","period":7}`,
			},
			Input: func(props map[string]*jsonschema.Schema) {
				props["scope"].Enum = []any{"day", "project"}
				p := props["period"]
				p.Default, p.Minimum, p.Maximum = json.RawMessage(`7`), fptr(1), fptr(days)
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetDailyCost", "GetDailyTokens", "GetProjectTokens"},
			Translate: func(in costIn) (string, any, error) {
				switch in.Scope {
				case "day":
					date := in.Date
					if date == "" {
						date = time.Now().Format("2006-01-02")
					} else if _, err := time.Parse("2006-01-02", date); err != nil {
						return "", nil, fmt.Errorf("date %q is not valid: use YYYY-MM-DD, e.g. 2026-10-15", date)
					}
					return "/rpc", rpcCallBody("GetDailyTokens", map[string]string{"date": date}), nil
				case "project":
					if err := needProject(in.Project); err != nil {
						return "", nil, err
					}
					return "/rpc", rpcCallBody("GetProjectTokens", map[string]any{"project": in.Project, "days": orDefault(in.Period, 7)}), nil
				}
				return "", nil, fmt.Errorf(`scope is required: "day" with an optional date, or "project" with a project`)
			},
			Result: func(in costIn, raw any) costOut {
				m, _ := raw.(map[string]any)
				num := func(k string) int64 { f, _ := m[k].(float64); return int64(f) }
				out := costOut{
					Scope: in.Scope, InputTokens: num("input_tokens"), OutputTokens: num("output_tokens"),
					CacheRead: num("cache_read"), CacheCreation: num("cache_creation"), Total: num("total"),
				}
				out.CostUSD, _ = m["cost_usd"].(float64)
				if in.Scope == "day" {
					out.Date, _ = m["date"].(string)
				} else {
					out.Project, out.Days = in.Project, orDefault(in.Period, 7)
				}
				return out
			},
			Summary: func(_ costIn, out costOut) string {
				if out.Scope == "day" {
					return fmt.Sprintf("%s: $%.2f, %d tokens.", out.Date, out.CostUSD, out.Total)
				}
				return fmt.Sprintf("%s over %d days: $%.2f, %d tokens.", out.Project, out.Days, out.CostUSD, out.Total)
			},
		}),
		define(spec[exportIn, exportOut]{
			Name: "export_log", Title: "Export session log to a file",
			Doc: Doc{
				What:    "Write a session's latest log to a file in the manager's exports folder and return its path.",
				UseWhen: "a human or another tool needs the whole log as a file, which is too large to read through get_session_logs.",
				NotFor:  "reading the log into your context (use get_session_logs or search_session_log).",
				Returns: "path, format, entries and bytes; the path is on the machine running Claude Manager.",
				Effects: "creates one file under the user folder .claude-manager/exports.",
				Example: `{"session_id":"lumen/P1","format":"md"}`,
			},
			Input: func(props map[string]*jsonschema.Schema) {
				f := props["format"]
				f.Enum, f.Default = []any{"md", "json", "txt"}, json.RawMessage(`"txt"`)
			},
			Level: LevelOperate, Annotations: ToolAnnotations{}, Covers: []string{"ExportLog"},
			Translate: func(in exportIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("ExportLog", map[string]string{"id": id, "format": strings.ToLower(in.Format)}), nil
			},
			Result: func(_ exportIn, raw any) exportOut {
				m, _ := raw.(map[string]any)
				num := func(k string) int64 { f, _ := m[k].(float64); return int64(f) }
				out := exportOut{Entries: num("entries"), Bytes: num("bytes")}
				out.Path, _ = m["path"].(string)
				out.Format, _ = m["format"].(string)
				return out
			},
			Summary: func(_ exportIn, out exportOut) string {
				return fmt.Sprintf("%s written to %s.", plural(int(out.Entries), "entry", "entries"), out.Path)
			},
		}),
		define(spec[importLogsIn, importOut]{
			Name: "import_project_logs", Title: "Import saved CLI logs",
			Doc: Doc{
				What:    "Read a folder of saved CLI logs into the project's experience tables.",
				UseWhen: "you bring the history of earlier runs, or a log corpus from another machine, into the experience layer.",
				NotFor:  "listing the saved logs (use get_project_log_files).",
				Returns: "files, runs, actions, skipped and errors counts; importing a folder again skips what it already covered.",
				Effects: "adds rows to the manager's database; needs experience tracking on in the project settings.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"ImportProjectLogs"},
			Translate: func(in importLogsIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("ImportProjectLogs", map[string]string{"project": in.Project, "dir": in.Dir}), nil
			},
			Result: func(_ importLogsIn, raw any) importOut {
				m, _ := raw.(map[string]any)
				num := func(k string) int64 { f, _ := m[k].(float64); return int64(f) }
				return importOut{Files: num("files"), Runs: num("runs"), Actions: num("actions"), Skipped: num("skipped"), Errors: num("errors")}
			},
			Summary: func(_ importLogsIn, out importOut) string {
				return fmt.Sprintf("Imported %d actions from %d files (%d skipped, %d errors).", out.Actions, out.Files, out.Skipped, out.Errors)
			},
		}),
		define(spec[logFilesIn, logFilesOut]{
			Name: "get_project_log_files", Title: "List saved log files",
			Doc: Doc{
				What:    "List the session logs the manager saved automatically in a project's .claude-manager/logs folder, newest first.",
				UseWhen: "you check how many logs a project keeps and how large they are, before clearing them.",
				NotFor:  "reading a session's log (use get_session_logs).",
				Returns: "files, with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{"project":"lumen"}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetProjectLogFiles"},
			Translate: func(in logFilesIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if _, _, err := in.pageIn.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetProjectLogFiles", map[string]string{"project": in.Project}), nil
			},
			Result: func(in logFilesIn, raw any) logFilesOut {
				page, p := listPage(in.pageIn, raw)
				return logFilesOut{Files: page, pageOut: p}
			},
			Summary: func(in logFilesIn, out logFilesOut) string {
				return plural(len(out.Files), "log file", "log files") + " of " + in.Project + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[clearLogsIn, projectAck]{
			Name: "clear_project_logs", Title: "Clear project logs",
			Doc: Doc{
				What:    "Delete every saved log file of a project and its stored log lines.",
				UseWhen: "a project's logs are no longer needed and take space.",
				NotFor:  "removing run history (it stays, so get_history still works) or logs older than a limit only (use clean_old_logs).",
				Returns: "ok and the project.",
				Effects: deleteFx,
				Example: `{"project":"lumen","confirm":true}`,
			},
			Level: LevelAdmin, Annotations: gone, Covers: []string{"ClearProjectLogs"},
			Translate: func(in clearLogsIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if !in.Confirm {
					return "", nil, fmt.Errorf("clear_project_logs deletes all logs of %s for good: call it again with confirm=true if that is intended, or use clean_old_logs to delete only old ones", in.Project)
				}
				return "/rpc", rpcCallBody("ClearProjectLogs", map[string]string{"project": in.Project}), nil
			},
			Result:  func(in clearLogsIn, _ any) projectAck { return projectAck{OK: true, Project: in.Project} },
			Summary: func(in clearLogsIn, _ projectAck) string { return "Logs of " + in.Project + " cleared." },
		}),
		define(spec[cleanLogsIn, cleanAck]{
			Name: "clean_old_logs", Title: "Delete old logs",
			Doc: Doc{
				What:    "Delete the stored log lines of runs older than a number of days, in every project.",
				UseWhen: "the database grows and old logs are not needed.",
				NotFor:  "clearing one project (use clear_project_logs).",
				Returns: "ok and the retention_days applied.",
				Effects: deleteFx,
				Example: `{"retention_days":30,"confirm":true}`,
			},
			Input: func(props map[string]*jsonschema.Schema) { props["retention_days"].Minimum = fptr(1) },
			Level: LevelAdmin, Annotations: gone, Covers: []string{"CleanOldLogs"},
			Translate: func(in cleanLogsIn) (string, any, error) {
				if in.RetentionDays < 1 {
					return "", nil, fmt.Errorf("retention_days must be at least 1, e.g. 30")
				}
				if !in.Confirm {
					return "", nil, fmt.Errorf("clean_old_logs deletes log lines of every project older than %d days for good: call it again with confirm=true if that is intended", in.RetentionDays)
				}
				return "/rpc", rpcCallBody("CleanOldLogs", map[string]int64{"retention_days": in.RetentionDays}), nil
			},
			Result: func(in cleanLogsIn, _ any) cleanAck { return cleanAck{OK: true, RetentionDays: in.RetentionDays} },
			Summary: func(in cleanLogsIn, _ cleanAck) string {
				return fmt.Sprintf("Logs older than %d days deleted.", in.RetentionDays)
			},
		}),
		define(spec[topActionsIn, actionsOut]{
			Name: "get_top_actions", Title: "List most frequent actions",
			Doc: Doc{
				What:    "List the tool calls a project's sessions repeat most, grouped by signature, with error rate and token weight.",
				UseWhen: "you look for repeated work worth a permission rule, a skill or a shortcut.",
				NotFor:  "where time or tokens went (use get_duration_profile or get_token_attribution).",
				Returns: "actions, with next_cursor and truncated when more exist; empty unless experience tracking was on.",
				Effects: "none, read only.",
				Example: `{"project":"lumen","days":30}`,
			},
			Input: tuneAll(tunePage, func(props map[string]*jsonschema.Schema) {
				d := props["days"]
				d.Default, d.Minimum, d.Maximum = json.RawMessage(`30`), fptr(1), fptr(days)
			}),
			Level: LevelRead, Annotations: ro, Covers: []string{"GetTopActions"},
			Translate: func(in topActionsIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if _, _, err := in.pageIn.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetTopActions", map[string]any{"project": in.Project, "days": orDefault(in.Days, 30)}), nil
			},
			Result: func(in topActionsIn, raw any) actionsOut {
				page, p := listPage(in.pageIn, raw)
				return actionsOut{Actions: page, pageOut: p}
			},
			Summary: func(in topActionsIn, out actionsOut) string {
				return plural(len(out.Actions), "action signature", "action signatures") + " in " + in.Project + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[actionSamplesIn, samplesOut]{
			Name: "get_action_samples", Title: "List calls behind an action",
			Doc: Doc{
				What:    "List concrete calls that make up one action signature of a project.",
				UseWhen: "get_top_actions shows a signature and you want to see what it covers.",
				NotFor:  "the signature table itself (use get_top_actions).",
				Returns: "samples, with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{"project":"lumen","signature":"Bash:go test"}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetActionSamples"},
			Translate: func(in actionSamplesIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if in.Signature == "" {
					return "", nil, fmt.Errorf("signature is required: take the Sig value of a get_top_actions row")
				}
				n, err := fetchWindow(in.pageIn)
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetActionSamples", map[string]any{"project": in.Project, "sig": in.Signature, "limit": n}), nil
			},
			Result: func(in actionSamplesIn, raw any) samplesOut {
				page, p := listPage(in.pageIn, raw)
				return samplesOut{Samples: page, pageOut: p}
			},
			Summary: func(_ actionSamplesIn, out samplesOut) string {
				return plural(len(out.Samples), "sample", "samples") + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[durationIn, durationOut]{
			Name: "get_duration_profile", Title: "Get action durations",
			Doc: Doc{
				What:    "Show how long each kind of tool call takes in a project: median, 90th percentile, maximum, total and failure rate.",
				UseWhen: "you look for where a project's run time goes.",
				NotFor:  "token cost (use get_token_attribution) or call counts (use get_top_actions).",
				Returns: "durations, with next_cursor and truncated when more exist; empty unless experience tracking was on.",
				Effects: "none, read only.",
				Example: `{"project":"lumen"}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetDurationProfile"},
			Translate: func(in durationIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if _, _, err := in.pageIn.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetDurationProfile", map[string]string{"project": in.Project}), nil
			},
			Result: func(in durationIn, raw any) durationOut {
				page, p := listPage(in.pageIn, raw)
				return durationOut{Durations: page, pageOut: p}
			},
			Summary: func(in durationIn, out durationOut) string {
				return plural(len(out.Durations), "timed signature", "timed signatures") + " in " + in.Project + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[attributionIn, attributionOut]{
			Name: "get_token_attribution", Title: "Get token cost by tool",
			Doc: Doc{
				What:    "Estimate which tool calls fill a project's context: tokens per signature, per tool and per result length.",
				UseWhen: "you look for the calls that cost the most context, to trim or replace them.",
				NotFor:  "dollar cost (use get_cost) or timing (use get_duration_profile).",
				Returns: "report with the totals and the three tables; empty unless experience tracking was on.",
				Effects: "none, read only.",
				Example: `{"project":"lumen","top_n":10}`,
			},
			Input: func(props map[string]*jsonschema.Schema) {
				n := props["top_n"]
				n.Default, n.Minimum, n.Maximum = json.RawMessage(`20`), fptr(1), fptr(100)
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetTokenAttribution"},
			Translate: func(in attributionIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetTokenAttribution", map[string]any{"project": in.Project, "top_n": orDefault(in.TopN, 20)}), nil
			},
			Result:  func(_ attributionIn, raw any) attributionOut { return attributionOut{Report: raw} },
			Summary: func(in attributionIn, _ attributionOut) string { return "Token attribution of " + in.Project + "." },
		}),
		define(spec[adviceIn, adviceOut]{
			Name: "get_model_advice", Title: "Get model recommendation",
			Doc: Doc{
				What:    "Say whether automatic model routing is on and which model and effort it recommends for a configured session's prompt.",
				UseWhen: "you pick a model for a session before start_session or set_session_model.",
				NotFor:  "changing the model (use set_session_model) or analysing a new task (use run_preflight).",
				Returns: "auto_routing and recommendation; recommendation is null when routing is off or the session has no prompt.",
				Effects: "none, read only.",
				Example: `{"session_id":"lumen/P1"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetAutoModelRouting", "GetModelRecommendation"},
			Translate: func(in adviceIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				project, name, _ := strings.Cut(id, "/")
				return "/rpc", rpcCallBody("GetModelAdvice", map[string]string{"project": project, "session": name}), nil
			},
			Result: func(_ adviceIn, raw any) adviceOut {
				m, _ := raw.(map[string]any)
				out := adviceOut{Recommendation: m["recommendation"]}
				out.AutoRouting, _ = m["auto_routing"].(bool)
				return out
			},
			Summary: func(_ adviceIn, out adviceOut) string {
				if !out.AutoRouting {
					return "Auto model routing is off."
				}
				if out.Recommendation == nil {
					return "Auto model routing is on; the session has no prompt to analyse."
				}
				return "Model recommendation ready."
			},
		}),
	}
}
