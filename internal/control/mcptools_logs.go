package control

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"claude-manager/internal/experience"
	"claude-manager/internal/store"
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

type wasteIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
}

type refReportIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
}

type taskLabelsIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
}

type taskLabelsOut struct {
	Report any `json:"report" jsonschema:"Project, Total, Tokens (what all the judgements cost), Counts (every label with its Count) and Recent (newest first: Project, TaskPtr, RunID, Label, Reason, Model, Tokens, CreatedAt)"`
}

type replayCaseIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	CaseID  int64  `json:"case_id" jsonschema:"Reference case ID, the CaseID of an item of get_reference_report, e.g. 3"`
	Model   string `json:"model,omitempty" jsonschema:"Model to replay with, e.g. sonnet (default: the one-shot runner default)"`
	Effort  string `json:"effort,omitempty" jsonschema:"Effort to replay with: low, medium, high (default: medium)"`
}

type benchConfigIn struct {
	Name     string `json:"name,omitempty" jsonschema:"Label of the configuration in the report, e.g. opus-high; derived from the fields when empty. Must be unique in the run"`
	Model    string `json:"model,omitempty" jsonschema:"Model to run with, e.g. sonnet (default: the one-shot runner default)"`
	Effort   string `json:"effort,omitempty" jsonschema:"Effort to run with: low, medium, high (default: medium)"`
	NoSkills bool   `json:"no_skills,omitempty" jsonschema:"Remove .claude/skills from the run folder (skills off)"`
	NoBans   bool   `json:"no_bans,omitempty" jsonschema:"Run without the PreToolUse ban hook (bans off); by default the project's active hook rules are installed"`
	NoGates  bool   `json:"no_gates,omitempty" jsonschema:"One attempt, no rework rounds on red gates (strict check off); the gates still decide passed or not"`
}

type benchTaskIn struct {
	ID         string   `json:"id,omitempty" jsonschema:"Label of the task in the report; task-N when empty. Must be unique in the run"`
	Text       string   `json:"text" jsonschema:"The task text given to the model as the prompt"`
	BaseCommit string   `json:"base_commit" jsonschema:"Commit of the project's repository to start from; must exist"`
	Gates      []string `json:"gates" jsonschema:"Shell commands, in order, that must all pass for the result to count, e.g. go test ./..."`
}

type runBenchmarkIn struct {
	Project string          `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	Name    string          `json:"name,omitempty" jsonschema:"Label of this run, e.g. bans-on-vs-off"`
	Configs []benchConfigIn `json:"configs" jsonschema:"Configurations to compare; the first is the baseline the others are compared with. At least one, usually two or more"`
	CaseIDs []int64         `json:"case_ids,omitempty" jsonschema:"Reference cases to run, the CaseID of items of get_reference_report. Empty with no tasks = every usable case of the project"`
	Tasks   []benchTaskIn   `json:"tasks,omitempty" jsonschema:"An external suite instead of reference cases"`
	Repeats int             `json:"repeats,omitempty" jsonschema:"Runs of every task under every configuration (default 3). One repeat has no spread, so every difference is reported as indistinguishable"`
	Rounds  int             `json:"rounds,omitempty" jsonschema:"Rework rounds allowed after the first attempt for a configuration with gates on (default 2)"`
}

type benchReportIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	RunID   int64  `json:"run_id,omitempty" jsonschema:"Benchmark run, the RunID run_benchmark returned; 0 or omitted = the project's latest run"`
}

type benchReportOut struct {
	Report any `json:"report" jsonschema:"RunID, Status (running, done, interrupted), Note, Repeats, PlannedTrials, StoredTrials, Tasks, Baseline (the first configuration), Warnings, Configs (per configuration: Trials, Errors, Passed, PassRate, and Rounds (to green), Tokens, DurationSec each as N, Mean, Min, Max, StdDev; Repeats with the same figures per repeat), Comparisons (each metric of each configuration against the baseline: Verdict differs, within_noise or indistinguishable, Direction, Note) and Trials (every cell). There is no overall score"`
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

type refReportOut struct {
	Report any `json:"report" jsonschema:"Project and Cases; each case has CaseID, TaskPtr, BaseCommit, Gates, InvalidReason (set when the case can no longer be replayed) and Runs: the original first (Original true), then replays, each with Model, Effort, Passed, Rounds, Tokens, DurationSec, ResultCommit, Note, At"`
}

type replayCaseOut struct {
	Replay any `json:"replay" jsonschema:"The stored replay: ID, CaseID, Model, Effort, Passed (gates green), Rounds (rework rounds it took), Tokens, DurationSec, ResultCommit (pinned under refs/cm-replay/, empty if nothing changed), Note"`
}

type wasteOut struct {
	Report any `json:"report" jsonschema:"TotalCalls, TotalLifetimeTokens, WastedCalls, WastedLifetimeTokens, WastedShare and Categories (Category, Count, EstTokens, LifetimeTokens, Share, up to 5 Examples with Session, RunID, Step, Tool, Arg), most expensive first"`
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
				NotFor:  "one session (use get_session_metrics) or the rate-limit status (use get_rate_limit).",
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
		define(spec[wasteIn, wasteOut]{
			Name: "get_waste_report", Title: "Get waste report",
			Doc: Doc{
				What:    "Find work in a project that succeeded and still bought nothing: reads repeated with no edit between, streaks of empty searches, whole-file reads, commands rerun unchanged. Per category: count, estimated tokens, share of the project's volume and examples.",
				UseWhen: "you look for what to cut from agents' habits, after get_token_attribution shows where the context goes.",
				NotFor:  "errors (use get_failure_rules) or the heaviest calls as such (use get_token_attribution).",
				Returns: "report with totals and the categories that occurred; an empty Categories list means no waste found; empty unless experience tracking was on.",
				Effects: "none, read only.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetWasteReport"},
			Translate: func(in wasteIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetWasteReport", map[string]string{"project": in.Project}), nil
			},
			Result:  func(_ wasteIn, raw any) wasteOut { return wasteOut{Report: raw} },
			Summary: func(in wasteIn, _ wasteOut) string { return "Waste report of " + in.Project + "." },
		}),
		define(spec[taskLabelsIn, taskLabelsOut]{
			Name: "get_task_labels", Title: "Get quality labels of failed tasks",
			Doc: Doc{
				What:    "List why a project's tasks that ended badly went wrong: each carries one label - tests_fitted_to_code, protocol_broken, garbage_left or wrong_task_done - with a reason, the judging model and the tokens the judgement cost; plus the count per label.",
				UseWhen: "you ask what kind of mistake the failed tasks share, before tightening a rule or a gate.",
				NotFor:  "whether a task succeeded (the gates and get_reference_report say that) or the spend of sessions (use get_cost).",
				Returns: "report with Counts per label and the newest labels with reasons; empty until [optimization] quality_labels is on and a task ends badly (gates red after the last rework round, or unfinished until the loop stopped).",
				Effects: "none.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetTaskLabels"},
			Translate: func(in taskLabelsIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetTaskLabels", map[string]string{"project": in.Project}), nil
			},
			Result:  func(_ taskLabelsIn, raw any) taskLabelsOut { return taskLabelsOut{Report: raw} },
			Summary: func(in taskLabelsIn, _ taskLabelsOut) string { return "Quality labels of " + in.Project + "." },
		}),
		define(spec[refReportIn, refReportOut]{
			Name: "get_reference_report", Title: "Get reference cases report",
			Doc: Doc{
				What:    "List a project's reference cases - finished tasks kept with their text, starting commit, gates and cost - and, per task, every model/setting that has run it: whether the gates went green, in how many rework rounds, with how many tokens and how much time.",
				UseWhen: "you compare models or settings on the same task, or look up a case_id for replay_reference_case.",
				NotFor:  "running a case again (use replay_reference_case) or the cost of ordinary sessions (use get_cost).",
				Returns: "report with one entry per task; an empty Cases list means no task was recorded (needs [optimization] reference_cases and the project's gates); a case whose starting commit is gone is listed with InvalidReason.",
				Effects: "none, apart from marking a case invalid when its starting commit is provably gone.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetReferenceReport"},
			Translate: func(in refReportIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetReferenceReport", map[string]string{"project": in.Project}), nil
			},
			Result:  func(_ refReportIn, raw any) refReportOut { return refReportOut{Report: raw} },
			Summary: func(in refReportIn, _ refReportOut) string { return "Reference cases of " + in.Project + "." },
		}),
		define(spec[replayCaseIn, replayCaseOut]{
			Name: "replay_reference_case", Title: "Replay a reference case",
			Doc: Doc{
				What:    "Run a stored reference case again with another model or effort: a detached worktree at the case's starting commit, the task text as the prompt, the same gates, up to two rework rounds on red gates. The outcome is stored next to the original.",
				UseWhen: "you want the same task's result under a different model or setting, to compare with get_reference_report.",
				NotFor:  "work on the project itself - a replay changes no project branch and its result is only pinned under refs/cm-replay/.",
				Returns: "the stored replay: whether the gates went green, rework rounds, tokens, seconds and the pinned result commit; fails if the case is invalid or the model could not be run.",
				Effects: "runs a model that spends tokens for minutes; creates and removes a temporary worktree of the project's repository; adds a ref under refs/cm-replay/.",
				Example: `{"project":"lumen","case_id":3,"model":"sonnet"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"ReplayReferenceCase"},
			Translate: func(in replayCaseIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if in.CaseID <= 0 {
					return "", nil, fmt.Errorf("case_id is required: take the CaseID of an item of get_reference_report")
				}
				return "/rpc", rpcCallBody("ReplayReferenceCase", map[string]any{
					"project": in.Project, "case_id": in.CaseID, "model": in.Model, "effort": in.Effort,
				}), nil
			},
			Result: func(_ replayCaseIn, raw any) replayCaseOut { return replayCaseOut{Replay: raw} },
			Summary: func(in replayCaseIn, _ replayCaseOut) string {
				return fmt.Sprintf("Case %d of %s replayed; compare with get_reference_report.", in.CaseID, in.Project)
			},
		}),
		define(spec[runBenchmarkIn, benchReportOut]{
			Name: "run_benchmark", Title: "Run a benchmark",
			Doc: Doc{
				What:    "Run a suite of tasks (reference cases or your own) under several configurations - model, effort, skills, bans, gate-driven rework - several times each, every trial in a throwaway worktree at the task's starting commit, judged by its gates. Inputs are frozen with the run.",
				UseWhen: "you ask whether a setting, ban, skill or stricter check changes the outcome on the same work, with repeats to tell it from noise.",
				NotFor:  "one case under one model (use replay_reference_case) or work on the project itself.",
				Returns: "the report of the just-started run (RunID, PlannedTrials); trials run in the background, poll get_benchmark_report until Status is done. A bad request (empty suite, vanished commit, duplicate name, too many trials) is refused with nothing spent.",
				Effects: "runs a model tasks x configurations x repeats times, spending the usage window; makes and removes temporary worktrees, adds refs under refs/cm-replay/; one run at a time; needs [optimization] benchmark = true.",
				Example: `{"project":"lumen","configs":[{"name":"on"},{"name":"off","no_bans":true}]}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"RunBenchmark"},
			Input: func(props map[string]*jsonschema.Schema) {
				props["repeats"].Default = json.RawMessage("3")
				props["rounds"].Default = json.RawMessage("2")
			},
			Translate: func(in runBenchmarkIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if len(in.Configs) == 0 {
					return "", nil, fmt.Errorf("configs is required: at least one configuration, the first is the baseline")
				}
				configs := make([]store.BenchConfig, 0, len(in.Configs))
				for _, c := range in.Configs {
					configs = append(configs, store.BenchConfig(c))
				}
				tasks := make([]store.BenchTask, 0, len(in.Tasks))
				for _, t := range in.Tasks {
					tasks = append(tasks, store.BenchTask{ID: t.ID, Text: t.Text, BaseCommit: t.BaseCommit, Gates: t.Gates})
				}
				return "/rpc", rpcCallBody("RunBenchmark", map[string]any{
					"project": in.Project,
					"spec": experience.BenchmarkSpec{Name: in.Name, CaseIDs: in.CaseIDs, Tasks: tasks,
						Configs: configs, Repeats: in.Repeats, Rounds: in.Rounds},
				}), nil
			},
			Result: func(_ runBenchmarkIn, raw any) benchReportOut { return benchReportOut{Report: raw} },
			Summary: func(in runBenchmarkIn, _ benchReportOut) string {
				return "Benchmark of " + in.Project + " started; poll get_benchmark_report."
			},
		}),
		define(spec[benchReportIn, benchReportOut]{
			Name: "get_benchmark_report", Title: "Get a benchmark report",
			Doc: Doc{
				What:    "Report a benchmark run metric by metric: per configuration the share of trials that passed, rounds to green, tokens and seconds, each with mean, range and standard deviation, plus the same per repeat; and each configuration against the first as differs, within_noise or indistinguishable. There is no single score - the weights are yours.",
				UseWhen: "you wait for a run_benchmark to finish or compare what its configurations did.",
				NotFor:  "starting a run (use run_benchmark) or one case's replays (use get_reference_report).",
				Returns: "report; Status running or interrupted means the figures are partial (Warnings says so); a single repeat marks every comparison indistinguishable; an empty report (RunID 0) means the project has no runs. Trials that could not be run at all are counted in Errors and left out of every figure.",
				Effects: "none. Works with the feature off.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetBenchmarkReport"},
			Translate: func(in benchReportIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetBenchmarkReport", map[string]any{"project": in.Project, "run_id": in.RunID}), nil
			},
			Result: func(_ benchReportIn, raw any) benchReportOut { return benchReportOut{Report: raw} },
			Summary: func(in benchReportIn, _ benchReportOut) string {
				return "Benchmark report of " + in.Project + "."
			},
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
