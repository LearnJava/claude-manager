package store

import (
	"fmt"
	"time"
)

// Quality labels (WASTE-TASKS.md WS-11). A label says what went wrong in a task
// that ended badly; the list is fixed so a model's free text can never become
// a label.
const (
	LabelTestsFitted    = "tests_fitted_to_code" // the tests were bent to pass instead of fixing the code
	LabelProtocolBroken = "protocol_broken"      // the project's working protocol was not followed
	LabelGarbageLeft    = "garbage_left"         // stray files, debug output, half-finished edits left behind
	LabelWrongTask      = "wrong_task_done"      // the work does not match what the task asked for
)

// TaskLabelNames lists the labels a task may carry, in a stable order.
var TaskLabelNames = []string{LabelTestsFitted, LabelProtocolBroken, LabelGarbageLeft, LabelWrongTask}

// ValidTaskLabel reports whether label is one of TaskLabelNames.
func ValidTaskLabel(label string) bool {
	for _, l := range TaskLabelNames {
		if l == label {
			return true
		}
	}
	return false
}

// TaskLabel is one label put on a task that ended badly. Tokens is what the
// judging call cost (input+output); the same tokens are added to RunID's run,
// so the judgement is part of the spend of the task it judged.
type TaskLabel struct {
	ID        int64
	Project   string
	TaskPtr   string
	RunID     int64
	Label     string
	Reason    string
	Model     string
	Tokens    int64
	CreatedAt time.Time
}

// InsertTaskLabel stores a label and sets l.ID. A label outside TaskLabelNames
// is an error, not a quiet write. A zero CreatedAt is now.
func (s *Store) InsertTaskLabel(l *TaskLabel) error {
	if !ValidTaskLabel(l.Label) {
		return fmt.Errorf("store: %q is not a task label (allowed: %v)", l.Label, TaskLabelNames)
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = time.Now().UTC()
	}
	res, err := s.db.Exec(`INSERT INTO task_labels
	    (project, task_ptr, run_id, label, reason, model, tokens, created_at)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		l.Project, l.TaskPtr, l.RunID, l.Label, l.Reason, l.Model, l.Tokens, l.CreatedAt)
	if err != nil {
		return err
	}
	l.ID, err = res.LastInsertId()
	return err
}

// ListTaskLabels returns a project's labels, newest first. limit <= 0 = all.
func (s *Store) ListTaskLabels(project string, limit int) ([]*TaskLabel, error) {
	q := `SELECT id, project, task_ptr, run_id, label, reason, model, tokens, created_at
	      FROM task_labels WHERE project=? ORDER BY id DESC`
	args := []any{project}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*TaskLabel{}
	for rows.Next() {
		var l TaskLabel
		if err := rows.Scan(&l.ID, &l.Project, &l.TaskPtr, &l.RunID, &l.Label, &l.Reason,
			&l.Model, &l.Tokens, &l.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &l)
	}
	return out, rows.Err()
}

// AddRunUsage adds extra model usage to an already finished run: the cost of
// work done on its behalf after it closed (a quality judgement). A run that no
// longer exists changes nothing.
func (s *Store) AddRunUsage(runID, inputTokens, outputTokens int64, costUSD float64) error {
	_, err := s.db.Exec(`UPDATE session_runs SET
	    input_tokens = COALESCE(input_tokens, 0) + ?,
	    output_tokens = COALESCE(output_tokens, 0) + ?,
	    total_cost_usd = COALESCE(total_cost_usd, 0) + ?
	    WHERE id=?`, inputTokens, outputTokens, costUSD, runID)
	return err
}

// HasTaskLabelForRun reports whether a run already carries a label, so one
// bad ending is never judged (and paid for) twice.
func (s *Store) HasTaskLabelForRun(runID int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM task_labels WHERE run_id=? AND run_id<>0`, runID).Scan(&n)
	return n > 0, err
}
