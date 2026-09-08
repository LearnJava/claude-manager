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
    date                        TEXT NOT NULL,
    project                     TEXT NOT NULL,
    total_cost                  REAL DEFAULT 0,
    total_input_tokens          INTEGER DEFAULT 0,
    total_output_tokens         INTEGER DEFAULT 0,
    total_cache_read_tokens     INTEGER DEFAULT 0,
    total_cache_creation_tokens INTEGER DEFAULT 0,
    total_runs                  INTEGER DEFAULT 0,
    total_tasks                 INTEGER DEFAULT 0,
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

	// action_signatures/ingest_state back the experience layer (LEARN-TASKS.md
	// LN-02): normalized tool-call signatures mined from CLI transcripts
	// (LN-01), aggregated per project by TopSignatures for the "Actions" tab
	// (LN-03) and downstream promotion into permission/skill candidates
	// (LN-04/07/08). ingest_state is the per-transcript offset checkpoint so
	// re-indexing a CLI session's JSONL never re-inserts rows already seen.
	sqlCreateActionSignatures = `
CREATE TABLE IF NOT EXISTS action_signatures (
    id             INTEGER PRIMARY KEY,
    project        TEXT NOT NULL,
    session        TEXT NOT NULL,
    run_id         INTEGER REFERENCES session_runs(id),
    cli_session_id TEXT,
    task_ptr       TEXT,
    step_index     INTEGER NOT NULL,
    tool           TEXT NOT NULL,
    sig            TEXT NOT NULL,
    arg            TEXT,
    is_error       INTEGER DEFAULT 0,
    out_tokens     INTEGER DEFAULT 0,
    result_chars   INTEGER DEFAULT 0,
    ts             DATETIME NOT NULL
)`

	sqlCreateIngestState = `
CREATE TABLE IF NOT EXISTS ingest_state (
    cli_session_id TEXT PRIMARY KEY,
    path           TEXT NOT NULL,
    offset         INTEGER NOT NULL,
    updated_at     DATETIME NOT NULL
)`

	// imported_logfiles backs IngestDir's bulk import of auto-saved markdown
	// logs (LEARN-TASKS.md LN-17). Unlike ingest_state (a byte offset into one
	// growing JSONL transcript), a markdown log is one closed, complete run —
	// there is nothing to resume mid-file, only "already imported or not" —
	// so the dedup key is the file's identity (name, size, mtime) rather than
	// an offset. Re-running an import over the same directory is then a
	// no-op: a file whose (name, size, mtime) triple is already present is
	// skipped without touching action_signatures again.
	sqlCreateImportedLogfiles = `
CREATE TABLE IF NOT EXISTS imported_logfiles (
    id          INTEGER PRIMARY KEY,
    project     TEXT NOT NULL,
    name        TEXT NOT NULL,
    size        INTEGER NOT NULL,
    mtime       DATETIME NOT NULL,
    imported_at DATETIME NOT NULL,
    UNIQUE(project, name, size, mtime)
)`

	sqlIdxLogsRun       = `CREATE INDEX IF NOT EXISTS idx_logs_run ON session_logs(run_id)`
	sqlIdxRunsProject   = `CREATE INDEX IF NOT EXISTS idx_runs_project ON session_runs(project, session)`
	sqlIdxBriefsProject = `CREATE INDEX IF NOT EXISTS idx_briefs_project ON mixed_briefs(project)`
	sqlIdxSigProject    = `CREATE INDEX IF NOT EXISTS idx_sig_project ON action_signatures(project, sig)`
	sqlIdxSigRun        = `CREATE INDEX IF NOT EXISTS idx_sig_run ON action_signatures(run_id)`
)

func migrate(db *sql.DB) error {
	stmts := []string{
		sqlCreateSessionRuns,
		sqlCreateSessionLogs,
		sqlCreateTaskPlans,
		sqlCreatePlanSubtasks,
		sqlCreateDailyMetrics,
		sqlCreateMixedBriefs,
		sqlCreateActionSignatures,
		sqlCreateIngestState,
		sqlCreateImportedLogfiles,
		sqlIdxLogsRun,
		sqlIdxRunsProject,
		sqlIdxBriefsProject,
		sqlIdxSigProject,
		sqlIdxSigRun,
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

	// daily_metrics predates showing token volume rather than dollars in the
	// UI: it tracked only input/output, so a day's cache traffic — usually the
	// bulk of the tokens actually pushed through the model — was invisible.
	// Same additive pattern as task_plans.kind above.
	for _, col := range []string{
		`ALTER TABLE daily_metrics ADD COLUMN total_cache_read_tokens INTEGER DEFAULT 0`,
		`ALTER TABLE daily_metrics ADD COLUMN total_cache_creation_tokens INTEGER DEFAULT 0`,
	} {
		if _, err := db.Exec(col); err != nil {
			if !strings.Contains(err.Error(), "duplicate column") {
				return fmt.Errorf("migrate: %s: %w", col, err)
			}
		}
	}
	return nil
}
