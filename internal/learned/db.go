package learned

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"claude-manager/internal/store"
)

func rawJSON(s string) json.RawMessage {
	s = strings.TrimSpace(s)
	if s != "" && json.Valid([]byte(s)) {
		return json.RawMessage(s)
	}
	return nil
}

// Collect returns the project's lessons that are in force in the database:
// skills that are applied or on trial, rules and bans that are on trial or
// kept. A skill counts by its newest row, so a skill archived after an older
// approval is not exported. Sorted by kind and key.
func Collect(st *store.Store, project string) ([]Lesson, error) {
	var out []Lesson

	skills, err := st.ListSkills(project) // newest first
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, sk := range skills {
		if seen[sk.Name] {
			continue
		}
		seen[sk.Name] = true
		if !liveSkill(sk.Status) {
			continue
		}
		l := Lesson{Kind: KindSkill, Key: sk.Name, Title: sk.Name, Status: sk.Status, Origin: sk.Origin,
			Body: sk.MD, Appeared: utc(sk.CreatedAt), Decided: utcPtr(sk.ApprovedAt)}
		l.Effect, l.EffectText = effectOf(sk.Reason)
		l.Evidence = &Evidence{Draft: rawJSON(sk.DraftJSON), Source: rawJSON(sk.SourceJSON)}
		out = append(out, l)
	}

	rules, err := st.ListFailureRules(project)
	if err != nil {
		return nil, err
	}
	for _, r := range rules {
		if !liveRule(r.Status) {
			continue
		}
		l := Lesson{Kind: KindRule, Key: r.ErrorKey, Title: clip(r.RuleText, 80), Status: r.Status, Origin: r.Origin,
			Body: r.RuleText, Appeared: utc(r.CreatedAt), Decided: utcPtr(r.DecidedAt)}
		l.Effect, l.EffectText = effectOf(r.Reason)
		l.Evidence = &Evidence{Examples: rawJSON(r.ExamplesJSON), Runs: r.DistinctRuns}
		out = append(out, l)
	}

	bans, err := st.ListHookRules(project)
	if err != nil {
		return nil, err
	}
	for _, r := range bans {
		if !liveBan(r.Status) {
			continue
		}
		l := Lesson{Kind: KindBan, Key: banKey(r.Kind, r.Param), Title: clip(r.Kind+" "+r.Param, 80), Status: r.Status,
			Origin: r.Origin, Body: r.Hint, Sub: r.Kind, Param: r.Param, Appeared: utc(r.CreatedAt), Decided: utcPtr(r.DecidedAt)}
		l.Effect, l.EffectText = effectOf(r.Reason)
		out = append(out, l)
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

// importLesson makes the database hold l: it updates the row that already has
// l's key (whatever its status — an archived or reverted row is brought back)
// or inserts one. Skill files are the caller's business.
func importLesson(st *store.Store, project string, l Lesson, now time.Time) error {
	decided := l.Decided
	if decided == nil {
		d := now.UTC()
		decided = &d
	}
	switch l.Kind {
	case KindSkill:
		rows, err := st.ListSkills(project)
		if err != nil {
			return err
		}
		for _, sk := range rows {
			if sk.Name == l.Key {
				return st.UpdateSkillApproved(sk.ID, l.Body, l.Status, l.reasonOf(), *decided)
			}
		}
		sk := &store.Skill{Project: project, Name: l.Key, Status: l.Status, MD: l.Body, Origin: l.Origin,
			Reason: l.reasonOf(), CreatedAt: l.Appeared, ApprovedAt: decided, DraftJSON: "{}", SourceJSON: "[]"}
		if l.Evidence != nil {
			if len(l.Evidence.Draft) > 0 {
				sk.DraftJSON = string(l.Evidence.Draft)
			}
			if len(l.Evidence.Source) > 0 {
				sk.SourceJSON = string(l.Evidence.Source)
			}
		}
		return st.InsertSkill(sk)
	case KindRule:
		rows, err := st.ListFailureRules(project)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.ErrorKey == l.Key {
				if err := st.UpdateFailureRule(r.ID, l.Status, l.Body, decided); err != nil {
					return err
				}
				return st.SetFailureRuleReason(r.ID, l.reasonOf())
			}
		}
		r := &store.FailureRule{Project: project, ErrorKey: l.Key, RuleText: l.Body, Status: l.Status, Origin: l.Origin,
			Reason: l.reasonOf(), CreatedAt: l.Appeared, DecidedAt: decided}
		if l.Evidence != nil {
			r.ExamplesJSON, r.DistinctRuns = string(l.Evidence.Examples), l.Evidence.Runs
		}
		return st.InsertFailureRule(r)
	case KindBan:
		rows, err := st.ListHookRules(project)
		if err != nil {
			return err
		}
		for _, r := range rows {
			if r.Kind == l.Sub && r.Param == l.Param {
				if err := st.UpdateHookRuleHint(r.ID, l.Body); err != nil {
					return err
				}
				return st.UpdateHookRule(r.ID, l.Status, decided, l.reasonOf())
			}
		}
		return st.InsertHookRule(&store.HookRule{Project: project, Kind: l.Sub, Param: l.Param, Hint: l.Body,
			Status: l.Status, Origin: l.Origin, Reason: l.reasonOf(), CreatedAt: l.Appeared, DecidedAt: decided})
	}
	return nil
}

// retireReason is the decision reason of a lesson whose file was removed.
const retireReason = `{"code":"file_removed"}`

// retireLesson takes a lesson out of force in the database: a skill is
// archived, a rule or ban reverted. It returns the id of the archived skill, whose file the
// caller should move out of .claude/skills (0 for anything else).
func retireLesson(st *store.Store, project string, ref Ref, now time.Time) (skillID int64, err error) {
	switch ref.Kind {
	case KindSkill:
		rows, err := st.ListSkills(project)
		if err != nil {
			return 0, err
		}
		for _, sk := range rows {
			if sk.Name == ref.Key {
				return sk.ID, st.UpdateSkillStatus(sk.ID, store.SkillStatusArchived, retireReason, now.UTC())
			}
		}
	case KindRule:
		rows, err := st.ListFailureRules(project)
		if err != nil {
			return 0, err
		}
		for _, r := range rows {
			if r.ErrorKey == ref.Key {
				d := now.UTC()
				if err := st.UpdateFailureRule(r.ID, store.FailureRuleReverted, r.RuleText, &d); err != nil {
					return 0, err
				}
				return 0, st.SetFailureRuleReason(r.ID, retireReason)
			}
		}
	case KindBan:
		rows, err := st.ListHookRules(project)
		if err != nil {
			return 0, err
		}
		for _, r := range rows {
			if banKey(r.Kind, r.Param) == ref.Key {
				d := now.UTC()
				return 0, st.UpdateHookRule(r.ID, store.HookRuleReverted, &d, retireReason)
			}
		}
	}
	return 0, nil
}
