package control

import (
	"encoding/json"
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// Tools of the interaction area (MCP-07): what waits for a human — questions,
// permissions, plans. approve_permission and deny_permission live with the
// core tools.

type pendingIn struct {
	sessionRef
	pageIn
}

// filter resolves the optional session filter: empty when none is given.
func (p pendingIn) filter() (string, error) {
	if p.SessionID == "" && p.Project == "" && p.Session == "" {
		return "", nil
	}
	return p.id()
}

// pendingAllIn is get_pending's input: only the optional session filter.
type pendingAllIn struct{ sessionRef }

func (p pendingAllIn) filter() (string, error) { return pendingIn{sessionRef: p.sessionRef}.filter() }

type answerIn struct {
	sessionRef
	QuestionID string `json:"question_id" jsonschema:"Question ID, taken from get_pending_questions or get_pending"`
	Answer     string `json:"answer" jsonschema:"The answer: one of the question's options, or free text, e.g. Use the second approach"`
}

type ruleIn struct {
	sessionRef
	Tool     string `json:"tool" jsonschema:"Tool the rule applies to, e.g. Bash, Edit or Write"`
	Pattern  string `json:"pattern,omitempty" jsonschema:"Pattern matched against the tool input, e.g. git status*; empty matches every call of the tool"`
	Decision string `json:"decision" jsonschema:"allow or deny"`
}

type candidatesIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_sessions, e.g. lumen"`
	Days    int64  `json:"days,omitempty" jsonschema:"How many past days of permission history to analyse (default 30)"`
}

type approvePlanIn struct {
	Plan map[string]any `json:"plan" jsonschema:"The plan object from run_preflight or get_plan, optionally edited (steps, subtasks); it keeps its plan_id"`
}

type questionsOut struct {
	Questions []any `json:"questions" jsonschema:"Questions waiting for an answer, each with session_id and question (id, question, options, asked_at)"`
	pageOut
}

type pendingOut struct {
	Permissions []any `json:"permissions" jsonschema:"Permission requests waiting for approve_permission or deny_permission"`
	Questions   []any `json:"questions" jsonschema:"Questions waiting for answer_question"`
	Total       int   `json:"total" jsonschema:"Number of permission requests and questions together"`
}

type answerAck struct {
	OK         bool   `json:"ok" jsonschema:"True when the answer was delivered"`
	SessionID  string `json:"session_id" jsonschema:"Session ID in project/session form"`
	QuestionID string `json:"question_id" jsonschema:"The question that was answered"`
}

type ruleAck struct {
	OK        bool   `json:"ok" jsonschema:"True when the rule is in the session config (also when it already was)"`
	SessionID string `json:"session_id" jsonschema:"Session ID in project/session form"`
}

type candidatesOut struct {
	Safe        []any `json:"safe" jsonschema:"Rules that keep being asked for, were always approved and are read-only: candidates for add_permission_rule"`
	NeedsReview []any `json:"needs_review" jsonschema:"Frequent requests that are not provably read-only; a human decides"`
}

type planDetailOut struct {
	Plan any `json:"plan" jsonschema:"The plan with its subtasks and their progress; null when no plan has this ID"`
}

func interactionTools() []Tool {
	ro := ToolAnnotations{ReadOnly: true}
	return []Tool{
		define(spec[pendingAllIn, pendingOut]{
			Name: "get_pending", Title: "List everything waiting for a human",
			Doc: Doc{
				What:    "List every permission request and question that waits for a human, across all sessions or for one.",
				UseWhen: "you want one answer to what is blocking the sessions, instead of calling get_pending_permissions and get_pending_questions.",
				NotFor:  "answering them (use approve_permission, deny_permission or answer_question).",
				Returns: "permissions, questions and their total; plans have no waiting queue, read one with get_plan.",
				Effects: "none, read only.",
				Example: `{"session_id":"lumen/P1"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetPendingPermissions", "GetPendingQuestions"},
			Translate: func(in pendingAllIn) (string, any, error) {
				id, err := in.filter()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetPending", map[string]string{"id": id}), nil
			},
			Result: func(_ pendingAllIn, raw any) pendingOut {
				m, _ := raw.(map[string]any)
				out := pendingOut{Permissions: list(m["permissions"]), Questions: list(m["questions"])}
				out.Total = len(out.Permissions) + len(out.Questions)
				return out
			},
			Summary: func(_ pendingAllIn, out pendingOut) string {
				return fmt.Sprintf("%s and %s waiting.", plural(len(out.Permissions), "permission", "permissions"),
					plural(len(out.Questions), "question", "questions"))
			},
		}),
		define(spec[pendingIn, questionsOut]{
			Name: "get_pending_questions", Title: "List pending questions",
			Doc: Doc{
				What:    "List the questions sessions asked the human (AskUserQuestion, clarify, the manager's own restart choice) that still wait for an answer.",
				UseWhen: "a session is idle after asking something, or before answer_question.",
				NotFor:  "permission requests (use get_pending_permissions).",
				Returns: "questions, with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{"session_id":"lumen/P1"}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetPendingQuestions"},
			Translate: func(in pendingIn) (string, any, error) {
				if _, _, err := in.window(); err != nil {
					return "", nil, err
				}
				id, err := in.filter()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetPendingQuestions", map[string]string{"id": id}), nil
			},
			Result: func(in pendingIn, raw any) questionsOut {
				page, p := listPage(in.pageIn, raw)
				return questionsOut{Questions: page, pageOut: p}
			},
			Summary: func(_ pendingIn, out questionsOut) string {
				return plural(len(out.Questions), "pending question", "pending questions") + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[answerIn, answerAck]{
			Name: "answer_question", Title: "Answer question",
			Doc: Doc{
				What:    "Answer a question a session asked the human.",
				UseWhen: "get_pending_questions lists a question you can answer.",
				NotFor:  "permission requests (use approve_permission or deny_permission).",
				Returns: "ok, the session and the question id.",
				Effects: "the answer is sent to the session as the user's reply and it continues; a manager question (restart choice) is acted on instead.",
				Example: `{"session_id":"lumen/P1","question_id":"q-1","answer":"Use the second approach"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"AnswerQuestion"},
			Translate: func(in answerIn) (string, any, error) {
				id, err := in.id()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("AnswerQuestion", map[string]string{
					"id": id, "question_id": in.QuestionID, "answer": in.Answer,
				}), nil
			},
			Result: func(in answerIn, _ any) answerAck {
				id, _ := in.id()
				return answerAck{OK: true, SessionID: id, QuestionID: in.QuestionID}
			},
			Summary: func(_ answerIn, out answerAck) string {
				return fmt.Sprintf("Question %s answered for %s.", out.QuestionID, out.SessionID)
			},
		}),
		define(spec[ruleIn, ruleAck]{
			Name: "add_permission_rule", Title: "Add permission rule",
			Doc: Doc{
				What:    "Add a standing allow or deny rule to one session's permission rules.",
				UseWhen: "get_permission_candidates shows a request that should never ask again, or one that must always be refused.",
				NotFor:  "deciding a single pending request (use approve_permission or deny_permission).",
				Returns: "ok and the session; a rule that already exists is not duplicated.",
				Effects: "rewrites the saved configuration; the rule applies to the session's later requests and persists across restarts.",
				Example: `{"session_id":"lumen/P1","tool":"Bash","pattern":"git status*","decision":"allow"}`,
			},
			Input: func(props map[string]*jsonschema.Schema) { props["decision"].Enum = []any{"allow", "deny"} },
			Level: LevelAdmin, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"AddPermissionRule"},
			Translate: func(in ruleIn) (string, any, error) {
				project, name, err := in.parts()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("AddPermissionRule", map[string]string{
					"project": project, "session": name, "tool": in.Tool, "pattern": in.Pattern, "decision": in.Decision,
				}), nil
			},
			Result: func(in ruleIn, _ any) ruleAck {
				id, _ := in.id()
				return ruleAck{OK: true, SessionID: id}
			},
			Summary: func(in ruleIn, out ruleAck) string {
				return fmt.Sprintf("Rule %s %s added to %s.", in.Decision, in.Tool, out.SessionID)
			},
		}),
		define(spec[candidatesIn, candidatesOut]{
			Name: "get_permission_candidates", Title: "Suggest permission rules",
			Doc: Doc{
				What:    "Suggest auto-allow rules from a project's permission history: requests that keep recurring and were always approved.",
				UseWhen: "sessions keep stopping on the same permission prompts.",
				NotFor:  "listing what waits now (use get_pending_permissions).",
				Returns: "safe (read-only, ready for add_permission_rule) and needs_review; both are empty unless experience tracking was on.",
				Effects: "none, read only.",
				Example: `{"project":"lumen","days":30}`,
			},
			Input: func(props map[string]*jsonschema.Schema) {
				d := props["days"]
				d.Minimum, d.Default = fptr(1), json.RawMessage("30")
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetPermissionCandidates"},
			Translate: func(in candidatesIn) (string, any, error) {
				return "/rpc", rpcCallBody("GetPermissionCandidates", map[string]any{
					"project": in.Project, "days": orDefault(in.Days, 30),
				}), nil
			},
			Result: func(_ candidatesIn, raw any) candidatesOut {
				m, _ := raw.(map[string]any)
				return candidatesOut{Safe: list(m["safe"]), NeedsReview: list(m["needs_review"])}
			},
			Summary: func(_ candidatesIn, out candidatesOut) string {
				return fmt.Sprintf("%d safe, %d needs review.", len(out.Safe), len(out.NeedsReview))
			},
		}),
		define(spec[planIn, planDetailOut]{
			Name: "get_plan", Title: "Get plan",
			Doc: Doc{
				What:    "Get a persisted plan with its subtasks and their progress.",
				UseWhen: "you follow a plan started with execute_plan, or review one before approve_plan.",
				NotFor:  "creating a plan (use run_preflight).",
				Returns: "the plan, or null when no plan has this id.",
				Effects: "none, read only.",
				Example: `{"plan_id":"7"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetPlan"},
			Translate: func(in planIn) (string, any, error) {
				return "/rpc", rpcCallBody("GetPlan", map[string]string{"plan_id": in.PlanID}), nil
			},
			Result: func(_ planIn, raw any) planDetailOut { return planDetailOut{Plan: raw} },
			Summary: func(in planIn, out planDetailOut) string {
				if out.Plan == nil {
					return fmt.Sprintf("No plan %s.", in.PlanID)
				}
				return fmt.Sprintf("Plan %s.", in.PlanID)
			},
		}),
		define(spec[approvePlanIn, planDetailOut]{
			Name: "approve_plan", Title: "Approve plan",
			Doc: Doc{
				What:    "Persist a plan, optionally edited, as approved on the human's behalf.",
				UseWhen: "the plan from run_preflight looks right and execute_plan should follow.",
				NotFor:  "starting the work (use execute_plan afterwards).",
				Returns: "the plan as stored, with its plan_id.",
				Effects: "stores the plan as approved; nothing runs until execute_plan.",
				Example: `{"plan":{"id":7,"steps":[]}}`,
			},
			Level: LevelAdmin, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"ApprovePlan"},
			Translate: func(in approvePlanIn) (string, any, error) {
				if len(in.Plan) == 0 {
					return "", nil, fmt.Errorf("plan is required: pass the plan object returned by run_preflight or get_plan")
				}
				return "/rpc", rpcCallBody("ApprovePlan", map[string]any{"plan": in.Plan}), nil
			},
			Result:  func(_ approvePlanIn, raw any) planDetailOut { return planDetailOut{Plan: raw} },
			Summary: func(approvePlanIn, planDetailOut) string { return "Plan approved." },
		}),
	}
}
