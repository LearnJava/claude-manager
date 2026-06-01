package store

import "database/sql"

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

	sqlIdxLogsRun     = `CREATE INDEX IF NOT EXISTS idx_logs_run ON session_logs(run_id)`
	sqlIdxRunsProject = `CREATE INDEX IF NOT EXISTS idx_runs_project ON session_runs(project, session)`
)

func migrate(db *sql.DB) error {
	stmts := []string{
		sqlCreateSessionRuns,
		sqlCreateSessionLogs,
		sqlCreateTaskPlans,
		sqlCreatePlanSubtasks,
		sqlCreateDailyMetrics,
		sqlIdxLogsRun,
		sqlIdxRunsProject,
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			return err
		}
	}
	return nil
}
