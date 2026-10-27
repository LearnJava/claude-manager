package store

import (
	"database/sql"
	"encoding/json"
	"time"
)

// Benchmark run statuses.
const (
	BenchmarkRunning     = "running"
	BenchmarkDone        = "done"
	BenchmarkInterrupted = "interrupted"
)

// BenchTask is one task of a benchmark suite as it was when the run started
// (WASTE-TASKS.md WS-12): text, the commit to start from and the gates. A run
// keeps its own copy, so editing or deleting the source reference case later
// changes nothing already measured. CaseID is the reference case it came from,
// 0 for a task of an external suite.
type BenchTask struct {
	ID         string   `json:"id"`
	CaseID     int64    `json:"case_id,omitempty"`
	Text       string   `json:"text"`
	BaseCommit string   `json:"base_commit"`
	Gates      []string `json:"gates"`
}

// BenchConfig is one point on the configuration axes. The zero value is the
// project as it is: bans installed, skills left in place, gate-driven rework on.
type BenchConfig struct {
	Name     string `json:"name"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	NoSkills bool   `json:"no_skills,omitempty"` // remove .claude/skills from the replay folder
	NoBans   bool   `json:"no_bans,omitempty"`   // no PreToolUse hook in the replay folder
	NoGates  bool   `json:"no_gates,omitempty"`  // one attempt, no rework rounds on red gates
}

// BenchmarkRun is a suite × configurations × repeats matrix and its frozen
// inputs. Rounds is the rework budget of a config with gates on.
type BenchmarkRun struct {
	ID         int64
	Project    string
	Name       string
	Suite      []BenchTask
	Configs    []BenchConfig
	Repeats    int
	Rounds     int
	Status     string
	Note       string
	CreatedAt  time.Time
	FinishedAt time.Time // zero while running
}

// BenchmarkTrial is one cell of the matrix: a task under a configuration, one
// repeat. Errored means the model could not be run at all — such a trial says
// nothing about the configuration and is counted apart from a red result.
type BenchmarkTrial struct {
	ID           int64
	RunID        int64
	Task         int // index into BenchmarkRun.Suite
	Config       int // index into BenchmarkRun.Configs
	Repeat       int // 1-based
	Passed       bool
	Errored      bool
	Rounds       int
	Tokens       int64
	DurationSec  int
	ResultCommit string
	Note         string
	CreatedAt    time.Time
}

// InsertBenchmarkRun inserts a run and sets r.ID. A zero CreatedAt is now and
// an empty Status is running.
func (s *Store) InsertBenchmarkRun(r *BenchmarkRun) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	if r.Status == "" {
		r.Status = BenchmarkRunning
	}
	suite, err := json.Marshal(r.Suite)
	if err != nil {
		return err
	}
	configs, err := json.Marshal(r.Configs)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`INSERT INTO benchmark_runs
	    (project, name, suite, configs, repeats, rounds, status, note, created_at)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Project, r.Name, string(suite), string(configs), r.Repeats, r.Rounds, r.Status, r.Note, r.CreatedAt)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

const selectBenchRunCols = `id, project, name, suite, configs, repeats, rounds, status, note, created_at, finished_at`

func scanBenchmarkRun(sc interface{ Scan(...any) error }) (*BenchmarkRun, error) {
	var r BenchmarkRun
	var suite, configs string
	var fin sql.NullTime
	if err := sc.Scan(&r.ID, &r.Project, &r.Name, &suite, &configs, &r.Repeats, &r.Rounds,
		&r.Status, &r.Note, &r.CreatedAt, &fin); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(suite), &r.Suite)
	_ = json.Unmarshal([]byte(configs), &r.Configs)
	if fin.Valid {
		r.FinishedAt = fin.Time
	}
	return &r, nil
}

// GetBenchmarkRun returns one run, or (nil, nil) when there is none.
func (s *Store) GetBenchmarkRun(id int64) (*BenchmarkRun, error) {
	r, err := scanBenchmarkRun(s.db.QueryRow(`SELECT `+selectBenchRunCols+` FROM benchmark_runs WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// ListBenchmarkRuns returns a project's runs, newest first.
func (s *Store) ListBenchmarkRuns(project string) ([]*BenchmarkRun, error) {
	rows, err := s.db.Query(`SELECT `+selectBenchRunCols+
		` FROM benchmark_runs WHERE project=? ORDER BY created_at DESC, id DESC`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BenchmarkRun
	for rows.Next() {
		r, err := scanBenchmarkRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// FinishBenchmarkRun records how a run ended. Only a running run changes, so a
// late call cannot reopen or rewrite a finished one.
func (s *Store) FinishBenchmarkRun(id int64, status, note string) error {
	_, err := s.db.Exec(`UPDATE benchmark_runs SET status=?, note=?, finished_at=?
	    WHERE id=? AND status=?`, status, note, time.Now().UTC(), id, BenchmarkRunning)
	return err
}

// InterruptRunningBenchmarks marks every run still "running" as interrupted —
// called at start-up, when no run can be alive. Returns how many it closed.
func (s *Store) InterruptRunningBenchmarks() (int64, error) {
	res, err := s.db.Exec(`UPDATE benchmark_runs SET status=?, note=?, finished_at=?
	    WHERE status=?`, BenchmarkInterrupted, "the manager stopped before the run finished",
		time.Now().UTC(), BenchmarkRunning)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// InsertBenchmarkTrial inserts a trial and sets t.ID. A zero CreatedAt is now.
func (s *Store) InsertBenchmarkTrial(t *BenchmarkTrial) error {
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now().UTC()
	}
	res, err := s.db.Exec(`INSERT INTO benchmark_trials
	    (run_id, task_idx, config_idx, repeat, passed, errored, rounds, tokens, duration_sec,
	     result_commit, note, created_at)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.RunID, t.Task, t.Config, t.Repeat, b2i(t.Passed), b2i(t.Errored), t.Rounds, t.Tokens,
		t.DurationSec, t.ResultCommit, t.Note, t.CreatedAt)
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// ListBenchmarkTrials returns a run's trials in the order they finished.
func (s *Store) ListBenchmarkTrials(runID int64) ([]*BenchmarkTrial, error) {
	rows, err := s.db.Query(`SELECT id, run_id, task_idx, config_idx, repeat, passed, errored, rounds,
	    tokens, duration_sec, result_commit, note, created_at
	    FROM benchmark_trials WHERE run_id=? ORDER BY id ASC`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*BenchmarkTrial
	for rows.Next() {
		var t BenchmarkTrial
		var passed, errored int
		if err := rows.Scan(&t.ID, &t.RunID, &t.Task, &t.Config, &t.Repeat, &passed, &errored, &t.Rounds,
			&t.Tokens, &t.DurationSec, &t.ResultCommit, &t.Note, &t.CreatedAt); err != nil {
			return nil, err
		}
		t.Passed, t.Errored = passed != 0, errored != 0
		out = append(out, &t)
	}
	return out, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
