package experience

import (
	"encoding/json"
	"strings"
	"time"

	"claude-manager/internal/store"
)

// SkillUsage is how much one skill is actually used (LEARN-TASKS.md LN-24):
// loads are `Skill` (Claude Code) / `skill_view` (Hermes) tool calls naming
// it — the agent pulled the skill's body into its context. "Since applied"
// counts start at the skill's ApprovedAt (its current trial window).
type SkillUsage struct {
	SkillID int64  `json:"skill_id"`
	Name    string `json:"name"`
	// Loads is every recorded load of this name in the project, ever.
	Loads        int        `json:"loads"`
	LastLoadedAt *time.Time `json:"last_loaded_at,omitempty"`
	// RunsSinceApplied is how many of the project's indexed runs started
	// after ApprovedAt; RunsWithLoad is how many of those loaded the skill at
	// least once. Both 0 for a skill that was never applied.
	RunsSinceApplied int `json:"runs_since_applied"`
	RunsWithLoad     int `json:"runs_with_load"`
	// RunsSinceLastLoad is how many of the project's runs started after the
	// last load (or after ApprovedAt, if never loaded since) — the
	// "unused lately" clock.
	RunsSinceLastLoad int `json:"runs_since_last_load"`
}

// SkillNameFromLoad extracts the skill name from one recorded load. Claude
// Code's `Skill` tool input is JSON ({"skill":"name","args":"…"}), possibly
// abbreviated; Hermes's `skill_view` arg is the bare name. A plugin-scoped
// name ("plugin:name") is reduced to its last segment — the directory name a
// project skill is written under. Returns "" when no name can be recovered.
func SkillNameFromLoad(tool, arg string) string {
	arg = strings.TrimSpace(arg)
	var name string
	switch tool {
	case "Skill":
		var in struct {
			Skill string `json:"skill"`
		}
		if err := json.Unmarshal([]byte(arg), &in); err == nil {
			name = in.Skill
		} else if i := strings.Index(arg, `"skill":"`); i >= 0 {
			// Abbreviated (truncated) JSON: take the value up to its quote.
			rest := arg[i+len(`"skill":"`):]
			if j := strings.IndexByte(rest, '"'); j >= 0 {
				name = rest[:j]
			}
		} else if !strings.ContainsAny(arg, "{} ") {
			name = arg
		}
	case "skill_view":
		name = arg
	}
	if i := strings.LastIndexByte(name, ':'); i >= 0 {
		name = name[i+1:]
	}
	return strings.TrimSpace(name)
}

// BuildSkillUsage computes SkillUsage for every skill row in project, keyed
// by skill ID. Loads are matched by name, so two rows sharing a name (a
// re-distillation) share the same load history; the "since applied" counts
// still differ by each row's own ApprovedAt.
func BuildSkillUsage(st *store.Store, project string) (map[int64]SkillUsage, error) {
	skills, err := st.ListSkills(project)
	if err != nil {
		return nil, err
	}
	loads, err := st.ListSkillLoads(project)
	if err != nil {
		return nil, err
	}
	runs, err := indexedRuns(st, project)
	if err != nil {
		return nil, err
	}
	return computeSkillUsage(skills, loads, runs), nil
}

// indexedRuns is the project's runs (newest first) restricted to the ones
// whose transcript was indexed — see store.IndexedRunIDs for why an
// unindexed run must not count as "the skill was not loaded".
func indexedRuns(st *store.Store, project string) ([]*store.SessionRun, error) {
	runs, err := st.ListRuns(project, "", 0)
	if err != nil {
		return nil, err
	}
	indexed, err := st.IndexedRunIDs(project)
	if err != nil {
		return nil, err
	}
	out := runs[:0]
	for _, r := range runs {
		if indexed[r.ID] {
			out = append(out, r)
		}
	}
	return out, nil
}

// loadsByName groups loads under the skill name each one names.
func loadsByName(loads []store.SkillLoad) map[string][]store.SkillLoad {
	out := make(map[string][]store.SkillLoad)
	for _, l := range loads {
		if name := SkillNameFromLoad(l.Tool, l.Arg); name != "" {
			out[name] = append(out[name], l)
		}
	}
	return out
}

func computeSkillUsage(skills []store.Skill, loads []store.SkillLoad, runs []*store.SessionRun) map[int64]SkillUsage {
	byName := loadsByName(loads)
	out := make(map[int64]SkillUsage, len(skills))
	for _, sk := range skills {
		u := SkillUsage{SkillID: sk.ID, Name: sk.Name}
		ls := byName[sk.Name]
		u.Loads = len(ls)
		if len(ls) > 0 {
			last := ls[len(ls)-1].TS
			u.LastLoadedAt = &last
		}
		if sk.ApprovedAt != nil {
			since := *sk.ApprovedAt
			loadedRuns := make(map[int64]bool)
			var lastLoadSince *time.Time
			for _, l := range ls {
				if l.TS.Before(since) {
					continue
				}
				ts := l.TS
				lastLoadSince = &ts
				if l.RunID != nil {
					loadedRuns[*l.RunID] = true
				}
			}
			clock := since
			if lastLoadSince != nil {
				clock = *lastLoadSince
			}
			for _, r := range runs {
				if r.StartedAt.Before(since) {
					continue
				}
				u.RunsSinceApplied++
				if loadedRuns[r.ID] {
					u.RunsWithLoad++
				}
				if r.StartedAt.After(clock) {
					u.RunsSinceLastLoad++
				}
			}
		}
		out[sk.ID] = u
	}
	return out
}
