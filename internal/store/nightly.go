package store

import (
	"database/sql"
	"time"
)

// NightlyRun is the latest scheduled gate run of a project (WASTE-TASKS.md
// WS-10). Note is "" for a green run, the filed task pointer or the reason none
// was filed for a red one.
type NightlyRun struct {
	Project string
	TS      time.Time
	Passed  bool
	Note    string
}

// SetNightlyRun records the project's latest scheduled run, replacing the
// previous one. A zero TS is now.
func (s *Store) SetNightlyRun(r NightlyRun) error {
	if r.TS.IsZero() {
		r.TS = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO nightly_runs (project, ts, passed, note) VALUES (?, ?, ?, ?)
	    ON CONFLICT(project) DO UPDATE SET ts=excluded.ts, passed=excluded.passed, note=excluded.note`,
		r.Project, r.TS, r.Passed, r.Note)
	return err
}

// LastNightlyRun returns the project's latest scheduled run, or (nil, nil)
// when it never ran.
func (s *Store) LastNightlyRun(project string) (*NightlyRun, error) {
	var r NightlyRun
	err := s.db.QueryRow(`SELECT project, ts, passed, note FROM nightly_runs WHERE project=?`, project).
		Scan(&r.Project, &r.TS, &r.Passed, &r.Note)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}
