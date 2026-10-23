package control

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// Tools of the experience layer, the "Failures" area (LEARN-TASKS.md LN-38):
// recurring errors on their way to a one-line rule in the project's
// CLAUDE.md. Only accept_failure_rule writes into the project.

type failureRulesIn struct {
	projectPageIn
	Status string `json:"status,omitempty" jsonschema:"Only rules in this status: draft, accepted or dismissed; omit for all"`
}

type draftFailureRulesIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	Limit   int    `json:"limit,omitempty" jsonschema:"How many recurring errors to draft a rule for, the most frequent first; omit or 0 for 3"`
}

type failureRuleIDIn struct {
	RuleID string `json:"rule_id" jsonschema:"Rule id, the id of an item of get_failure_rules or draft_failure_rules, e.g. 4"`
}

type acceptFailureRuleIn struct {
	RuleID string `json:"rule_id" jsonschema:"Rule id of a draft, the id of an item of get_failure_rules, e.g. 4"`
	Text   string `json:"text" jsonschema:"The final rule line to append to CLAUDE.md; start from the rule_text of the draft and edit it if needed"`
}

type failureRulesOut struct {
	Rules   []any `json:"rules" jsonschema:"Rules: id, error_key, rule_text, status, distinct_runs, cost_usd, created_at, decided_at, examples (failed_arg, err_text, fixed_arg) and, for an accepted rule, effect (runs_before, hits_before, runs_after, hits_after)"`
	Pending int   `json:"pending" jsonschema:"Recurring errors that have no rule yet; draft_failure_rules drafts them"`
	pageOut
}

type draftedRulesOut struct {
	Rules []any `json:"rules" jsonschema:"The rows created: id, error_key, rule_text, status (draft, or dismissed when CLAUDE.md already says it), distinct_runs and cost_usd"`
}

type failureRuleAck struct {
	OK     bool   `json:"ok" jsonschema:"True when the action was done"`
	RuleID string `json:"rule_id" jsonschema:"The rule acted on"`
}

func needRuleID(id string) error {
	if _, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64); err != nil {
		return fmt.Errorf("rule_id %q is not a number: take the id of an item of get_failure_rules, e.g. 4", id)
	}
	return nil
}

// failureRuleItem keeps the fields of a rule an agent needs.
func failureRuleItem(raw any) any {
	m, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	out := map[string]any{
		"id": m["ID"], "error_key": m["ErrorKey"], "rule_text": m["RuleText"], "status": m["Status"],
		"distinct_runs": m["DistinctRuns"], "cost_usd": m["CostUSD"], "created_at": m["CreatedAt"],
		"decided_at": m["DecidedAt"],
	}
	if ex, ok := m["Examples"]; ok {
		items := []any{}
		for _, e := range list(ex) {
			if em, ok := e.(map[string]any); ok {
				items = append(items, map[string]any{
					"failed_arg": em["failed_arg"], "err_text": em["err_text"], "fixed_arg": em["fixed_arg"],
				})
			}
		}
		out["examples"] = items
	}
	if ef, ok := m["Effect"].(map[string]any); ok && m["Status"] == "accepted" {
		out["effect"] = map[string]any{
			"runs_before": ef["RunsBefore"], "hits_before": ef["HitsBefore"],
			"runs_after": ef["RunsAfter"], "hits_after": ef["HitsAfter"],
		}
	}
	return out
}

func failuresTools() []Tool {
	ro := ToolAnnotations{ReadOnly: true}

	return []Tool{
		define(spec[failureRulesIn, failureRulesOut]{
			Name: "get_failure_rules", Title: "Get failure rules",
			Doc: Doc{
				What:    "List the CLAUDE.md rules drafted from a project's recurring errors, with the examples and, for accepted ones, how often the error came back.",
				UseWhen: "you review what the failure layer proposed, or look up a rule id.",
				NotFor:  "producing new drafts (use draft_failure_rules).",
				Returns: "rules with id, error_key, rule_text, status and dates, plus pending (errors without a rule yet), with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{"project":"lumen","status":"draft"}`,
			},
			Input: func(props map[string]*jsonschema.Schema) {
				tunePage(props)
				props["status"].Enum = []any{"draft", "accepted", "dismissed"}
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetFailureRules"},
			Translate: func(in failureRulesIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if _, _, err := in.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetFailureRules", map[string]string{"project": in.Project}), nil
			},
			Result: func(in failureRulesIn, raw any) failureRulesOut {
				m, _ := raw.(map[string]any)
				var items []any
				for _, r := range list(m["Rules"]) {
					if rm, ok := r.(map[string]any); ok && in.Status != "" && rm["Status"] != in.Status {
						continue
					}
					items = append(items, failureRuleItem(r))
				}
				pending, _ := m["Pending"].(float64)
				page, p := listPage(in.pageIn, items)
				return failureRulesOut{Rules: page, Pending: int(pending), pageOut: p}
			},
			Summary: func(in failureRulesIn, out failureRulesOut) string {
				return plural(len(out.Rules), "failure rule", "failure rules") + " in " + in.Project +
					fmt.Sprintf(", %d recurring errors without a rule.", out.Pending) + moreHint(out.pageOut)
			},
		}),
		define(spec[draftFailureRulesIn, draftedRulesOut]{
			Name: "draft_failure_rules", Title: "Draft failure rules",
			Doc: Doc{
				What:    "Ask a model to write a one-line CLAUDE.md rule for each of the project's most frequent recurring errors that has none yet.",
				UseWhen: "get_failure_rules shows pending errors and you want drafts to review.",
				NotFor:  "writing the rule into CLAUDE.md (use accept_failure_rule).",
				Returns: "the rows created; a rule CLAUDE.md already states comes back as dismissed.",
				Effects: "runs one paid model call per error (tokens are spent); CLAUDE.md is not changed until accept_failure_rule.",
				Example: `{"project":"lumen","limit":3}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"DraftFailureRules"},
			Translate: func(in draftFailureRulesIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if in.Limit < 0 {
					return "", nil, fmt.Errorf("limit must not be negative; omit it for the default of 3")
				}
				return "/rpc", rpcCallBody("DraftFailureRules", map[string]any{"project": in.Project, "limit": in.Limit}), nil
			},
			Result: func(_ draftFailureRulesIn, raw any) draftedRulesOut {
				items := []any{}
				for _, r := range list(raw) {
					items = append(items, failureRuleItem(r))
				}
				return draftedRulesOut{Rules: items}
			},
			Summary: func(in draftFailureRulesIn, out draftedRulesOut) string {
				return plural(len(out.Rules), "rule", "rules") + " drafted for " + in.Project + "; review them, then accept_failure_rule."
			},
		}),
		define(spec[acceptFailureRuleIn, failureRuleAck]{
			Name: "accept_failure_rule", Title: "Accept failure rule",
			Doc: Doc{
				What:    "Append a drafted rule to the \"Learned from failures\" section of the project's CLAUDE.md and mark it accepted.",
				UseWhen: "a draft from get_failure_rules is right (edit the text first if it is not) and you are authorised to change CLAUDE.md.",
				NotFor:  "turning a draft down (use dismiss_failure_rule).",
				Returns: "ok and the rule_id.",
				Effects: "writes one line into the project's CLAUDE.md; only a draft can be accepted.",
				Example: `{"rule_id":"4","text":"Run go vet before go test."}`,
			},
			Level: LevelAdmin, Covers: []string{"AcceptFailureRule"},
			Translate: func(in acceptFailureRuleIn) (string, any, error) {
				if err := needRuleID(in.RuleID); err != nil {
					return "", nil, err
				}
				if strings.TrimSpace(in.Text) == "" {
					return "", nil, fmt.Errorf("text is required: pass the rule line, e.g. the rule_text of the draft")
				}
				return "/rpc", rpcCallBody("AcceptFailureRule", map[string]any{"rule_id": in.RuleID, "text": in.Text}), nil
			},
			Result: func(in acceptFailureRuleIn, _ any) failureRuleAck { return failureRuleAck{OK: true, RuleID: in.RuleID} },
			Summary: func(in acceptFailureRuleIn, _ failureRuleAck) string {
				return "Rule " + in.RuleID + " added to CLAUDE.md."
			},
		}),
		define(spec[failureRuleIDIn, failureRuleAck]{
			Name: "dismiss_failure_rule", Title: "Dismiss failure rule",
			Doc: Doc{
				What:    "Turn a drafted rule down so the error is not proposed again.",
				UseWhen: "a draft from get_failure_rules is wrong or not worth a line in CLAUDE.md.",
				NotFor:  "accepting it (use accept_failure_rule).",
				Returns: "ok and the rule_id.",
				Effects: "marks the rule dismissed; nothing is written into the project; only a draft can be dismissed.",
				Example: `{"rule_id":"4"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"DismissFailureRule"},
			Translate: func(in failureRuleIDIn) (string, any, error) {
				if err := needRuleID(in.RuleID); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("DismissFailureRule", map[string]string{"rule_id": in.RuleID}), nil
			},
			Result:  func(in failureRuleIDIn, _ any) failureRuleAck { return failureRuleAck{OK: true, RuleID: in.RuleID} },
			Summary: func(in failureRuleIDIn, _ failureRuleAck) string { return "Rule " + in.RuleID + " dismissed." },
		}),
	}
}
