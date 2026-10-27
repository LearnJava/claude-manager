package store

import "time"

// LearnedSyncHashes returns the synced hash of every lesson of a project,
// keyed "<kind>\x00<key>" (WASTE-TASKS.md WS-09).
func (s *Store) LearnedSyncHashes(project string) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT kind, key, hash FROM learned_sync WHERE project=?`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var kind, key, hash string
		if err := rows.Scan(&kind, &key, &hash); err != nil {
			return nil, err
		}
		out[kind+"\x00"+key] = hash
	}
	return out, rows.Err()
}

// SetLearnedSync records that the database and the file agreed on hash.
func (s *Store) SetLearnedSync(project, kind, key, hash string, at time.Time) error {
	_, err := s.db.Exec(`INSERT INTO learned_sync (project, kind, key, hash, synced_at) VALUES (?, ?, ?, ?, ?)
	    ON CONFLICT(project, kind, key) DO UPDATE SET hash=excluded.hash, synced_at=excluded.synced_at`,
		project, kind, key, hash, at.UTC())
	return err
}

// DeleteLearnedSync forgets a lesson that no longer exists on either side.
func (s *Store) DeleteLearnedSync(project, kind, key string) error {
	_, err := s.db.Exec(`DELETE FROM learned_sync WHERE project=? AND kind=? AND key=?`, project, kind, key)
	return err
}

// UpdateHookRuleHint replaces the "do this instead" text of a rule (an import
// of a changed lesson file, WS-09).
func (s *Store) UpdateHookRuleHint(id int64, hint string) error {
	_, err := s.db.Exec(`UPDATE hook_rules SET hint=? WHERE id=?`, hint, id)
	return err
}
