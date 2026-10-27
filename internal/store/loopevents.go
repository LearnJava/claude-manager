package store

import "time"

// LoopEvent is one detected tool-call loop (WASTE-TASKS.md WS-04). Sig is the
// experience signature of the repeated call, so it joins to action_signatures;
// Arg is the call as the detector saw it. Far marks a repeat found outside
// the detector's ring buffer. ResolvedBy is "<tool> <arg>" of the first
// different successful action after the loop — what broke it last time —
// and "" until the run has been indexed.
type LoopEvent struct {
	ID         int64
	Project    string
	Session    string
	RunID      int64
	Sig        string
	Tool       string
	Arg        string
	Count      int
	Far        bool
	Timestamp  time.Time
	ResolvedBy string
}

const selectLoopEventCols = `id, project, session, run_id, sig, tool, arg, count, far, ts, resolved_by`

func scanLoopEvent(sc interface{ Scan(...any) error }) (*LoopEvent, error) {
	var e LoopEvent
	var far int
	if err := sc.Scan(&e.ID, &e.Project, &e.Session, &e.RunID, &e.Sig, &e.Tool, &e.Arg,
		&e.Count, &far, &e.Timestamp, &e.ResolvedBy); err != nil {
		return nil, err
	}
	e.Far = far != 0
	return &e, nil
}

// InsertLoopEvent inserts an event and sets e.ID. A zero Timestamp is now.
func (s *Store) InsertLoopEvent(e *LoopEvent) error {
	if e.Timestamp.IsZero() {
		e.Timestamp = time.Now().UTC()
	}
	far := 0
	if e.Far {
		far = 1
	}
	res, err := s.db.Exec(`INSERT INTO loop_events
	    (project, session, run_id, sig, tool, arg, count, far, ts, resolved_by)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Project, e.Session, e.RunID, e.Sig, e.Tool, e.Arg, e.Count, far, e.Timestamp, e.ResolvedBy)
	if err != nil {
		return err
	}
	e.ID, err = res.LastInsertId()
	return err
}

// UnresolvedLoopEvents returns the events of one run that have no
// resolved_by yet, oldest first.
func (s *Store) UnresolvedLoopEvents(runID int64) ([]*LoopEvent, error) {
	return s.queryLoopEvents(`SELECT `+selectLoopEventCols+
		` FROM loop_events WHERE run_id=? AND resolved_by='' ORDER BY ts ASC, id ASC`, runID)
}

// SetLoopResolved records what broke the loop.
func (s *Store) SetLoopResolved(id int64, resolvedBy string) error {
	_, err := s.db.Exec(`UPDATE loop_events SET resolved_by=? WHERE id=?`, resolvedBy, id)
	return err
}

// LastResolvedLoop returns the most recent earlier loop of a project on the
// same signature whose way out is known, or nil when there is none.
func (s *Store) LastResolvedLoop(project, sig string) (*LoopEvent, error) {
	list, err := s.queryLoopEvents(`SELECT `+selectLoopEventCols+
		` FROM loop_events WHERE project=? AND sig=? AND resolved_by<>''
		  ORDER BY ts DESC, id DESC LIMIT 1`, project, sig)
	if err != nil || len(list) == 0 {
		return nil, err
	}
	return list[0], nil
}

// ListLoopEvents returns a project's events, newest first (limit <= 0: all).
func (s *Store) ListLoopEvents(project string, limit int) ([]*LoopEvent, error) {
	q := `SELECT ` + selectLoopEventCols + ` FROM loop_events WHERE project=? ORDER BY ts DESC, id DESC`
	if limit > 0 {
		q += ` LIMIT ?`
		return s.queryLoopEvents(q, project, limit)
	}
	return s.queryLoopEvents(q, project)
}

func (s *Store) queryLoopEvents(q string, args ...any) ([]*LoopEvent, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*LoopEvent
	for rows.Next() {
		e, err := scanLoopEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
