package store

import (
	"encoding/json"
	"time"
)

// ReferenceCase is a finished task kept as a case with a known right answer
// (WASTE-TASKS.md WS-06): the task text, the commit it started from, the
// gates it passed and what it cost. Tokens are input+output (cache excluded) —
// what a one-shot replay reports too, so an original and its replays compare.
// InvalidReason is "" while the case can be replayed.
type ReferenceCase struct {
	ID            int64
	Project       string
	TaskPtr       string
	TaskText      string
	BaseCommit    string
	GateCmds      []string
	ResultCommit  string
	Rounds        int
	Tokens        int64
	DurationSec   int
	Model         string
	Effort        string
	CreatedAt     time.Time
	InvalidReason string
}

// ReferenceReplay is one more run of a case with another model or setting.
// Passed says whether the case's gates went green; Rounds counts the rework
// rounds that took. ResultCommit is the replay's outcome pinned under
// refs/cm-replay/ ("" when the agent changed nothing).
type ReferenceReplay struct {
	ID           int64
	CaseID       int64
	Model        string
	Effort       string
	Passed       bool
	Rounds       int
	Tokens       int64
	DurationSec  int
	ResultCommit string
	Note         string
	CreatedAt    time.Time
}

const selectRefCaseCols = `id, project, task_ptr, task_text, base_commit, gate_cmds, result_commit,
	rounds, tokens, duration_sec, model, effort, created_at, invalid_reason`

func scanReferenceCase(sc interface{ Scan(...any) error }) (*ReferenceCase, error) {
	var c ReferenceCase
	var gates string
	if err := sc.Scan(&c.ID, &c.Project, &c.TaskPtr, &c.TaskText, &c.BaseCommit, &gates, &c.ResultCommit,
		&c.Rounds, &c.Tokens, &c.DurationSec, &c.Model, &c.Effort, &c.CreatedAt, &c.InvalidReason); err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(gates), &c.GateCmds)
	return &c, nil
}

// InsertReferenceCase inserts a case and sets c.ID. A zero CreatedAt is now.
func (s *Store) InsertReferenceCase(c *ReferenceCase) error {
	if c.CreatedAt.IsZero() {
		c.CreatedAt = time.Now().UTC()
	}
	gates, err := json.Marshal(c.GateCmds)
	if err != nil {
		return err
	}
	if c.GateCmds == nil {
		gates = []byte("[]")
	}
	res, err := s.db.Exec(`INSERT INTO reference_cases
	    (project, task_ptr, task_text, base_commit, gate_cmds, result_commit,
	     rounds, tokens, duration_sec, model, effort, created_at, invalid_reason)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Project, c.TaskPtr, c.TaskText, c.BaseCommit, string(gates), c.ResultCommit,
		c.Rounds, c.Tokens, c.DurationSec, c.Model, c.Effort, c.CreatedAt, c.InvalidReason)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

// GetReferenceCase returns one case, or (nil, nil) when there is none.
func (s *Store) GetReferenceCase(id int64) (*ReferenceCase, error) {
	list, err := s.queryReferenceCases(`SELECT `+selectRefCaseCols+` FROM reference_cases WHERE id=?`, id)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return list[0], nil
}

// HasReferenceCase reports whether the same task from the same base commit is
// already a case of the project — a re-recorded task must not duplicate it.
func (s *Store) HasReferenceCase(project, taskPtr, baseCommit string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM reference_cases
	    WHERE project=? AND task_ptr=? AND base_commit=?`, project, taskPtr, baseCommit).Scan(&n)
	return n > 0, err
}

// ListReferenceCases returns a project's cases, oldest first.
func (s *Store) ListReferenceCases(project string) ([]*ReferenceCase, error) {
	return s.queryReferenceCases(`SELECT `+selectRefCaseCols+
		` FROM reference_cases WHERE project=? ORDER BY created_at ASC, id ASC`, project)
}

// SetReferenceCaseInvalid marks a case unusable with the reason ("" restores it).
func (s *Store) SetReferenceCaseInvalid(id int64, reason string) error {
	_, err := s.db.Exec(`UPDATE reference_cases SET invalid_reason=? WHERE id=?`, reason, id)
	return err
}

func (s *Store) queryReferenceCases(q string, args ...any) ([]*ReferenceCase, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReferenceCase
	for rows.Next() {
		c, err := scanReferenceCase(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// InsertReferenceReplay inserts a replay and sets r.ID. A zero CreatedAt is now.
func (s *Store) InsertReferenceReplay(r *ReferenceReplay) error {
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	passed := 0
	if r.Passed {
		passed = 1
	}
	res, err := s.db.Exec(`INSERT INTO reference_replays
	    (case_id, model, effort, passed, rounds, tokens, duration_sec, result_commit, note, created_at)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.CaseID, r.Model, r.Effort, passed, r.Rounds, r.Tokens, r.DurationSec, r.ResultCommit, r.Note, r.CreatedAt)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

// ListReferenceReplays returns the replays of one case, oldest first.
func (s *Store) ListReferenceReplays(caseID int64) ([]*ReferenceReplay, error) {
	rows, err := s.db.Query(`SELECT id, case_id, model, effort, passed, rounds, tokens, duration_sec,
	    result_commit, note, created_at FROM reference_replays WHERE case_id=? ORDER BY created_at ASC, id ASC`, caseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ReferenceReplay
	for rows.Next() {
		var r ReferenceReplay
		var passed int
		if err := rows.Scan(&r.ID, &r.CaseID, &r.Model, &r.Effort, &passed, &r.Rounds, &r.Tokens,
			&r.DurationSec, &r.ResultCommit, &r.Note, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Passed = passed != 0
		out = append(out, &r)
	}
	return out, rows.Err()
}
