package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// SessionRun represents a row in session_runs.
type SessionRun struct {
	ID                   int64
	Project              string
	Session              string
	CLISessionID         string
	Model                string
	StartedAt            time.Time
	FinishedAt           *time.Time
	Status               string // "completed" | "error" | "stopped" | "rate_limited"
	TasksDone            int
	ExitCode             *int
	ErrorMsg             string
	TotalCostUSD         float64
	InputTokens          int64
	OutputTokens         int64
	CacheReadTokens      int64
	CacheCreationTokens  int64
	NumTurns             int
	DurationMs           int64
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
	Date               string // "2026-05-22"
	Project            string
	TotalCost          float64
	TotalInputTokens   int64
	TotalOutputTokens  int64
	TotalRuns          int
	TotalTasks         int
}

// TaskPlan represents a row in task_plans.
type TaskPlan struct {
	ID           int64
	Project      string
	OriginalTask string
	AnalysisJSON string
	Status       string // "draft" | "approved" | "executing" | "completed"
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

// Store wraps a SQLite database and provides CRUD for all tables.
type Store struct {
	db *sql.DB
}

// New opens the SQLite database at dbPath, runs migrations, and returns a Store.
func New(dbPath string) (*Store, error) {
	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_foreign_keys=on")
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
    cache_read_tokens, cache_creation_tokens, num_turns, duration_ms`

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanRun(row rowScanner) (*SessionRun, error) {
	var r SessionRun
	var finishedAt sql.NullTime
	var exitCode sql.NullInt64
	err := row.Scan(
		&r.ID, &r.Project, &r.Session, &r.CLISessionID, &r.Model,
		&r.StartedAt, &finishedAt, &r.Status,
		&r.TasksDone, &exitCode, &r.ErrorMsg,
		&r.TotalCostUSD, &r.InputTokens, &r.OutputTokens,
		&r.CacheReadTokens, &r.CacheCreationTokens, &r.NumTurns, &r.DurationMs,
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
	return &r, nil
}

// InsertRun inserts a new SessionRun and sets run.ID to the generated row ID.
func (s *Store) InsertRun(run *SessionRun) error {
	const q = `INSERT INTO session_runs
    (project, session, cli_session_id, model, started_at, finished_at, status,
     tasks_done, exit_code, error_msg, total_cost_usd, input_tokens, output_tokens,
     cache_read_tokens, cache_creation_tokens, num_turns, duration_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := s.db.Exec(q,
		run.Project, run.Session, run.CLISessionID, run.Model,
		run.StartedAt, nullTime(run.FinishedAt), run.Status,
		run.TasksDone, nullIntPtr(run.ExitCode), run.ErrorMsg,
		run.TotalCostUSD, run.InputTokens, run.OutputTokens,
		run.CacheReadTokens, run.CacheCreationTokens, run.NumTurns, run.DurationMs,
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
    cache_creation_tokens=?, num_turns=?, duration_ms=?, model=?
WHERE id=?`
	_, err := s.db.Exec(q,
		nullTime(run.FinishedAt), run.Status, run.TasksDone, nullIntPtr(run.ExitCode), run.ErrorMsg,
		run.TotalCostUSD, run.InputTokens, run.OutputTokens, run.CacheReadTokens,
		run.CacheCreationTokens, run.NumTurns, run.DurationMs, run.Model,
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

// --- daily_metrics ---

// AddDailyMetrics upserts daily aggregate metrics, adding the delta to any existing row.
func (s *Store) AddDailyMetrics(m *DailyMetrics) error {
	const q = `INSERT INTO daily_metrics
    (date, project, total_cost, total_input_tokens, total_output_tokens, total_runs, total_tasks)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(date, project) DO UPDATE SET
    total_cost          = total_cost          + excluded.total_cost,
    total_input_tokens  = total_input_tokens  + excluded.total_input_tokens,
    total_output_tokens = total_output_tokens + excluded.total_output_tokens,
    total_runs          = total_runs          + excluded.total_runs,
    total_tasks         = total_tasks         + excluded.total_tasks`
	_, err := s.db.Exec(q,
		m.Date, m.Project, m.TotalCost,
		m.TotalInputTokens, m.TotalOutputTokens,
		m.TotalRuns, m.TotalTasks,
	)
	return err
}

// GetDailyMetrics returns metrics for a specific date and project, or nil if not found.
func (s *Store) GetDailyMetrics(date, project string) (*DailyMetrics, error) {
	const q = `SELECT date, project, total_cost, total_input_tokens, total_output_tokens, total_runs, total_tasks
    FROM daily_metrics WHERE date=? AND project=?`
	var m DailyMetrics
	err := s.db.QueryRow(q, date, project).Scan(
		&m.Date, &m.Project, &m.TotalCost,
		&m.TotalInputTokens, &m.TotalOutputTokens,
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
	const q = `SELECT date, project, total_cost, total_input_tokens, total_output_tokens, total_runs, total_tasks
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
		&p.ID, &p.Project, &p.OriginalTask, &p.AnalysisJSON, &p.Status,
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
	const q = `INSERT INTO task_plans
    (project, original_task, analysis_json, status, created_at, completed_at, total_cost_usd, total_tokens)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	res, err := s.db.Exec(q,
		plan.Project, plan.OriginalTask, plan.AnalysisJSON, plan.Status,
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
	const q = `SELECT id, project, original_task, analysis_json, status, created_at, completed_at, total_cost_usd, total_tokens
    FROM task_plans WHERE id=?`
	p, err := scanPlan(s.db.QueryRow(q, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

// ListPlans returns up to limit plans for a project, newest first.
func (s *Store) ListPlans(project string, limit int) ([]*TaskPlan, error) {
	q := `SELECT id, project, original_task, analysis_json, status, created_at, completed_at, total_cost_usd, total_tokens
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
