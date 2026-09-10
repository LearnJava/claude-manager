package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"claude-manager/internal/optimization"

	// Pure-Go SQLite driver (registers driver name "sqlite"). Unlike
	// github.com/mattn/go-sqlite3 it needs no cgo/C toolchain, so history and
	// logs work in a CGO_ENABLED=0 build too.
	_ "modernc.org/sqlite"
)

// SessionRun represents a row in session_runs.
type SessionRun struct {
	ID                  int64
	Project             string
	Session             string
	CLISessionID        string
	Model               string
	StartedAt           time.Time
	FinishedAt          *time.Time
	Status              string // "completed" | "error" | "stopped" | "rate_limited"
	TasksDone           int
	ExitCode            *int
	ErrorMsg            string
	TotalCostUSD        float64
	InputTokens         int64
	OutputTokens        int64
	CacheReadTokens     int64
	CacheCreationTokens int64
	NumTurns            int
	DurationMs          int64
	Effort              string // LEARN-TASKS.md LN-13: set from SessionConfig.Effort at run start
}

// LogEntry represents a row in session_logs.
type LogEntry struct {
	ID        int64
	RunID     int64
	Timestamp time.Time
	Level     string // "text" | "tool" | "error" | "result" | "system"
	Message   string
	ToolName  string
	ToolInput string
}

// DailyMetrics represents a row in daily_metrics.
type DailyMetrics struct {
	Date                     string // "2026-05-22"
	Project                  string
	TotalCost                float64
	TotalInputTokens         int64
	TotalOutputTokens        int64
	TotalCacheReadTokens     int64
	TotalCacheCreationTokens int64
	TotalRuns                int
	TotalTasks               int
}

// TaskPlan represents a row in task_plans.
type TaskPlan struct {
	ID           int64
	Project      string
	OriginalTask string
	AnalysisJSON string
	Status       string // "draft" | "approved" | "executing" | "completed"
	Kind         string // "adhoc" | "roadmap"
	CreatedAt    time.Time
	CompletedAt  *time.Time
	TotalCostUSD *float64
	TotalTokens  *int64
}

// PlanSubtask represents a row in plan_subtasks.
type PlanSubtask struct {
	ID            int64
	PlanID        int64
	SubtaskID     string
	Name          string
	Prompt        string
	DependsOn     string // JSON array of subtask IDs
	Model         string
	SessionRunID  *int64
	Status        string
	ResultSummary string
	FilesChanged  string // JSON array
}

// MixedBrief represents a row in mixed_briefs (MIXED-TASKS.md MP-06).
type MixedBrief struct {
	ID        int64
	BriefID   string // stable external ID (worker.Brief.ID)
	Project   string
	Task      string
	Files     string // JSON array of files the brief targets
	CreatedAt time.Time
}

// Skill represents a row in `skills` — a distilled procedure candidate
// (LEARN-TASKS.md LN-09/10/11). Status is draft (just distilled, not yet
// written into the project), approved (written to
// <project>/.claude/skills/<name>/SKILL.md by LN-10) or archived (rejected or
// protuhla per LN-11). DraftJSON is the SkillDraft the distiller produced; MD
// is its rendered form (analysis.RenderSkillMarkdown) — kept alongside the
// JSON so LN-10 can offer the exact text for review/edit without re-rendering.
// SourceJSON is the candidate's signature sequence (LN-08's
// SkillCandidate.Sig) — LN-11 uses it to check whether this skill's pattern
// still occurs in later runs.
type Skill struct {
	ID         int64
	Project    string
	Name       string
	Status     string // draft | approved | archived
	DraftJSON  string
	MD         string
	SourceJSON string
	CreatedAt  time.Time
	ApprovedAt *time.Time
	ArchivedAt *time.Time
}

// ActionRow represents a row in action_signatures — one normalized tool call
// mined from a CLI transcript (LEARN-TASKS.md LN-01/LN-02). RunID is nil when
// the row was ingested from a transcript with no matching session_runs row
// yet (e.g. an in-flight run).
type ActionRow struct {
	ID           int64
	Project      string
	Session      string
	RunID        *int64
	CLISessionID string
	TaskPtr      string
	StepIndex    int
	Tool         string
	Sig          string
	Arg          string
	IsError      bool
	OutTokens    int64
	ResultChars  int
	// DurSec is the tool_use→tool_result gap in whole seconds (LEARN-TASKS.md
	// LN-18); 0 means unknown, not instant — a call whose result never
	// arrived within the ingested window.
	DurSec    int64
	Timestamp time.Time
}

// SignatureStat aggregates action_signatures rows sharing the same (project,
// sig) — the row shape behind the "Actions" tab (LN-03) and the input to
// downstream candidate mining (LN-04 permission rules, LN-07/08 skill
// promotion).
type SignatureStat struct {
	Sig          string
	Tool         string
	Count        int
	DistinctRuns int
	ErrorRate    float64
	SumOutTokens int64
	SampleArgs   []string
	FirstSeen    time.Time
	LastSeen     time.Time
}

// PermissionEvent represents a row in permission_events — one resolved
// permission_request, whether a config/runtime rule or bypassPermissions
// decided it automatically (Auto=true) or a human answered it (Auto=false)
// (LEARN-TASKS.md LN-04). RunID is nil when no run was in flight (mirrors
// ActionRow.RunID).
type PermissionEvent struct {
	ID        int64
	Project   string
	Session   string
	RunID     *int64
	Tool      string
	Pattern   string
	Decision  string
	Auto      bool
	Timestamp time.Time
}

// PermissionEventStat aggregates permission_events sharing the same (project,
// tool, pattern) — the input to the permission-rule classifier (LN-04):
// AllowCount/DenyCount tell whether a repeated ask was consistently approved,
// which is a precondition for suggesting an auto-allow rule.
type PermissionEventStat struct {
	Tool       string
	Pattern    string
	Count      int
	AllowCount int
	DenyCount  int
	FirstSeen  time.Time
	LastSeen   time.Time
}

// Store wraps a SQLite database and provides CRUD for all tables.
type Store struct {
	db *sql.DB
}

// New opens the SQLite database at dbPath, runs migrations, and returns a Store.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)")
	if err != nil {
		return nil, fmt.Errorf("store open: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("store migrate: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// --- session_runs ---

const selectRunCols = `id, project, session, cli_session_id, model, started_at, finished_at, status,
    tasks_done, exit_code, error_msg, total_cost_usd, input_tokens, output_tokens,
    cache_read_tokens, cache_creation_tokens, num_turns, duration_ms, effort`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRun(row rowScanner) (*SessionRun, error) {
	var r SessionRun
	var finishedAt sql.NullTime
	var exitCode sql.NullInt64
	var effort sql.NullString
	err := row.Scan(
		&r.ID, &r.Project, &r.Session, &r.CLISessionID, &r.Model,
		&r.StartedAt, &finishedAt, &r.Status,
		&r.TasksDone, &exitCode, &r.ErrorMsg,
		&r.TotalCostUSD, &r.InputTokens, &r.OutputTokens,
		&r.CacheReadTokens, &r.CacheCreationTokens, &r.NumTurns, &r.DurationMs,
		&effort,
	)
	if err != nil {
		return nil, err
	}
	if finishedAt.Valid {
		t := finishedAt.Time
		r.FinishedAt = &t
	}
	if exitCode.Valid {
		v := int(exitCode.Int64)
		r.ExitCode = &v
	}
	if effort.Valid {
		r.Effort = effort.String
	}
	return &r, nil
}

// InsertRun inserts a new SessionRun and sets run.ID to the generated row ID.
func (s *Store) InsertRun(run *SessionRun) error {
	const q = `INSERT INTO session_runs
    (project, session, cli_session_id, model, started_at, finished_at, status,
     tasks_done, exit_code, error_msg, total_cost_usd, input_tokens, output_tokens,
     cache_read_tokens, cache_creation_tokens, num_turns, duration_ms, effort)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := s.db.Exec(q,
		run.Project, run.Session, run.CLISessionID, run.Model,
		run.StartedAt, nullTime(run.FinishedAt), run.Status,
		run.TasksDone, nullIntPtr(run.ExitCode), run.ErrorMsg,
		run.TotalCostUSD, run.InputTokens, run.OutputTokens,
		run.CacheReadTokens, run.CacheCreationTokens, run.NumTurns, run.DurationMs,
		nullStr(run.Effort),
	)
	if err != nil {
		return err
	}
	run.ID, err = res.LastInsertId()
	return err
}

// UpdateRun updates all mutable fields of an existing SessionRun by ID.
func (s *Store) UpdateRun(run *SessionRun) error {
	const q = `UPDATE session_runs SET
    finished_at=?, status=?, tasks_done=?, exit_code=?, error_msg=?,
    total_cost_usd=?, input_tokens=?, output_tokens=?, cache_read_tokens=?,
    cache_creation_tokens=?, num_turns=?, duration_ms=?, model=?, effort=?
WHERE id=?`
	_, err := s.db.Exec(q,
		nullTime(run.FinishedAt), run.Status, run.TasksDone, nullIntPtr(run.ExitCode), run.ErrorMsg,
		run.TotalCostUSD, run.InputTokens, run.OutputTokens, run.CacheReadTokens,
		run.CacheCreationTokens, run.NumTurns, run.DurationMs, run.Model, nullStr(run.Effort),
		run.ID,
	)
	return err
}

// GetRun returns a SessionRun by ID, or nil if not found.
func (s *Store) GetRun(id int64) (*SessionRun, error) {
	q := `SELECT ` + selectRunCols + ` FROM session_runs WHERE id=?`
	r, err := scanRun(s.db.QueryRow(q, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// ListRuns returns up to limit runs for the given project and/or session, newest first.
// Pass empty strings to skip that filter; pass limit<=0 for no limit.
func (s *Store) ListRuns(project, session string, limit int) ([]*SessionRun, error) {
	var where []string
	var args []any

	if project != "" {
		where = append(where, "project=?")
		args = append(args, project)
	}
	if session != "" {
		where = append(where, "session=?")
		args = append(args, session)
	}

	q := "SELECT " + selectRunCols + " FROM session_runs"
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += " ORDER BY started_at DESC"
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*SessionRun
	for rows.Next() {
		r, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// OutcomeStats implements optimization.OutcomeProvider for the real app:
// one row per (model, effort) actually used in the project, aggregated from
// finished session_runs (LEARN-TASKS.md LN-13).
//
// `complexity` is accepted for interface conformance and echoed back onto
// every returned row as a label, but it does not filter the query:
// session_runs has no per-run complexity tag. The analyst's
// estimated_complexity for a given prompt is computed ad hoc by
// GetModelRecommendation (app.go) and never persisted against the run it
// eventually starts, and task_plans (the one table that does carry
// estimated_complexity) has no foreign key into session_runs — a plan
// executed via ExecutePlan runs through analysis.CLIExecutor's one-shot
// `claude -p`, not through SessionManager, so it never produces a
// session_runs row at all (plan_subtasks.session_run_id exists in the schema
// but is currently always nil). Aggregating per (model, effort) project-wide
// is the coarser, honest alternative: ModelRouter.Route's own tier-ordering,
// minimum-run-count and cost-threshold checks are what keep an override safe
// despite the coarser grain, not this query.
func (s *Store) OutcomeStats(project string, complexity optimization.Complexity) ([]optimization.OutcomeStats, error) {
	const q = `SELECT model, effort, COUNT(*),
    SUM(CASE WHEN status='completed' THEN 1 ELSE 0 END),
    AVG(total_cost_usd), AVG(num_turns)
FROM session_runs
WHERE project=? AND finished_at IS NOT NULL AND model IS NOT NULL AND model != ''
GROUP BY model, effort`
	rows, err := s.db.Query(q, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []optimization.OutcomeStats
	for rows.Next() {
		var model string
		var effort sql.NullString
		var runs, completed int
		var avgCost, avgTurns float64
		if err := rows.Scan(&model, &effort, &runs, &completed, &avgCost, &avgTurns); err != nil {
			return nil, err
		}
		out = append(out, optimization.OutcomeStats{
			Project:    project,
			Complexity: complexity,
			Model:      model,
			Effort:     effort.String,
			Runs:       runs,
			Completed:  completed,
			AvgCostUSD: avgCost,
			AvgTurns:   avgTurns,
		})
	}
	return out, rows.Err()
}

// --- session_logs ---

// InsertLogs batch-inserts log entries for a run in a single transaction.
func (s *Store) InsertLogs(runID int64, logs []LogEntry) error {
	if len(logs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(
		`INSERT INTO session_logs (run_id, timestamp, level, message, tool_name, tool_input)
         VALUES (?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, l := range logs {
		if _, err := stmt.Exec(runID, l.Timestamp, l.Level, l.Message,
			nullStr(l.ToolName), nullStr(l.ToolInput)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetLogs returns log entries for a run with offset/limit pagination, oldest first.
func (s *Store) GetLogs(runID int64, offset, limit int) ([]*LogEntry, error) {
	const q = `SELECT id, run_id, timestamp, level, message, tool_name, tool_input
    FROM session_logs WHERE run_id=? ORDER BY id ASC LIMIT ? OFFSET ?`
	rows, err := s.db.Query(q, runID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*LogEntry
	for rows.Next() {
		var l LogEntry
		var toolName, toolInput sql.NullString
		if err := rows.Scan(&l.ID, &l.RunID, &l.Timestamp, &l.Level, &l.Message,
			&toolName, &toolInput); err != nil {
			return nil, err
		}
		l.ToolName = toolName.String
		l.ToolInput = toolInput.String
		logs = append(logs, &l)
	}
	return logs, rows.Err()
}

// DeleteOldLogs removes log entries whose run started more than retentionDays days ago.
func (s *Store) DeleteOldLogs(retentionDays int) error {
	const q = `DELETE FROM session_logs WHERE run_id IN (
        SELECT id FROM session_runs WHERE started_at < datetime('now', ?)
    )`
	_, err := s.db.Exec(q, fmt.Sprintf("-%d days", retentionDays))
	return err
}

// DeleteLogsForProject removes all session_logs rows belonging to a project's
// runs. Used by the "clear project logs" button alongside
// ClearProjectLogFiles — it only clears log bodies, not the session_runs
// history rows themselves (History/Dashboard keep showing past runs).
func (s *Store) DeleteLogsForProject(project string) error {
	const q = `DELETE FROM session_logs WHERE run_id IN (
        SELECT id FROM session_runs WHERE project=?
    )`
	_, err := s.db.Exec(q, project)
	return err
}

// --- daily_metrics ---

// AddDailyMetrics upserts daily aggregate metrics, adding the delta to any existing row.
func (s *Store) AddDailyMetrics(m *DailyMetrics) error {
	const q = `INSERT INTO daily_metrics
    (date, project, total_cost, total_input_tokens, total_output_tokens,
     total_cache_read_tokens, total_cache_creation_tokens, total_runs, total_tasks)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(date, project) DO UPDATE SET
    total_cost                  = total_cost                  + excluded.total_cost,
    total_input_tokens          = total_input_tokens          + excluded.total_input_tokens,
    total_output_tokens         = total_output_tokens         + excluded.total_output_tokens,
    total_cache_read_tokens     = total_cache_read_tokens     + excluded.total_cache_read_tokens,
    total_cache_creation_tokens = total_cache_creation_tokens + excluded.total_cache_creation_tokens,
    total_runs                  = total_runs                  + excluded.total_runs,
    total_tasks                 = total_tasks                 + excluded.total_tasks`
	_, err := s.db.Exec(q,
		m.Date, m.Project, m.TotalCost,
		m.TotalInputTokens, m.TotalOutputTokens,
		m.TotalCacheReadTokens, m.TotalCacheCreationTokens,
		m.TotalRuns, m.TotalTasks,
	)
	return err
}

// GetDailyMetrics returns metrics for a specific date and project, or nil if not found.
func (s *Store) GetDailyMetrics(date, project string) (*DailyMetrics, error) {
	const q = `SELECT date, project, total_cost, total_input_tokens, total_output_tokens,
       total_cache_read_tokens, total_cache_creation_tokens, total_runs, total_tasks
    FROM daily_metrics WHERE date=? AND project=?`
	var m DailyMetrics
	err := s.db.QueryRow(q, date, project).Scan(
		&m.Date, &m.Project, &m.TotalCost,
		&m.TotalInputTokens, &m.TotalOutputTokens,
		&m.TotalCacheReadTokens, &m.TotalCacheCreationTokens,
		&m.TotalRuns, &m.TotalTasks,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// ListDailyMetrics returns metrics for a project over the last days days, newest first.
func (s *Store) ListDailyMetrics(project string, days int) ([]*DailyMetrics, error) {
	const q = `SELECT date, project, total_cost, total_input_tokens, total_output_tokens,
       total_cache_read_tokens, total_cache_creation_tokens, total_runs, total_tasks
    FROM daily_metrics WHERE project=? AND date >= date('now', ?)
    ORDER BY date DESC`
	rows, err := s.db.Query(q, project, fmt.Sprintf("-%d days", days))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var metrics []*DailyMetrics
	for rows.Next() {
		var m DailyMetrics
		if err := rows.Scan(
			&m.Date, &m.Project, &m.TotalCost,
			&m.TotalInputTokens, &m.TotalOutputTokens,
			&m.TotalCacheReadTokens, &m.TotalCacheCreationTokens,
			&m.TotalRuns, &m.TotalTasks,
		); err != nil {
			return nil, err
		}
		metrics = append(metrics, &m)
	}
	return metrics, rows.Err()
}

// --- task_plans ---

func scanPlan(row rowScanner) (*TaskPlan, error) {
	var p TaskPlan
	var completedAt sql.NullTime
	var totalCostUSD sql.NullFloat64
	var totalTokens sql.NullInt64
	err := row.Scan(
		&p.ID, &p.Project, &p.OriginalTask, &p.AnalysisJSON, &p.Status, &p.Kind,
		&p.CreatedAt, &completedAt, &totalCostUSD, &totalTokens,
	)
	if err != nil {
		return nil, err
	}
	if completedAt.Valid {
		t := completedAt.Time
		p.CompletedAt = &t
	}
	if totalCostUSD.Valid {
		p.TotalCostUSD = &totalCostUSD.Float64
	}
	if totalTokens.Valid {
		p.TotalTokens = &totalTokens.Int64
	}
	return &p, nil
}

// InsertPlan inserts a TaskPlan and sets plan.ID to the generated row ID.
func (s *Store) InsertPlan(plan *TaskPlan) error {
	kind := plan.Kind
	if kind == "" {
		kind = "adhoc"
	}
	const q = `INSERT INTO task_plans
    (project, original_task, analysis_json, status, kind, created_at, completed_at, total_cost_usd, total_tokens)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := s.db.Exec(q,
		plan.Project, plan.OriginalTask, plan.AnalysisJSON, plan.Status, kind,
		plan.CreatedAt, nullTime(plan.CompletedAt),
		nullFloat64(plan.TotalCostUSD), nullInt64(plan.TotalTokens),
	)
	if err != nil {
		return err
	}
	plan.ID, err = res.LastInsertId()
	return err
}

// UpdatePlan updates mutable fields of an existing TaskPlan by ID.
func (s *Store) UpdatePlan(plan *TaskPlan) error {
	const q = `UPDATE task_plans SET
    status=?, completed_at=?, total_cost_usd=?, total_tokens=?
WHERE id=?`
	_, err := s.db.Exec(q,
		plan.Status, nullTime(plan.CompletedAt),
		nullFloat64(plan.TotalCostUSD), nullInt64(plan.TotalTokens),
		plan.ID,
	)
	return err
}

// GetPlan returns a TaskPlan by ID, or nil if not found.
func (s *Store) GetPlan(id int64) (*TaskPlan, error) {
	const q = `SELECT id, project, original_task, analysis_json, status, kind, created_at, completed_at, total_cost_usd, total_tokens
    FROM task_plans WHERE id=?`
	p, err := scanPlan(s.db.QueryRow(q, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// ListPlans returns up to limit plans for a project, newest first.
func (s *Store) ListPlans(project string, limit int) ([]*TaskPlan, error) {
	q := `SELECT id, project, original_task, analysis_json, status, kind, created_at, completed_at, total_cost_usd, total_tokens
    FROM task_plans WHERE project=? ORDER BY created_at DESC`
	args := []any{project}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var plans []*TaskPlan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, err
		}
		plans = append(plans, p)
	}
	return plans, rows.Err()
}

// --- plan_subtasks ---

func scanSubtask(row rowScanner) (*PlanSubtask, error) {
	var sub PlanSubtask
	var dependsOn, model, resultSummary, filesChanged sql.NullString
	var sessionRunID sql.NullInt64
	err := row.Scan(
		&sub.ID, &sub.PlanID, &sub.SubtaskID, &sub.Name, &sub.Prompt,
		&dependsOn, &model, &sessionRunID, &sub.Status, &resultSummary, &filesChanged,
	)
	if err != nil {
		return nil, err
	}
	sub.DependsOn = dependsOn.String
	sub.Model = model.String
	sub.ResultSummary = resultSummary.String
	sub.FilesChanged = filesChanged.String
	if sessionRunID.Valid {
		sub.SessionRunID = &sessionRunID.Int64
	}
	return &sub, nil
}

// InsertSubtask inserts a PlanSubtask and sets sub.ID to the generated row ID.
func (s *Store) InsertSubtask(sub *PlanSubtask) error {
	const q = `INSERT INTO plan_subtasks
    (plan_id, subtask_id, name, prompt, depends_on, model, session_run_id, status, result_summary, files_changed)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := s.db.Exec(q,
		sub.PlanID, sub.SubtaskID, sub.Name, sub.Prompt,
		nullStr(sub.DependsOn), nullStr(sub.Model),
		nullInt64(sub.SessionRunID), sub.Status,
		nullStr(sub.ResultSummary), nullStr(sub.FilesChanged),
	)
	if err != nil {
		return err
	}
	sub.ID, err = res.LastInsertId()
	return err
}

// UpdateSubtask updates mutable fields of an existing PlanSubtask by ID.
func (s *Store) UpdateSubtask(sub *PlanSubtask) error {
	const q = `UPDATE plan_subtasks SET
    session_run_id=?, status=?, result_summary=?, files_changed=?
WHERE id=?`
	_, err := s.db.Exec(q,
		nullInt64(sub.SessionRunID), sub.Status,
		nullStr(sub.ResultSummary), nullStr(sub.FilesChanged),
		sub.ID,
	)
	return err
}

// ListSubtasks returns all subtasks for a plan in insertion order.
func (s *Store) ListSubtasks(planID int64) ([]*PlanSubtask, error) {
	const q = `SELECT id, plan_id, subtask_id, name, prompt, depends_on, model, session_run_id, status, result_summary, files_changed
    FROM plan_subtasks WHERE plan_id=? ORDER BY id ASC`
	rows, err := s.db.Query(q, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []*PlanSubtask
	for rows.Next() {
		sub, err := scanSubtask(rows)
		if err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// --- mixed_briefs ---

func scanBrief(row rowScanner) (*MixedBrief, error) {
	var b MixedBrief
	var files sql.NullString
	err := row.Scan(&b.ID, &b.BriefID, &b.Project, &b.Task, &files, &b.CreatedAt)
	if err != nil {
		return nil, err
	}
	b.Files = files.String
	return &b, nil
}

// InsertBrief inserts a MixedBrief and sets b.ID to the generated row ID.
func (s *Store) InsertBrief(b *MixedBrief) error {
	const q = `INSERT INTO mixed_briefs (brief_id, project, task, files, created_at) VALUES (?, ?, ?, ?, ?)`
	res, err := s.db.Exec(q, b.BriefID, b.Project, b.Task, nullStr(b.Files), b.CreatedAt)
	if err != nil {
		return err
	}
	b.ID, err = res.LastInsertId()
	return err
}

// GetBriefByBriefID returns a MixedBrief by its external brief_id, or nil if
// not found.
func (s *Store) GetBriefByBriefID(briefID string) (*MixedBrief, error) {
	const q = `SELECT id, brief_id, project, task, files, created_at FROM mixed_briefs WHERE brief_id=?`
	b, err := scanBrief(s.db.QueryRow(q, briefID))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return b, err
}

// ListBriefs returns up to limit briefs for a project, newest first.
func (s *Store) ListBriefs(project string, limit int) ([]*MixedBrief, error) {
	q := `SELECT id, brief_id, project, task, files, created_at FROM mixed_briefs WHERE project=? ORDER BY created_at DESC`
	args := []any{project}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var briefs []*MixedBrief
	for rows.Next() {
		b, err := scanBrief(rows)
		if err != nil {
			return nil, err
		}
		briefs = append(briefs, b)
	}
	return briefs, rows.Err()
}

// --- skills (LEARN-TASKS.md LN-09/10/11) ---

func scanSkill(row rowScanner) (*Skill, error) {
	var sk Skill
	var approvedAt, archivedAt sql.NullTime
	err := row.Scan(&sk.ID, &sk.Project, &sk.Name, &sk.Status,
		&sk.DraftJSON, &sk.MD, &sk.SourceJSON, &sk.CreatedAt, &approvedAt, &archivedAt)
	if err != nil {
		return nil, err
	}
	if approvedAt.Valid {
		sk.ApprovedAt = &approvedAt.Time
	}
	if archivedAt.Valid {
		sk.ArchivedAt = &archivedAt.Time
	}
	return &sk, nil
}

// InsertSkill inserts a Skill draft and sets sk.ID to the generated row ID.
// Status is expected to be "draft" — the distiller (LEARN-TASKS.md LN-09)
// never writes anything else; approval/archival (LN-10/11) update the row in
// place instead of inserting a new one.
func (s *Store) InsertSkill(sk *Skill) error {
	const q = `INSERT INTO skills (project, name, status, draft_json, md, source_json, created_at)
	    VALUES (?, ?, ?, ?, ?, ?, ?)`
	res, err := s.db.Exec(q, sk.Project, sk.Name, sk.Status, sk.DraftJSON, sk.MD, sk.SourceJSON, sk.CreatedAt)
	if err != nil {
		return err
	}
	sk.ID, err = res.LastInsertId()
	return err
}

// GetSkill returns a Skill by ID, or nil if not found.
func (s *Store) GetSkill(id int64) (*Skill, error) {
	const q = `SELECT id, project, name, status, draft_json, md, source_json, created_at, approved_at, archived_at
	    FROM skills WHERE id=?`
	sk, err := scanSkill(s.db.QueryRow(q, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return sk, err
}

// ListSkills returns every skill row for a project — draft, approved and
// archived alike, newest first — so the "Skills" tab (LEARN-TASKS.md LN-10)
// can filter by status client-side (e.g. show an "already approved —
// overwrite?" banner for a draft whose name collides with an approved one).
func (s *Store) ListSkills(project string) ([]Skill, error) {
	const q = `SELECT id, project, name, status, draft_json, md, source_json, created_at, approved_at, archived_at
	    FROM skills WHERE project=? ORDER BY created_at DESC`
	rows, err := s.db.Query(q, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Skill
	for rows.Next() {
		sk, err := scanSkill(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *sk)
	}
	return out, rows.Err()
}

// UpdateSkillApproved marks a skill row approved (LEARN-TASKS.md LN-10): md
// is the reviewed/possibly-edited body actually written to
// <project>/.claude/skills/<name>/SKILL.md, kept alongside draft_json so a
// later view of the row shows what was really approved, not the original
// distillation.
func (s *Store) UpdateSkillApproved(id int64, md string, approvedAt time.Time) error {
	const q = `UPDATE skills SET status='approved', md=?, approved_at=? WHERE id=?`
	_, err := s.db.Exec(q, md, approvedAt, id)
	return err
}

// UpdateSkillArchived marks a skill row archived (LEARN-TASKS.md LN-10/11) —
// a rejected draft or a skill LN-11 later proposes as stale. Does not touch
// any file already written into the project; archiving only removes the row
// from the active list.
func (s *Store) UpdateSkillArchived(id int64, archivedAt time.Time) error {
	const q = `UPDATE skills SET status='archived', archived_at=? WHERE id=?`
	_, err := s.db.Exec(q, archivedAt, id)
	return err
}

// --- action_signatures / ingest_state (LEARN-TASKS.md LN-02) ---

// InsertActions batch-inserts action rows in a single transaction, mirroring
// InsertLogs — the indexer (LN-03) ingests a transcript's whole unread tail
// per call.
func (s *Store) InsertActions(rows []ActionRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck

	stmt, err := tx.Prepare(
		`INSERT INTO action_signatures
	    (project, session, run_id, cli_session_id, task_ptr, step_index, tool, sig, arg, is_error, out_tokens, result_chars, dur_sec, ts)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, r := range rows {
		isErr := 0
		if r.IsError {
			isErr = 1
		}
		if _, err := stmt.Exec(
			r.Project, r.Session, nullInt64(r.RunID), nullStr(r.CLISessionID),
			nullStr(r.TaskPtr), r.StepIndex, r.Tool, r.Sig, nullStr(r.Arg),
			isErr, r.OutTokens, r.ResultChars, r.DurSec, r.Timestamp,
		); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const selectActionCols = `id, project, session, run_id, cli_session_id, task_ptr, step_index, tool, sig, arg, is_error, out_tokens, result_chars, dur_sec, ts`

// ActionsForRun returns all action rows for one session_runs ID, in step order.
func (s *Store) ActionsForRun(runID int64) ([]*ActionRow, error) {
	q := `SELECT ` + selectActionCols + `
    FROM action_signatures WHERE run_id=? ORDER BY step_index ASC`
	rows, err := s.db.Query(q, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*ActionRow
	for rows.Next() {
		r, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func scanAction(row rowScanner) (*ActionRow, error) {
	var r ActionRow
	var runID sql.NullInt64
	var cliSessionID, taskPtr, arg sql.NullString
	var isErr int
	err := row.Scan(
		&r.ID, &r.Project, &r.Session, &runID, &cliSessionID, &taskPtr,
		&r.StepIndex, &r.Tool, &r.Sig, &arg, &isErr, &r.OutTokens, &r.ResultChars, &r.DurSec, &r.Timestamp,
	)
	if err != nil {
		return nil, err
	}
	if runID.Valid {
		v := runID.Int64
		r.RunID = &v
	}
	r.CLISessionID = cliSessionID.String
	r.TaskPtr = taskPtr.String
	r.Arg = arg.String
	r.IsError = isErr != 0
	return &r, nil
}

// TopSignatures aggregates action_signatures for a project over the last
// sinceDays days, most frequent signature first. Pass limit<=0 for no limit.
//
// DistinctRuns groups by run_id, falling back to cli_session_id when run_id
// is NULL — a bulk-imported row (LEARN-TASKS.md LN-17: IngestDir) never has a
// session_runs row to point at, and plain COUNT(DISTINCT run_id) silently
// ignores every NULL, undercounting (or zeroing) DistinctRuns for a
// signature that only appears in imported history.
func (s *Store) TopSignatures(project string, sinceDays, limit int) ([]SignatureStat, error) {
	q := `SELECT sig, tool, COUNT(*), COUNT(DISTINCT COALESCE(CAST(run_id AS TEXT), cli_session_id)),
       SUM(CASE WHEN is_error THEN 1 ELSE 0 END), SUM(out_tokens), MIN(ts), MAX(ts)
    FROM action_signatures
    WHERE project=? AND ts >= datetime('now', ?)
    GROUP BY sig, tool
    ORDER BY COUNT(*) DESC`
	args := []any{project, fmt.Sprintf("-%d days", sinceDays)}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []SignatureStat
	for rows.Next() {
		var st SignatureStat
		var errCount int
		var firstSeen, lastSeen string
		if err := rows.Scan(&st.Sig, &st.Tool, &st.Count, &st.DistinctRuns,
			&errCount, &st.SumOutTokens, &firstSeen, &lastSeen); err != nil {
			return nil, err
		}
		st.FirstSeen = parseAggTime(firstSeen)
		st.LastSeen = parseAggTime(lastSeen)
		if st.Count > 0 {
			st.ErrorRate = float64(errCount) / float64(st.Count)
		}
		stats = append(stats, st)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range stats {
		samples, err := s.sampleArgs(project, stats[i].Sig, 3)
		if err != nil {
			return nil, err
		}
		stats[i].SampleArgs = samples
	}
	return stats, nil
}

// sampleArgs returns up to n distinct non-empty arg values for a (project,
// sig) pair — enough for a human or a promotion rule (LN-04/07) to eyeball
// what the signature actually covers, without shipping every row's arg.
func (s *Store) sampleArgs(project, sig string, n int) ([]string, error) {
	const q = `SELECT DISTINCT arg FROM action_signatures
    WHERE project=? AND sig=? AND arg IS NOT NULL AND arg != ''
    ORDER BY id DESC LIMIT ?`
	rows, err := s.db.Query(q, project, sig, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var samples []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		samples = append(samples, a)
	}
	return samples, rows.Err()
}

// ActionSamples returns up to limit full action_signatures rows for one
// (project, sig) pair, most recent first — the "Actions" tab's click-through
// from a signature to concrete examples (LEARN-TASKS.md LN-03). Pass
// limit<=0 for no limit.
func (s *Store) ActionSamples(project, sig string, limit int) ([]ActionRow, error) {
	q := `SELECT ` + selectActionCols + `
    FROM action_signatures WHERE project=? AND sig=? ORDER BY id DESC`
	args := []any{project, sig}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ActionRow
	for rows.Next() {
		r, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// DurationRow is one action_signatures row's duration sample — just the
// fields DurationProfile (LEARN-TASKS.md LN-18) aggregates over, not the full
// ActionRow.
type DurationRow struct {
	Sig     string
	Tool    string
	DurSec  int64
	IsError bool
}

// ActionDurations returns every action_signatures row with a known duration
// (dur_sec > 0) for project over the last sinceDays days — the raw sample set
// experience.DurationProfile groups into per-signature statistics. Rows with
// dur_sec == 0 (no result ever arrived) are excluded here rather than by the
// caller, since "unknown" must never silently count as "instant" in a median.
func (s *Store) ActionDurations(project string, sinceDays int) ([]DurationRow, error) {
	const q = `SELECT sig, tool, dur_sec, is_error FROM action_signatures
    WHERE project=? AND dur_sec > 0 AND ts >= datetime('now', ?)`
	rows, err := s.db.Query(q, project, fmt.Sprintf("-%d days", sinceDays))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []DurationRow
	for rows.Next() {
		var r DurationRow
		var isErr int
		if err := rows.Scan(&r.Sig, &r.Tool, &r.DurSec, &isErr); err != nil {
			return nil, err
		}
		r.IsError = isErr != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// ResultCharsRow is one action_signatures row's result size — the raw sample
// experience.BuildAttributionReport (LEARN-TASKS.md LN-12) aggregates into
// per-signature/per-tool token-attribution stats.
type ResultCharsRow struct {
	Sig         string
	Tool        string
	ResultChars int
}

// ActionResultChars returns every action_signatures row's (sig, tool,
// result_chars) for project over the last sinceDays days — including rows
// with result_chars==0 (no result ever captured), unlike ActionDurations:
// those calls still happened and must count toward Count, they simply
// contribute nothing to the token estimate (LEARN-TASKS.md LN-12).
func (s *Store) ActionResultChars(project string, sinceDays int) ([]ResultCharsRow, error) {
	const q = `SELECT sig, tool, result_chars FROM action_signatures
    WHERE project=? AND ts >= datetime('now', ?)`
	rows, err := s.db.Query(q, project, fmt.Sprintf("-%d days", sinceDays))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []ResultCharsRow
	for rows.Next() {
		var r ResultCharsRow
		if err := rows.Scan(&r.Sig, &r.Tool, &r.ResultChars); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// CandidateActionRow pairs an action_signatures row with the status of the
// session_runs row it belongs to (empty when RunID is nil, or the run row no
// longer exists) — ActionRowsForCandidates' own row shape, the raw material
// experience.BuildCandidateRuns groups into experience.CandidateRun for
// MineCandidates (LEARN-TASKS.md LN-08).
type CandidateActionRow struct {
	ActionRow
	RunStatus string
}

// ActionRowsForCandidates returns every action_signatures row for project
// over the last sinceDays days, each paired with its own run's
// session_runs.status via a LEFT JOIN — the input to
// experience.MineProjectCandidates (LEARN-TASKS.md LN-08). A bulk-imported
// row (LN-17, RunID nil) or one whose run_id no longer resolves gets
// RunStatus="", which MineCandidates already treats as neutral evidence, not
// as evidence of success (see outcomeWeight's default case).
func (s *Store) ActionRowsForCandidates(project string, sinceDays int) ([]CandidateActionRow, error) {
	const q = `SELECT a.id, a.project, a.session, a.run_id, a.cli_session_id, a.task_ptr,
       a.step_index, a.tool, a.sig, a.arg, a.is_error, a.out_tokens, a.result_chars, a.dur_sec, a.ts,
       COALESCE(r.status, '')
    FROM action_signatures a
    LEFT JOIN session_runs r ON r.id = a.run_id
    WHERE a.project = ? AND a.ts >= datetime('now', ?)`
	rows, err := s.db.Query(q, project, fmt.Sprintf("-%d days", sinceDays))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []CandidateActionRow
	for rows.Next() {
		var r CandidateActionRow
		var runID sql.NullInt64
		var cliSessionID, taskPtr, arg sql.NullString
		var isErr int
		if err := rows.Scan(
			&r.ID, &r.Project, &r.Session, &runID, &cliSessionID, &taskPtr,
			&r.StepIndex, &r.Tool, &r.Sig, &arg, &isErr, &r.OutTokens, &r.ResultChars, &r.DurSec, &r.Timestamp,
			&r.RunStatus,
		); err != nil {
			return nil, err
		}
		if runID.Valid {
			v := runID.Int64
			r.RunID = &v
		}
		r.CLISessionID = cliSessionID.String
		r.TaskPtr = taskPtr.String
		r.Arg = arg.String
		r.IsError = isErr != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// RunsWithSignature returns the set of session_runs IDs (as a membership
// map) that have at least one action_signatures row in project whose sig is
// one of sigs — the "comparable run" filter for skill-effect measurement
// (LEARN-TASKS.md LN-11: "среди шагов которых есть хотя бы одна сигнатура из
// source_json"). Rows with a NULL run_id (a bulk-imported log, LN-17) are
// excluded by the run_id IS NOT NULL filter alone: they have no session_runs
// row to key into this map by, which is exactly right — LN-11 measures
// token/turn/status effect from session_runs, and an imported row has none of
// those to contribute either way. Empty sigs returns an empty map rather than
// matching everything.
func (s *Store) RunsWithSignature(project string, sigs []string) (map[int64]bool, error) {
	out := make(map[int64]bool)
	if len(sigs) == 0 {
		return out, nil
	}
	placeholders := strings.Repeat("?,", len(sigs))
	placeholders = placeholders[:len(placeholders)-1]
	q := fmt.Sprintf(`SELECT DISTINCT run_id FROM action_signatures
    WHERE project=? AND run_id IS NOT NULL AND sig IN (%s)`, placeholders)
	args := make([]any, 0, len(sigs)+1)
	args = append(args, project)
	for _, sig := range sigs {
		args = append(args, sig)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// InsertPermissionEvent records one resolved permission request. Unlike
// InsertActions (batched per ingest pass), permission decisions arrive one at
// a time from SessionManager.handlePermission/RespondPermission, so this is a
// single-row insert.
func (s *Store) InsertPermissionEvent(ev PermissionEvent) error {
	const q = `INSERT INTO permission_events
	    (project, session, run_id, tool, pattern, decision, auto, ts)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	auto := 0
	if ev.Auto {
		auto = 1
	}
	_, err := s.db.Exec(q,
		ev.Project, ev.Session, nullInt64(ev.RunID), ev.Tool, nullStr(ev.Pattern),
		ev.Decision, auto, ev.Timestamp,
	)
	return err
}

// PermissionEventsForProject returns every raw permission_events row for a
// project, most recent first — mainly a test/debugging accessor since the
// "Permissions" tab (LEARN-TASKS.md LN-04) works off the aggregated
// TopPermissionEvents view, not individual rows.
func (s *Store) PermissionEventsForProject(project string) ([]PermissionEvent, error) {
	const q = `SELECT id, project, session, run_id, tool, pattern, decision, auto, ts
	    FROM permission_events WHERE project=? ORDER BY id DESC`
	rows, err := s.db.Query(q, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []PermissionEvent
	for rows.Next() {
		var ev PermissionEvent
		var runID sql.NullInt64
		var pattern sql.NullString
		var auto int
		if err := rows.Scan(&ev.ID, &ev.Project, &ev.Session, &runID, &ev.Tool,
			&pattern, &ev.Decision, &auto, &ev.Timestamp); err != nil {
			return nil, err
		}
		if runID.Valid {
			v := runID.Int64
			ev.RunID = &v
		}
		ev.Pattern = pattern.String
		ev.Auto = auto != 0
		out = append(out, ev)
	}
	return out, rows.Err()
}

// TopPermissionEvents aggregates permission_events for a project over the
// last sinceDays days into per (tool, pattern) stats, most frequent first.
// Only auto=0 rows are counted: an auto=1 row means a rule already resolves
// that (tool, pattern) automatically, so it is not a candidate for a new
// suggestion — counting it in would make an already-covered case look like it
// still needs one. Pass limit<=0 for no limit.
func (s *Store) TopPermissionEvents(project string, sinceDays, limit int) ([]PermissionEventStat, error) {
	q := `SELECT tool, pattern, COUNT(*),
       SUM(CASE WHEN decision IN ('allow','allow_session','allow_similar','allow_always') THEN 1 ELSE 0 END),
       SUM(CASE WHEN decision IN ('deny','deny_always') THEN 1 ELSE 0 END),
       MIN(ts), MAX(ts)
    FROM permission_events
    WHERE project=? AND auto=0 AND ts >= datetime('now', ?)
    GROUP BY tool, pattern
    ORDER BY COUNT(*) DESC`
	args := []any{project, fmt.Sprintf("-%d days", sinceDays)}
	if limit > 0 {
		q += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var stats []PermissionEventStat
	for rows.Next() {
		var st PermissionEventStat
		var pattern sql.NullString
		var firstSeen, lastSeen string
		if err := rows.Scan(&st.Tool, &pattern, &st.Count, &st.AllowCount, &st.DenyCount,
			&firstSeen, &lastSeen); err != nil {
			return nil, err
		}
		st.Pattern = pattern.String
		st.FirstSeen = parseAggTime(firstSeen)
		st.LastSeen = parseAggTime(lastSeen)
		stats = append(stats, st)
	}
	return stats, rows.Err()
}

// GetIngestOffset returns the last-indexed transcript path and byte offset
// for a CLI session, and false if it has never been indexed.
func (s *Store) GetIngestOffset(cliSessionID string) (path string, offset int64, ok bool, err error) {
	const q = `SELECT path, offset FROM ingest_state WHERE cli_session_id=?`
	err = s.db.QueryRow(q, cliSessionID).Scan(&path, &offset)
	if err == sql.ErrNoRows {
		return "", 0, false, nil
	}
	if err != nil {
		return "", 0, false, err
	}
	return path, offset, true, nil
}

// SetIngestOffset records the byte offset up to which a CLI session's
// transcript has been indexed, so the next ingest pass resumes instead of
// re-reading the whole file (upsert: same cli_session_id overwrites).
func (s *Store) SetIngestOffset(cliSessionID, path string, offset int64) error {
	const q = `INSERT INTO ingest_state (cli_session_id, path, offset, updated_at)
    VALUES (?, ?, ?, ?)
    ON CONFLICT(cli_session_id) DO UPDATE SET
        path=excluded.path, offset=excluded.offset, updated_at=excluded.updated_at`
	_, err := s.db.Exec(q, cliSessionID, path, offset, time.Now())
	return err
}

// IsLogFileImported reports whether a file with this exact (project, name,
// size, mtime) has already been bulk-imported by IngestDir (LEARN-TASKS.md
// LN-17) — the dedup check that makes re-running an import over the same
// directory a no-op.
func (s *Store) IsLogFileImported(project, name string, size int64, mtime time.Time) (bool, error) {
	const q = `SELECT 1 FROM imported_logfiles WHERE project=? AND name=? AND size=? AND mtime=?`
	var one int
	err := s.db.QueryRow(q, project, name, size, mtime).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// MarkLogFileImported records a file as imported so a later IngestDir pass
// over the same directory skips it. Idempotent: re-marking the same
// (project, name, size, mtime) triple is a no-op rather than an error.
func (s *Store) MarkLogFileImported(project, name string, size int64, mtime time.Time) error {
	const q = `INSERT INTO imported_logfiles (project, name, size, mtime, imported_at)
    VALUES (?, ?, ?, ?, ?)
    ON CONFLICT(project, name, size, mtime) DO NOTHING`
	_, err := s.db.Exec(q, project, name, size, mtime, time.Now())
	return err
}

// parseAggTime parses the string an aggregate function (MIN(ts)/MAX(ts))
// hands back for a DATETIME column. Unlike a plain column reference, the
// modernc.org/sqlite driver cannot infer the declared type through an
// aggregate, so it comes back as a string in time.Time's own default String()
// layout rather than as a time.Time value the driver auto-converts — RFC3339
// (the layout a direct column scan produces) is tried too, in case that ever
// changes upstream. An unparseable value yields the zero time rather than an
// error: this only ever feeds a display timestamp, never a comparison.
func parseAggTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	if t, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", s); err == nil {
		return t
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t
	}
	return time.Time{}
}

// --- helpers ---

func nullStr(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

func nullTime(t *time.Time) sql.NullTime {
	if t == nil {
		return sql.NullTime{}
	}
	return sql.NullTime{Time: *t, Valid: true}
}

func nullInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}

func nullIntPtr(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}

func nullFloat64(v *float64) sql.NullFloat64 {
	if v == nil {
		return sql.NullFloat64{}
	}
	return sql.NullFloat64{Float64: *v, Valid: true}
}
