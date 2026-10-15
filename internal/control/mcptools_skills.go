package control

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"

	"claude-manager/internal/experience"
)

// Tools of the experience layer, the "Skills" area (MCP-11). Nothing here
// switches a feature on behind its flag: the tools report which flag a result
// depends on ([optimization] experience_tracking, a project's auto_skills) and
// set_skill_autopilot is the one explicit, admin-level way to turn it on.

type skillsIn struct {
	projectPageIn
	Status    string `json:"status,omitempty" jsonschema:"Only skills in this status: draft, rejected, trial, approved or archived; omit for all"`
	IncludeMD bool   `json:"include_md,omitempty" jsonschema:"True to include the skill text (md) of every item; off by default because skill texts are long"`
}

type distillSkillIn struct {
	Project  string   `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	Sig      []string `json:"sig" jsonschema:"Signature sequence of the candidate to distil, the sig of an item of get_skill_candidates, e.g. [\"Bash:go test\",\"Edit\"]"`
	Model    string   `json:"model,omitempty" jsonschema:"Model that writes the skill, e.g. sonnet; default the distiller model"`
	MinScore float64  `json:"min_score,omitempty" jsonschema:"Lowest candidate score to accept; omit or 0 for the relative threshold (the best share of the project's candidates)"`
}

type skillIDIn struct {
	SkillID string `json:"skill_id" jsonschema:"Skill ID, the id of an item of get_skills or distill_skill, e.g. 7"`
}

type approveSkillIn struct {
	Project   string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen; used to find the stored draft text when md is omitted"`
	SkillID   string `json:"skill_id" jsonschema:"Skill ID, the id of an item of get_skills or distill_skill, e.g. 7"`
	MD        string `json:"md,omitempty" jsonschema:"Final skill text (SKILL.md) to write; omit to write the draft as distilled"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"True to replace a skill file that already exists in the project; otherwise the call fails when it exists"`
}

type restoreSkillIn struct {
	SkillID   string `json:"skill_id" jsonschema:"Skill ID of an archived or rejected skill, the id of an item of get_skills, e.g. 7"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"True to replace a skill file that already exists in the project; otherwise the call fails when it exists"`
}

type setAutopilotIn struct {
	Project        string  `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	Enabled        bool    `json:"enabled" jsonschema:"True to turn the skill autopilot on for the project, false to turn it off"`
	DailyBudgetUSD float64 `json:"daily_budget_usd,omitempty" jsonschema:"Most the autopilot may spend on distilling per day, in US dollars; 0 or omitted means no cap"`
}

type skillCandidatesOut struct {
	Candidates []any `json:"candidates" jsonschema:"Ranked skill candidates: sig, distinct_runs, run_share, score, kind, context_loss_suspect, imported, related_failures (count), first_seen and last_seen; empty without enough history"`
	pageOut
}

type skillsOut struct {
	Skills []any `json:"skills" jsonschema:"Skills of the project: id, name, status, origin, reason, cost_usd, created_at, approved_at, archived_at, updated_at, and md when asked"`
	pageOut
}

type skillOut struct {
	Skill any `json:"skill" jsonschema:"The skill draft: id, name, status, md and the other fields of get_skills"`
}

type skillPathAck struct {
	OK      bool   `json:"ok" jsonschema:"True when the action was done"`
	SkillID string `json:"skill_id" jsonschema:"The skill acted on"`
	Path    string `json:"path,omitempty" jsonschema:"File written into the project; empty when the action writes none"`
}

type skillUsageOut struct {
	Usage []any `json:"usage" jsonschema:"Per skill: skill_id, name, loads, last_loaded_at, runs_since_applied, runs_with_load and runs_since_last_load"`
	pageOut
}

type skillQualityOut struct {
	Effects []any `json:"effects" jsonschema:"Per approved skill: skill_id, skill_name, approved_at and the before and after run statistics, insufficient_data and stale"`
	pageOut
}

type autopilotOut struct {
	Enabled        bool    `json:"enabled" jsonschema:"True when auto_skills is on for the project"`
	Tracking       bool    `json:"tracking" jsonschema:"True when [optimization] experience_tracking is on; without it nothing is indexed and the autopilot does not run"`
	DailyBudgetUSD float64 `json:"daily_budget_usd" jsonschema:"Daily spending cap in US dollars; 0 means no cap"`
	SpentTodayUSD  float64 `json:"spent_today_usd" jsonschema:"Spent today on auto-distilled skills"`
	SpentTotalUSD  float64 `json:"spent_total_usd" jsonschema:"Spent in all on auto-distilled skills"`
	TrialRuns      int64   `json:"trial_runs" jsonschema:"Runs a skill stays in trial before the autopilot judges it"`
}

type autopilotAck struct {
	OK             bool    `json:"ok" jsonschema:"True when the setting was saved"`
	Project        string  `json:"project" jsonschema:"The project changed"`
	Enabled        bool    `json:"enabled" jsonschema:"The autopilot state now"`
	DailyBudgetUSD float64 `json:"daily_budget_usd" jsonschema:"The daily cap now; 0 means no cap"`
}

type pilotReportOut struct {
	Applied  []string `json:"applied" jsonschema:"Skills put into the project as a trial in this step"`
	Kept     []string `json:"kept" jsonschema:"Trial skills judged good and approved"`
	Archived []string `json:"archived" jsonschema:"Skills switched off in this step"`
	Rejected []string `json:"rejected" jsonschema:"Drafts the reviewer turned down"`
	Idle     string   `json:"idle" jsonschema:"Why nothing happened, e.g. not enough runs; empty when something did"`
}

const trackingHint = "Skill candidates come from the action history, which is only recorded with [optimization] experience_tracking = true (get_skill_autopilot shows it as tracking) and needs at least %d runs sharing a pattern."

// skillItem keeps the fields of a stored skill an agent needs; the long texts
// stay out unless asked for.
func skillItem(raw any, withMD bool) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	out := map[string]any{
		"id": m["ID"], "name": m["Name"], "status": m["Status"], "origin": m["Origin"], "reason": m["Reason"],
		"cost_usd": m["CostUSD"], "created_at": m["CreatedAt"], "approved_at": m["ApprovedAt"],
		"archived_at": m["ArchivedAt"], "updated_at": m["UpdatedAt"],
	}
	if withMD {
		out["md"] = m["MD"]
	}
	return out
}

func candidateItem(raw any) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	related, _ := m["RelatedFailures"].([]any)
	return map[string]any{
		"sig": m["Sig"], "distinct_runs": m["DistinctRuns"], "run_share": m["RunShare"], "score": m["Score"],
		"kind": m["Kind"], "context_loss_suspect": m["ContextLossSuspect"], "imported": m["Imported"],
		"related_failures": len(related), "first_seen": m["FirstSeen"], "last_seen": m["LastSeen"],
	}
}

func strList(raw any, key string) []string {
	m, _ := raw.(map[string]any)
	l, _ := m[key].([]any)
	out := make([]string, 0, len(l))
	for _, v := range l {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func needSkillID(id string) error {
	if _, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64); err != nil {
		return fmt.Errorf("skill_id %q is not a number: take the id of an item of get_skills, e.g. 7", id)
	}
	return nil
}

func skillsTools() []Tool {
	ro := ToolAnnotations{ReadOnly: true}

	return []Tool{
		define(spec[projectPageIn, skillCandidatesOut]{
			Name: "get_skill_candidates", Title: "Get skill candidates",
			Doc: Doc{
				What:    "List the action patterns of a project's past runs that are worth turning into a skill, best first.",
				UseWhen: "you look for a repeated workflow to capture before distill_skill.",
				NotFor:  "skills that already exist (use get_skills).",
				Returns: "candidates with their sig, score and run counts, with next_cursor and truncated when more exist; empty when experience tracking is off or the history is short.",
				Effects: "none, read only.",
				Example: `{"project":"lumen"}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetSkillCandidates"},
			Translate: func(in projectPageIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if _, _, err := in.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetSkillCandidates", map[string]string{"project": in.Project}), nil
			},
			Result: func(in projectPageIn, raw any) skillCandidatesOut {
				all := list(raw)
				items := make([]any, len(all))
				for i, c := range all {
					items[i] = candidateItem(c)
				}
				page, p := listPage(in.pageIn, items)
				return skillCandidatesOut{Candidates: page, pageOut: p}
			},
			Summary: func(in projectPageIn, out skillCandidatesOut) string {
				if len(out.Candidates) == 0 && !out.Truncated {
					return "No skill candidates in " + in.Project + ". " + fmt.Sprintf(trackingHint, experience.MinCandidateRuns)
				}
				return plural(len(out.Candidates), "skill candidate", "skill candidates") + " in " + in.Project + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[distillSkillIn, skillOut]{
			Name: "distill_skill", Title: "Distil a skill",
			Doc: Doc{
				What:    "Turn one skill candidate into a skill draft with a model run and save it as a draft; nothing is written into the project yet.",
				UseWhen: "get_skill_candidates lists a pattern worth capturing and you want its draft to review.",
				NotFor:  "writing the skill into the project (use approve_skill) or the automatic loop (use run_skill_autopilot).",
				Returns: "the draft skill, including its id and md; fails with the score and threshold when the candidate is below the threshold.",
				Effects: "runs a model session that spends tokens; the project is not changed until approve_skill.",
				Example: `{"project":"lumen","sig":["Bash:go test","Edit"]}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"DistillSkill"},
			Translate: func(in distillSkillIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if len(in.Sig) == 0 {
					return "", nil, fmt.Errorf(`sig is required: pass the sig of an item of get_skill_candidates, e.g. ["Bash:go test","Edit"]`)
				}
				if in.MinScore < 0 {
					return "", nil, fmt.Errorf("min_score must not be negative; omit it for the relative threshold")
				}
				return "/rpc", rpcCallBody("DistillSkill", map[string]any{
					"project": in.Project, "sig": in.Sig, "model": in.Model, "min_score": in.MinScore,
				}), nil
			},
			Result: func(_ distillSkillIn, raw any) skillOut { return skillOut{Skill: skillItem(raw, true)} },
			Summary: func(in distillSkillIn, out skillOut) string {
				m, _ := out.Skill.(map[string]any)
				return fmt.Sprintf("Skill draft %v (%v) distilled for %s; review it, then approve_skill.", m["id"], m["name"], in.Project)
			},
		}),
		define(spec[skillsIn, skillsOut]{
			Name: "get_skills", Title: "Get skills",
			Doc: Doc{
				What:    "List the skills of a project in every status: drafts, trials, approved, rejected and archived.",
				UseWhen: "you review what the experience layer produced or look up a skill id.",
				NotFor:  "how much a skill is used (use get_skill_usage) or whether it helps (use get_skill_quality).",
				Returns: "skills with id, name, status, origin and dates (and md when asked), with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{"project":"lumen","status":"draft"}`,
			},
			Input: func(props map[string]*jsonschema.Schema) {
				tunePage(props)
				props["status"].Enum = []any{"draft", "rejected", "trial", "approved", "archived"}
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetSkills"},
			Translate: func(in skillsIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if _, _, err := in.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetSkills", map[string]string{"project": in.Project}), nil
			},
			Result: func(in skillsIn, raw any) skillsOut {
				var items []any
				for _, sk := range list(raw) {
					if m, ok := sk.(map[string]any); ok && in.Status != "" && m["Status"] != in.Status {
						continue
					}
					items = append(items, skillItem(sk, in.IncludeMD))
				}
				page, p := listPage(in.pageIn, items)
				return skillsOut{Skills: page, pageOut: p}
			},
			Summary: func(in skillsIn, out skillsOut) string {
				return plural(len(out.Skills), "skill", "skills") + " in " + in.Project + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[approveSkillIn, skillPathAck]{
			Name: "approve_skill", Title: "Approve skill",
			Doc: Doc{
				What:    "Write a skill draft into the project as .claude/skills/<name>/SKILL.md so the agents load it.",
				UseWhen: "distill_skill or get_skills gave a draft you are authorised to make real.",
				NotFor:  "bringing back an archived skill (use restore_skill).",
				Returns: "ok, the skill_id and the path of the file written.",
				Effects: "writes a file into the project folder; with the project's auto_skills on the skill starts a trial the autopilot judges, otherwise it is approved; overwrite=true replaces an existing file.",
				Example: `{"project":"lumen","skill_id":"7"}`,
			},
			Level: LevelAdmin, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"ApproveSkill"},
			Translate: func(in approveSkillIn) (string, any, error) {
				if err := needSkillID(in.SkillID); err != nil {
					return "", nil, err
				}
				if strings.TrimSpace(in.MD) == "" {
					if err := needProject(in.Project); err != nil {
						return "", nil, err
					}
				}
				return "/rpc", rpcCallBody("ApproveSkill", map[string]any{
					"project": in.Project, "skill_id": in.SkillID, "md": in.MD, "overwrite": in.Overwrite,
				}), nil
			},
			Result: func(in approveSkillIn, raw any) skillPathAck {
				m, _ := raw.(map[string]any)
				path, _ := m["path"].(string)
				return skillPathAck{OK: true, SkillID: in.SkillID, Path: path}
			},
			Summary: func(in approveSkillIn, out skillPathAck) string {
				return "Skill " + in.SkillID + " written to " + out.Path + "."
			},
		}),
		define(spec[skillIDIn, skillPathAck]{
			Name: "archive_skill", Title: "Archive skill",
			Doc: Doc{
				What:    "Switch a skill off: an applied skill's folder is moved out of .claude/skills so the agents stop seeing it.",
				UseWhen: "a skill is stale or hurts, and you want it off without losing it.",
				NotFor:  "deleting it for good, or bringing it back (use restore_skill).",
				Returns: "ok and the skill_id.",
				Effects: "moves the skill folder to .claude-manager/archived-skills inside the project; a draft or rejected row is only marked archived.",
				Example: `{"skill_id":"7"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"ArchiveSkill"},
			Translate: func(in skillIDIn) (string, any, error) {
				if err := needSkillID(in.SkillID); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("ArchiveSkill", map[string]string{"skill_id": in.SkillID}), nil
			},
			Result:  func(in skillIDIn, _ any) skillPathAck { return skillPathAck{OK: true, SkillID: in.SkillID} },
			Summary: func(in skillIDIn, _ skillPathAck) string { return "Skill " + in.SkillID + " archived." },
		}),
		define(spec[restoreSkillIn, skillPathAck]{
			Name: "restore_skill", Title: "Restore skill",
			Doc: Doc{
				What:    "Bring an archived skill back into .claude/skills, or apply a rejected one anyway.",
				UseWhen: "an archived skill is needed again, or you overrule the autopilot's rejection.",
				NotFor:  "a fresh draft (use approve_skill).",
				Returns: "ok, the skill_id and the path of the file written.",
				Effects: "writes a file into the project folder; with the project's auto_skills on a new trial starts, otherwise the skill is approved; overwrite=true replaces an existing file.",
				Example: `{"skill_id":"7"}`,
			},
			Level: LevelAdmin, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"RestoreSkill"},
			Translate: func(in restoreSkillIn) (string, any, error) {
				if err := needSkillID(in.SkillID); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("RestoreSkill", map[string]any{"skill_id": in.SkillID, "overwrite": in.Overwrite}), nil
			},
			Result: func(in restoreSkillIn, raw any) skillPathAck {
				m, _ := raw.(map[string]any)
				path, _ := m["path"].(string)
				return skillPathAck{OK: true, SkillID: in.SkillID, Path: path}
			},
			Summary: func(in restoreSkillIn, out skillPathAck) string {
				return "Skill " + in.SkillID + " restored to " + out.Path + "."
			},
		}),
		define(spec[projectPageIn, skillUsageOut]{
			Name: "get_skill_usage", Title: "Get skill usage",
			Doc: Doc{
				What:    "Show how often agents actually load each skill of a project.",
				UseWhen: "you decide whether a skill is used or should be archived.",
				NotFor:  "whether a skill improves runs (use get_skill_quality).",
				Returns: "one entry per skill with loads and runs counts, with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{"project":"lumen"}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetSkillUsage"},
			Translate: func(in projectPageIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if _, _, err := in.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetSkillUsage", map[string]string{"project": in.Project}), nil
			},
			Result: func(in projectPageIn, raw any) skillUsageOut {
				m, _ := raw.(map[string]any)
				keys := make([]string, 0, len(m))
				for k := range m {
					keys = append(keys, k)
				}
				sort.Slice(keys, func(i, j int) bool {
					a, _ := strconv.Atoi(keys[i])
					b, _ := strconv.Atoi(keys[j])
					return a < b
				})
				items := make([]any, 0, len(keys))
				for _, k := range keys {
					items = append(items, m[k])
				}
				page, p := listPage(in.pageIn, items)
				return skillUsageOut{Usage: page, pageOut: p}
			},
			Summary: func(in projectPageIn, out skillUsageOut) string {
				return "Usage of " + plural(len(out.Usage), "skill", "skills") + " in " + in.Project + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[projectPageIn, skillQualityOut]{
			Name: "get_skill_quality", Title: "Get skill quality",
			Doc: Doc{
				What:    "Compare the runs before and after each approved skill of a project: does the skill help.",
				UseWhen: "you judge a skill's effect before keeping or archiving it.",
				NotFor:  "how often a skill is loaded (use get_skill_usage).",
				Returns: "one effect per approved skill, with insufficient_data and stale flags, with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{"project":"lumen"}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetSkillQuality"},
			Translate: func(in projectPageIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if _, _, err := in.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetSkillQuality", map[string]string{"project": in.Project}), nil
			},
			Result: func(in projectPageIn, raw any) skillQualityOut {
				page, p := listPage(in.pageIn, raw)
				return skillQualityOut{Effects: page, pageOut: p}
			},
			Summary: func(in projectPageIn, out skillQualityOut) string {
				return "Effect of " + plural(len(out.Effects), "approved skill", "approved skills") + " in " + in.Project + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[projectIn, autopilotOut]{
			Name: "get_skill_autopilot", Title: "Get skill autopilot",
			Doc: Doc{
				What:    "Show whether the skill autopilot is on for a project, its daily budget and what it has spent.",
				UseWhen: "you check the experience layer's switches before relying on candidates or the autopilot.",
				NotFor:  "changing them (use set_skill_autopilot).",
				Returns: "enabled (auto_skills), tracking (experience_tracking), budget and spending, trial_runs.",
				Effects: "none, read only.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetSkillAutopilot"},
			Translate: func(in projectIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetSkillAutopilot", map[string]string{"project": in.Project}), nil
			},
			Result: func(_ projectIn, raw any) autopilotOut {
				m, _ := raw.(map[string]any)
				b := func(k string) bool { v, _ := m[k].(bool); return v }
				f := func(k string) float64 { v, _ := m[k].(float64); return v }
				return autopilotOut{
					Enabled: b("enabled"), Tracking: b("tracking"), DailyBudgetUSD: f("daily_budget_usd"),
					SpentTodayUSD: f("spent_today_usd"), SpentTotalUSD: f("spent_total_usd"), TrialRuns: int64(f("trial_runs")),
				}
			},
			Summary: func(in projectIn, out autopilotOut) string {
				switch {
				case !out.Enabled:
					return "Skill autopilot is off for " + in.Project + " (auto_skills); set_skill_autopilot turns it on."
				case !out.Tracking:
					return "Skill autopilot is on for " + in.Project + " but [optimization] experience_tracking is off, so it sees nothing."
				}
				return fmt.Sprintf("Skill autopilot is on for %s: $%.2f spent today.", in.Project, out.SpentTodayUSD)
			},
		}),
		define(spec[setAutopilotIn, autopilotAck]{
			Name: "set_skill_autopilot", Title: "Set skill autopilot",
			Doc: Doc{
				What:    "Turn the skill autopilot on or off for a project and set its daily budget.",
				UseWhen: "you are authorised to let the experience layer distil, trial and judge skills on its own.",
				NotFor:  "a single manual step (use distill_skill and approve_skill) or running one step now (use run_skill_autopilot).",
				Returns: "ok and the state and budget now in force.",
				Effects: "writes the project's private config.local.toml; turning it on also turns on [optimization] experience_tracking, and the autopilot then spends tokens and writes skills into the project.",
				Example: `{"project":"lumen","enabled":true,"daily_budget_usd":2}`,
			},
			Input: func(props map[string]*jsonschema.Schema) { props["daily_budget_usd"].Minimum = fptr(0) },
			Level: LevelAdmin, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"SetSkillAutopilot"},
			Translate: func(in setAutopilotIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if in.DailyBudgetUSD < 0 {
					return "", nil, fmt.Errorf("daily_budget_usd must not be negative; use 0 or omit it for no cap")
				}
				return "/rpc", rpcCallBody("SetSkillAutopilot", map[string]any{
					"project": in.Project, "enabled": in.Enabled, "daily_budget_usd": in.DailyBudgetUSD,
				}), nil
			},
			Result: func(in setAutopilotIn, _ any) autopilotAck {
				return autopilotAck{OK: true, Project: in.Project, Enabled: in.Enabled, DailyBudgetUSD: in.DailyBudgetUSD}
			},
			Summary: func(in setAutopilotIn, _ autopilotAck) string {
				if in.Enabled {
					return "Skill autopilot turned on for " + in.Project + "."
				}
				return "Skill autopilot turned off for " + in.Project + "."
			},
		}),
		define(spec[projectIn, pilotReportOut]{
			Name: "run_skill_autopilot", Title: "Run skill autopilot",
			Doc: Doc{
				What:    "Run one autopilot step for a project now instead of waiting for the next finished run.",
				UseWhen: "get_skill_autopilot shows it on and you want a step immediately.",
				NotFor:  "turning it on (use set_skill_autopilot); it fails while auto_skills is off.",
				Returns: "what the step applied, kept, archived and rejected, or why it was idle.",
				Effects: "may run model sessions up to the daily budget and write or archive skill files in the project.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"RunSkillAutopilot"},
			Translate: func(in projectIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("RunSkillAutopilot", map[string]string{"project": in.Project}), nil
			},
			Result: func(_ projectIn, raw any) pilotReportOut {
				m, _ := raw.(map[string]any)
				idle, _ := m["idle"].(string)
				return pilotReportOut{
					Applied: strList(raw, "applied"), Kept: strList(raw, "kept"),
					Archived: strList(raw, "archived"), Rejected: strList(raw, "rejected"), Idle: idle,
				}
			},
			Summary: func(in projectIn, out pilotReportOut) string {
				if out.Idle != "" {
					return "Autopilot step for " + in.Project + " did nothing: " + out.Idle + "."
				}
				return fmt.Sprintf("Autopilot step for %s: %d applied, %d kept, %d archived, %d rejected.",
					in.Project, len(out.Applied), len(out.Kept), len(out.Archived), len(out.Rejected))
			},
		}),
	}
}
