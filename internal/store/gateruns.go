package store

import (
	"database/sql"
	"time"
)

// GateRun is the outcome of one run of a project's gates and the commit it ran
// on (WASTE-TASKS.md WS-07). Summary is the failed command and the tail of its
// output, empty for a green run.
type GateRun struct {
	ID      int64
	Project string
	TS      time.Time
	Passed  bool
	Head    string
	Summary string
}

// gateRunKeep bounds the table: older rows of a project are of no use to the
// scanner, which reads only the latest one.
const gateRunKeep = 50

// InsertGateRun records a gate run. A zero TS is now.
func (s *Store) InsertGateRun(r *GateRun) error {
	if r.TS.IsZero() {
		r.TS = time.Now().UTC()
	}
	res, err := s.db.Exec(`INSERT INTO gate_runs (project, ts, passed, head, summary) VALUES (?, ?, ?, ?, ?)`,
		r.Project, r.TS, r.Passed, r.Head, r.Summary)
	if err != nil {
		return err
	}
	if r.ID, err = res.LastInsertId(); err != nil {
		return err
	}
	_, err = s.db.Exec(`DELETE FROM gate_runs WHERE project=? AND id <= ?`, r.Project, r.ID-gateRunKeep)
	return err
}

// LastGateRun returns the project's latest gate run, or (nil, nil) when none
// was recorded.
func (s *Store) LastGateRun(project string) (*GateRun, error) {
	var r GateRun
	err := s.db.QueryRow(`SELECT id, project, ts, passed, head, summary FROM gate_runs
	    WHERE project=? ORDER BY id DESC LIMIT 1`, project).
		Scan(&r.ID, &r.Project, &r.TS, &r.Passed, &r.Head, &r.Summary)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
