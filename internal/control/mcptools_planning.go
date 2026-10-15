package control

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
)

// Tools of the preflight, roadmap and mixed-programming area (MCP-10). The
// operations that run a model (session_preflight, generate_roadmap) answer when
// the model is done; cm-mcp sends progress notifications meanwhile (MCP-12).

type sessionPreflightIn struct{ sessionRef }

type generateRoadmapIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	Idea    string `json:"idea" jsonschema:"What the project is to become, in free text; the roadmap splits it into phases and tasks, e.g. A CLI that syncs notes to S3"`
	Model   string `json:"model,omitempty" jsonschema:"Model that writes the roadmap, e.g. opus; default the analyst model (opus)"`
}

type approveRoadmapIn struct {
	PlanID    string `json:"plan_id" jsonschema:"Plan ID of a draft roadmap from generate_roadmap or get_latest_draft_roadmap, e.g. 9"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"True to replace ROADMAP.md and STATUS-P1.md when the project already has them; otherwise the call fails when they exist"`
}

type roadmapIn struct {
	sessionRef
	pageIn
}

type taskDetailIn struct {
	Project string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	Path    string `json:"path" jsonschema:"Link of the task file as in the roadmap row (detail_path of get_session_roadmap), relative to the project folder, e.g. tasks/01-setup.md"`
	Cursor  string `json:"cursor,omitempty" jsonschema:"Byte position to continue from: the next_cursor of the previous response; omit for the start"`
}

type rowDetailIn struct {
	Project     string `json:"project" jsonschema:"Project name as in get_projects, e.g. lumen"`
	RoadmapFile string `json:"roadmap_file" jsonschema:"Roadmap file of the row, the roadmap_file of a get_session_roadmap node, e.g. ROADMAP.md"`
	Line        int64  `json:"line" jsonschema:"1-based line of the row in that file, the line of a get_session_roadmap node, e.g. 42"`
	Cursor      string `json:"cursor,omitempty" jsonschema:"Byte position to continue from: the next_cursor of the previous response; omit for the start"`
}

type cancelMixedIn struct {
	TaskID string `json:"task_id" jsonschema:"Task ID in project/brief/worker form, as returned by dispatch_mixed_task, e.g. lumen/parse-header/step-flash"`
}

type sessionPreflightOut struct {
	Needed bool `json:"needed" jsonschema:"True when the session preflight settings called for a plan; false means the session may simply start"`
	Plan   any  `json:"plan" jsonschema:"The plan to review, including its plan_id; null when needed is false"`
}

type draftRoadmapOut struct {
	Found bool `json:"found" jsonschema:"True when an unapproved draft roadmap exists for the project"`
	Plan  any  `json:"plan" jsonschema:"The draft roadmap plan, including its plan_id; null when none"`
}

type roadmapOut struct {
	Found       bool   `json:"found" jsonschema:"False when the session has no task source or the project has no readable roadmap; the other fields are then empty"`
	Title       string `json:"title" jsonschema:"Roadmap title"`
	RoadmapFile string `json:"roadmap_file" jsonschema:"Roadmap file, relative to the project folder"`
	StatusFile  string `json:"status_file" jsonschema:"Queue file whose pointers decide done, current and pending"`
	Done        int64  `json:"done" jsonschema:"Tasks finished"`
	Total       int64  `json:"total" jsonschema:"Tasks in all"`
	Nodes       []any  `json:"nodes" jsonschema:"Phases and tasks in reading order, each with kind, id, name, summary, status (done, active, blocked or pending), current, depth, detail_path, line and roadmap_file; the children of a node are the following nodes with a greater depth"`
	pageOut
}

type textOut struct {
	Text string `json:"text" jsonschema:"The text, from the cursor position on"`
	pageOut
}

type qualityOut struct {
	Workers []any `json:"workers" jsonschema:"Per worker model: tasks total, done and needs_human, avg_rounds_to_green, clean_patch_rate, parse_errors and gate_failures"`
	pageOut
}

type cancelMixedAck struct {
	OK     bool   `json:"ok" jsonschema:"True when the task was cancelled"`
	TaskID string `json:"task_id" jsonschema:"The task that was cancelled"`
}

// flattenNodes turns the roadmap tree into a reading-order list: each node
// loses its children and gains the depth it had.
func flattenNodes(nodes []any, depth int, out []any) []any {
	for _, n := range nodes {
		m, ok := n.(map[string]any)
		if !ok {
			continue
		}
		flat := make(map[string]any, len(m)+1)
		for k, v := range m {
			if k != "children" {
				flat[k] = v
			}
		}
		flat["depth"] = depth
		out = append(out, flat)
		kids, _ := m["children"].([]any)
		out = flattenNodes(kids, depth+1, out)
	}
	return out
}

// textWindow cuts text to MaxResponseBytes from the byte position in cursor,
// never inside a character, and reports where the next window starts.
func textWindow(text, cursor string) (string, pageOut, error) {
	from := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil || n < 0 {
			return "", pageOut{}, fmt.Errorf("cursor %q is not valid: pass the next_cursor value from the previous response unchanged (a non-negative integer), or omit it for the start", cursor)
		}
		from = n
	}
	if from > len(text) {
		from = len(text)
	}
	for from < len(text) && !utf8.RuneStart(text[from]) {
		from++
	}
	end := from + MaxResponseBytes
	if end >= len(text) {
		return text[from:], pageOut{}, nil
	}
	for end > from && !utf8.RuneStart(text[end]) {
		end--
	}
	return text[from:end], pageOut{NextCursor: strconv.Itoa(end), Truncated: true}, nil
}

func textResult(raw any, cursor string) textOut {
	s, _ := raw.(string)
	text, p, _ := textWindow(s, cursor)
	return textOut{Text: text, pageOut: p}
}

func textSummary(what string, out textOut) string {
	return fmt.Sprintf("%s: %d characters.", what, utf8.RuneCountInString(out.Text)) + moreHint(out.pageOut)
}

func planningTools() []Tool {
	ro := ToolAnnotations{ReadOnly: true}

	return []Tool{
		define(spec[sessionPreflightIn, sessionPreflightOut]{
			Name: "session_preflight", Title: "Run a session preflight",
			Doc: Doc{
				What:    "Run the preflight analysis the settings of a session call for (what the Start button does) and return the plan to review, or nothing when the session should simply start.",
				UseWhen: "you start a session whose project has preflight on and want the same gate as the GUI.",
				NotFor:  "analysing an ad-hoc task (use run_preflight) or starting the session (use start_session).",
				Returns: "needed and, when true, the plan including its plan_id.",
				Effects: "may run an analysis session that spends tokens; the project is not changed.",
				Example: `{"session_id":"lumen/P1"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"SessionPreflight"},
			Translate: func(in sessionPreflightIn) (string, any, error) {
				p, s, err := in.parts()
				if err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("SessionPreflight", map[string]string{"project": p, "session": s}), nil
			},
			Result: func(_ sessionPreflightIn, raw any) sessionPreflightOut {
				return sessionPreflightOut{Needed: raw != nil, Plan: raw}
			},
			Summary: func(_ sessionPreflightIn, out sessionPreflightOut) string {
				if !out.Needed {
					return "No preflight needed: the session can start."
				}
				return "Plan generated for review."
			},
		}),
		define(spec[generateRoadmapIn, planOut]{
			Name: "generate_roadmap", Title: "Generate roadmap",
			Doc: Doc{
				What:    "Split a whole project idea into a backlog of phases and tasks and save it as a draft roadmap plan.",
				UseWhen: "a project starts and needs a task queue.",
				NotFor:  "one task (use run_preflight) or writing the files (use approve_roadmap).",
				Returns: "the draft plan, including its plan_id for approve_roadmap.",
				Effects: "runs an analysis session that spends tokens; nothing is written to the project until approve_roadmap.",
				Example: `{"project":"lumen","idea":"A CLI that syncs notes to S3"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{OpenWorld: true}, Covers: []string{"GenerateRoadmap"},
			Translate: func(in generateRoadmapIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if strings.TrimSpace(in.Idea) == "" {
					return "", nil, fmt.Errorf(`idea is required: describe what the project is to become, e.g. "A CLI that syncs notes to S3"`)
				}
				return "/rpc", rpcCallBody("GenerateRoadmap", map[string]string{"project": in.Project, "idea": in.Idea, "model": in.Model}), nil
			},
			Result:  func(_ generateRoadmapIn, raw any) planOut { return planOut{Plan: raw} },
			Summary: func(in generateRoadmapIn, _ planOut) string { return "Draft roadmap generated for " + in.Project + "." },
		}),
		define(spec[projectIn, draftRoadmapOut]{
			Name: "get_latest_draft_roadmap", Title: "Get latest draft roadmap",
			Doc: Doc{
				What:    "Return the newest roadmap of a project that was generated but not approved yet.",
				UseWhen: "the answer of generate_roadmap was lost, or you review a draft before approve_roadmap.",
				NotFor:  "the approved roadmap in use (use get_session_roadmap).",
				Returns: "found and the draft plan, including its plan_id; found is false when there is none.",
				Effects: "none, read only.",
				Example: `{"project":"lumen"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetLatestDraftRoadmap"},
			Translate: func(in projectIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetLatestDraftRoadmap", map[string]string{"project": in.Project}), nil
			},
			Result: func(_ projectIn, raw any) draftRoadmapOut { return draftRoadmapOut{Found: raw != nil, Plan: raw} },
			Summary: func(in projectIn, out draftRoadmapOut) string {
				if !out.Found {
					return "No draft roadmap for " + in.Project + "."
				}
				return "Draft roadmap of " + in.Project + "."
			},
		}),
		define(spec[approveRoadmapIn, planOut]{
			Name: "approve_roadmap", Title: "Approve roadmap",
			Doc: Doc{
				What:    "Approve a draft roadmap: write ROADMAP.md and STATUS-P1.md into the project and create or update its P1 session.",
				UseWhen: "get_latest_draft_roadmap or generate_roadmap gave a roadmap you are authorised to make real.",
				NotFor:  "an ad-hoc plan (use approve_plan) or reading a roadmap (use get_session_roadmap).",
				Returns: "the approved plan.",
				Effects: "writes files into the project folder and edits the config; with overwrite=true it replaces existing roadmap files.",
				Example: `{"plan_id":"9"}`,
			},
			Level: LevelAdmin, Annotations: ToolAnnotations{Idempotent: true}, Covers: []string{"ApproveRoadmap"},
			Translate: func(in approveRoadmapIn) (string, any, error) {
				if strings.TrimSpace(in.PlanID) == "" {
					return "", nil, fmt.Errorf("plan_id is required: pass the plan_id of generate_roadmap or get_latest_draft_roadmap, e.g. 9")
				}
				return "/rpc", rpcCallBody("ApproveRoadmap", map[string]any{"plan_id": in.PlanID, "overwrite": in.Overwrite}), nil
			},
			Result:  func(_ approveRoadmapIn, raw any) planOut { return planOut{Plan: raw} },
			Summary: func(in approveRoadmapIn, _ planOut) string { return "Roadmap " + in.PlanID + " approved and written." },
		}),
		define(spec[roadmapIn, roadmapOut]{
			Name: "get_session_roadmap", Title: "Get session roadmap",
			Doc: Doc{
				What:    "Show the roadmap of a session as its task panel does: every phase and task with done, current or pending status.",
				UseWhen: "you want to know what is done and what comes next in the queue of a session.",
				NotFor:  "the text of one task (use get_roadmap_task_detail or get_roadmap_row_detail).",
				Returns: "title, files, done and total counts and the nodes in reading order, with next_cursor and truncated when more exist; found is false without a roadmap.",
				Effects: "none, read only.",
				Example: `{"session_id":"lumen/P1","limit":50}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetSessionRoadmap"},
			Translate: func(in roadmapIn) (string, any, error) {
				p, s, err := in.parts()
				if err != nil {
					return "", nil, err
				}
				if _, _, err := in.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetSessionRoadmap", map[string]string{"project": p, "session": s}), nil
			},
			Result: func(in roadmapIn, raw any) roadmapOut {
				m, ok := raw.(map[string]any)
				if !ok {
					return roadmapOut{Nodes: []any{}}
				}
				str := func(k string) string { v, _ := m[k].(string); return v }
				num := func(k string) int64 { f, _ := m[k].(float64); return int64(f) }
				nodes, _ := m["nodes"].([]any)
				page, p := listPage(in.pageIn, flattenNodes(nodes, 0, nil))
				return roadmapOut{
					Found: true, Title: str("title"), RoadmapFile: str("roadmap_file"), StatusFile: str("status_file"),
					Done: num("done"), Total: num("total"), Nodes: page, pageOut: p,
				}
			},
			Summary: func(_ roadmapIn, out roadmapOut) string {
				if !out.Found {
					return "No roadmap for this session."
				}
				return fmt.Sprintf("Roadmap %q: %d of %d tasks done.", out.Title, out.Done, out.Total) + moreHint(out.pageOut)
			},
		}),
		define(spec[taskDetailIn, textOut]{
			Name: "get_roadmap_task_detail", Title: "Get roadmap task file",
			Doc: Doc{
				What:    "Read the task file (tasks/NN-*.md) a roadmap row links to.",
				UseWhen: "get_session_roadmap shows a node with a detail_path and you need its full spec.",
				NotFor:  "rows that keep their text inline (use get_roadmap_row_detail) or files outside the project.",
				Returns: "the text, with next_cursor and truncated when the file is longer than one response.",
				Effects: "none, read only.",
				Example: `{"project":"lumen","path":"tasks/01-setup.md"}`,
			},
			Level: LevelRead, Annotations: ro, Covers: []string{"GetRoadmapTaskDetail"},
			Translate: func(in taskDetailIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if in.Path == "" {
					return "", nil, fmt.Errorf("path is required: take the detail_path of a get_session_roadmap node, e.g. tasks/01-setup.md")
				}
				if _, _, err := textWindow("", in.Cursor); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetRoadmapTaskDetail", map[string]string{"project": in.Project, "rel_path": in.Path}), nil
			},
			Result:  func(in taskDetailIn, raw any) textOut { return textResult(raw, in.Cursor) },
			Summary: func(in taskDetailIn, out textOut) string { return textSummary(in.Path, out) },
		}),
		define(spec[rowDetailIn, textOut]{
			Name: "get_roadmap_row_detail", Title: "Get roadmap row text",
			Doc: Doc{
				What:    "Read the long text of one roadmap row: its note column, else the raw row.",
				UseWhen: "a roadmap keeps task descriptions inline and you need the full text of one task.",
				NotFor:  "rows with a task file (use get_roadmap_task_detail).",
				Returns: "the text, with next_cursor and truncated when it is longer than one response.",
				Effects: "none, read only.",
				Example: `{"project":"lumen","roadmap_file":"ROADMAP.md","line":42}`,
			},
			Input: func(props map[string]*jsonschema.Schema) { props["line"].Minimum = fptr(1) },
			Level: LevelRead, Annotations: ro, Covers: []string{"GetRoadmapRowDetail"},
			Translate: func(in rowDetailIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if in.RoadmapFile == "" || in.Line < 1 {
					return "", nil, fmt.Errorf("roadmap_file and line (1 or more) are required: take them from a get_session_roadmap node, e.g. ROADMAP.md and 42")
				}
				if _, _, err := textWindow("", in.Cursor); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetRoadmapRowDetail", map[string]any{
					"project": in.Project, "roadmap_file": in.RoadmapFile, "line": in.Line,
				}), nil
			},
			Result: func(in rowDetailIn, raw any) textOut { return textResult(raw, in.Cursor) },
			Summary: func(in rowDetailIn, out textOut) string {
				return textSummary(fmt.Sprintf("%s line %d", in.RoadmapFile, in.Line), out)
			},
		}),
		define(spec[projectPageIn, qualityOut]{
			Name: "get_mixed_quality", Title: "Compare worker quality",
			Doc: Doc{
				What:    "Compare the worker models of the mixed-programming tasks of a project: tasks done, rounds to green, clean patch rate and failures per worker.",
				UseWhen: "you choose which worker model gets the next brief.",
				NotFor:  "the rounds of one task (use get_mixed_rounds).",
				Returns: "workers, with next_cursor and truncated when more exist.",
				Effects: "none, read only.",
				Example: `{"project":"lumen"}`,
			},
			Input: tunePage,
			Level: LevelRead, Annotations: ro, Covers: []string{"GetMixedQuality"},
			Translate: func(in projectPageIn) (string, any, error) {
				if err := needProject(in.Project); err != nil {
					return "", nil, err
				}
				if _, _, err := in.window(); err != nil {
					return "", nil, err
				}
				return "/rpc", rpcCallBody("GetMixedQuality", map[string]string{"project": in.Project}), nil
			},
			Result: func(in projectPageIn, raw any) qualityOut {
				page, p := listPage(in.pageIn, raw)
				return qualityOut{Workers: page, pageOut: p}
			},
			Summary: func(in projectPageIn, out qualityOut) string {
				return plural(len(out.Workers), "worker", "workers") + " in " + in.Project + "." + moreHint(out.pageOut)
			},
		}),
		define(spec[cancelMixedIn, cancelMixedAck]{
			Name: "cancel_mixed_task", Title: "Cancel mixed task",
			Doc: Doc{
				What:    "Cancel a running mixed-programming task.",
				UseWhen: "a dispatched task goes wrong or is no longer wanted.",
				NotFor:  "stopping a normal session (use stop_session).",
				Returns: "ok and the task_id.",
				Effects: "stops the worker loop; patches already applied stay in the project.",
				Example: `{"task_id":"lumen/parse-header/step-flash"}`,
			},
			Level: LevelOperate, Annotations: ToolAnnotations{Destructive: true, Idempotent: true}, Covers: []string{"CancelMixedTask"},
			Translate: func(in cancelMixedIn) (string, any, error) {
				if strings.Count(in.TaskID, "/") < 2 {
					return "", nil, fmt.Errorf(`task_id %q must look like "project/brief/worker", e.g. "lumen/parse-header/step-flash"; dispatch_mixed_task and get_mixed_rounds return it`, in.TaskID)
				}
				return "/rpc", rpcCallBody("CancelMixedTask", map[string]string{"id": in.TaskID}), nil
			},
			Result:  func(in cancelMixedIn, _ any) cancelMixedAck { return cancelMixedAck{OK: true, TaskID: in.TaskID} },
			Summary: func(in cancelMixedIn, _ cancelMixedAck) string { return "Task " + in.TaskID + " cancelled." },
		}),
	}
}
