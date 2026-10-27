package store

import (
	"database/sql"
	"time"
)

// Hook rule statuses (WASTE-TASKS.md WS-03). draft is proposed and installs
// nothing; trial is in the project's hook and being measured; kept passed the
// measurement; reverted was taken out (by the measurement or by hand);
// dismissed was refused and is not proposed again.
const (
	HookRuleDraft     = "draft"
	HookRuleTrial     = "trial"
	HookRuleKept      = "kept"
	HookRuleReverted  = "reverted"
	HookRuleDismissed = "dismissed"
)

// HookRule is one rule of the generated PreToolUse hook. Kind is one of
// experience.HookKind*; Param is the kind's argument (the oversized-read
// token threshold, the blocked command text); Hint is what to do instead.
type HookRule struct {
	ID        int64
	Project   string
	Kind      string
	Param     string
	Hint      string
	Status    string
	Origin    string
	Reason    string // JSON of experience.HookRuleReason for a decided rule
	CreatedAt time.Time
	DecidedAt *time.Time
}

const selectHookRuleCols = `id, project, kind, param, hint, status, origin, reason, created_at, decided_at`

func scanHookRule(sc interface{ Scan(...any) error }) (*HookRule, error) {
	var r HookRule
	var decided sql.NullTime
	if err := sc.Scan(&r.ID, &r.Project, &r.Kind, &r.Param, &r.Hint, &r.Status, &r.Origin,
		&r.Reason, &r.CreatedAt, &decided); err != nil {
		return nil, err
	}
	if decided.Valid {
		t := decided.Time
		r.DecidedAt = &t
	}
	return &r, nil
}

// InsertHookRule inserts a rule and sets r.ID. A second row for the same
// (project, kind, param) fails on the UNIQUE constraint.
func (s *Store) InsertHookRule(r *HookRule) error {
	if r.Status == "" {
		r.Status = HookRuleDraft
	}
	if r.Origin == "" {
		r.Origin = "manual"
	}
	if r.CreatedAt.IsZero() {
		r.CreatedAt = time.Now().UTC()
	}
	var decided any
	if r.DecidedAt != nil {
		decided = *r.DecidedAt
	}
	res, err := s.db.Exec(`INSERT INTO hook_rules (project, kind, param, hint, status, origin, reason, created_at, decided_at)
	    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Project, r.Kind, r.Param, r.Hint, r.Status, r.Origin, r.Reason, r.CreatedAt, decided)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

// ListHookRules returns every hook rule of a project, newest first.
func (s *Store) ListHookRules(project string) ([]HookRule, error) {
	rows, err := s.db.Query(`SELECT `+selectHookRuleCols+` FROM hook_rules WHERE project=? ORDER BY id DESC`, project)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HookRule
	for rows.Next() {
		r, err := scanHookRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *r)
	}
	return out, rows.Err()
}

// GetHookRule returns a rule by ID, or nil if not found.
func (s *Store) GetHookRule(id int64) (*HookRule, error) {
	r, err := scanHookRule(s.db.QueryRow(`SELECT `+selectHookRuleCols+` FROM hook_rules WHERE id=?`, id))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return r, err
}

// UpdateHookRule sets the status, decision time and decision reason of a rule.
func (s *Store) UpdateHookRule(id int64, status string, decidedAt *time.Time, reason string) error {
	var decided any
	if decidedAt != nil {
		decided = *decidedAt
	}
	_, err := s.db.Exec(`UPDATE hook_rules SET status=?, decided_at=?, reason=? WHERE id=?`, status, decided, reason, id)
	return err
}
