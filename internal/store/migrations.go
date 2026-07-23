package store

import (
	"database/sql"
	"fmt"
	"strings"
)

const (
	sqlCreateSessionRuns = `
CREATE TABLE IF NOT EXISTS session_runs (
    id                    INTEGER PRIMARY KEY,
    project               TEXT NOT NULL,
    session               TEXT NOT NULL,
    cli_session_id        TEXT,
    model                 TEXT,
    started_at            DATETIME NOT NULL,
    finished_at           DATETIME,
    status                TEXT NOT NULL,
    tasks_done            INTEGER DEFAULT 0,
    exit_code             INTEGER,
    error_msg             TEXT,
    total_cost_usd        REAL DEFAULT 0,
    input_tokens          INTEGER DEFAULT 0,
    output_tokens         INTEGER DEFAULT 0,
    cache_read_tokens     INTEGER DEFAULT 0,
    cache_creation_tokens INTEGER DEFAULT 0,
    num_turns             INTEGER DEFAULT 0,
    duration_ms           INTEGER DEFAULT 0
)`

	sqlCreateSessionLogs = `
CREATE TABLE IF NOT EXISTS session_logs (
    id         INTEGER PRIMARY KEY,
    run_id     INTEGER REFERENCES session_runs(id),
    timestamp  DATETIME NOT NULL,
    level      TEXT NOT NULL,
    message    TEXT NOT NULL,
    tool_name  TEXT,
    tool_input TEXT
)`

	sqlCreateTaskPlans = `
CREATE TABLE IF NOT EXISTS task_plans (
    id             INTEGER PRIMARY KEY,
    project        TEXT NOT NULL,
    original_task  TEXT NOT NULL,
    analysis_json  TEXT NOT NULL,
    status         TEXT NOT NULL,
    kind           TEXT NOT NULL DEFAULT 'adhoc',
    created_at     DATETIME NOT NULL,
    completed_at   DATETIME,
    total_cost_usd REAL,
    total_tokens   INTEGER
)`

	sqlCreatePlanSubtasks = `
CREATE TABLE IF NOT EXISTS plan_subtasks (
    id             INTEGER PRIMARY KEY,
    plan_id        INTEGER REFERENCES task_plans(id),
    subtask_id     TEXT NOT NULL,
    name           TEXT NOT NULL,
    prompt         TEXT NOT NULL,
    depends_on     TEXT,
    model          TEXT,
    session_run_id INTEGER REFERENCES session_runs(id),
    status         TEXT NOT NULL,
    result_summary TEXT,
    files_changed  TEXT
)`

	sqlCreateDailyMetrics = `
CREATE TABLE IF NOT EXISTS daily_metrics (
    date                TEXT NOT NULL,
    project             TEXT NOT NULL,
    total_cost          REAL DEFAULT 0,
    total_input_tokens  INTEGER DEFAULT 0,
    total_output_tokens INTEGER DEFAULT 0,
    total_runs          INTEGER DEFAULT 0,
    total_tasks         INTEGER DEFAULT 0,
    PRIMARY KEY (date, project)
)`

	sqlCreateMixedBriefs = `
CREATE TABLE IF NOT EXISTS mixed_briefs (
    id         INTEGER PRIMARY KEY,
    brief_id   TEXT NOT NULL UNIQUE,
    project    TEXT NOT NULL,
    task       TEXT NOT NULL,
    files      TEXT,
    created_at DATETIME NOT NULL
)`

	sqlIdxLogsRun       = `CREATE INDEX IF NOT EXISTS idx_logs_run ON session_logs(run_id)`
	sqlIdxRunsProject   = `CREATE INDEX IF NOT EXISTS idx_runs_project ON session_runs(project, session)`
	sqlIdxBriefsProject = `CREATE INDEX IF NOT EXISTS idx_briefs_project ON mixed_briefs(project)`
)

func migrate(db *sql.DB) error {
	stmts := []string{
		sqlCreateSessionRuns,
		sqlCreateSessionLogs,
		sqlCreateTaskPlans,
		sqlCreatePlanSubtasks,
		sqlCreateDailyMetrics,
		sqlCreateMixedBriefs,
		sqlIdxLogsRun,
		sqlIdxRunsProject,
		sqlIdxBriefsProject,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}

	// task_plans predates the roadmap-vs-adhoc discriminator (kind column).
	// sqlCreateTaskPlans above already includes it for fresh databases; this
	// ALTER TABLE backfills it on existing ones. There is no migration
	// version table in this codebase, so this statement re-runs on every
	// startup — tolerate the "duplicate column" error instead of guarding
	// with a version check. This is the template for any future additive
	// column: add it to the CREATE TABLE for new DBs, then ALTER + tolerate
	// here for existing ones.
	if _, err := db.Exec(`ALTER TABLE task_plans ADD COLUMN kind TEXT NOT NULL DEFAULT 'adhoc'`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("migrate: add task_plans.kind: %w", err)
		}
	}
	return nil
}
