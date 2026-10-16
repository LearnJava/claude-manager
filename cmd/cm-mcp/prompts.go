package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type promptDef struct {
	name, title, description string
	args                     []*mcp.PromptArgument
	// text builds the user message from the trimmed arguments.
	text func(a map[string]string) string
}

func promptArg(name, desc string, required bool) *mcp.PromptArgument {
	return &mcp.PromptArgument{Name: name, Description: desc, Required: required}
}

// orDefault returns v, or def when v is empty.
func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

var promptDefs = []promptDef{
	{
		name: "setup_project", title: "Set up a project and session from scratch",
		description: "Add a project, give it a CLAUDE.md and the session protocol, define a session and check it starts.",
		args: []*mcp.PromptArgument{
			promptArg("path", "Absolute folder of the project", true),
			promptArg("name", "Project name (defaults to the folder name)", false),
		},
		text: func(a map[string]string) string {
			as := ""
			if a["name"] != "" {
				as = " as " + a["name"]
			}
			return fmt.Sprintf(`Set up the Claude Manager project in folder %s%s.
1. check_environment: make sure the Claude CLI and Git are found; report what is missing.
2. add_project with the folder, then get_project_setup to see whether it has a CLAUDE.md and the session protocol.
3. If there is no CLAUDE.md, generate_claude_md; if there is no protocol, install_session_protocol. Show me what they would write before applying anything.
4. add_session for a first session, then get_config (section "project") to confirm the result.
5. start_session, wait_for_status until it is waiting, and report its state. Do not send work to it.
Read the cm://guide resource first if you are unsure about identifiers or access levels.`, a["path"], as)
		},
	},
	{
		name: "run_task", title: "Start a task and see it through",
		description: "Send a task to a session, handle its questions and permissions, and report the outcome.",
		args: []*mcp.PromptArgument{
			promptArg("session_id", `Session as "project/session", e.g. lumen/P1`, true),
			promptArg("task", "What the session should do", true),
		},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`Run this task in session %[1]s and see it through: %[2]s
1. get_session: if it is stopped, start_session; if it is running, wait_for_status for waiting first.
2. send_message with the task.
3. Loop on wait_for_any (the session's status change, a permission, a question), never poll:
   - permission request: show me the tool and its input, then approve_permission or deny_permission only as I say;
   - question: show it to me and answer_question with my answer;
   - waiting: the task step is done.
4. Finish with get_session_logs (a short tail) and get_cost, and summarize what changed and what it cost.`, a["session_id"], a["task"])
		},
	},
	{
		name: "diagnose_session", title: "Work out why a session stopped",
		description: "Find out why a session is stopped, crashed, in error or stuck, and propose the next step.",
		args:        []*mcp.PromptArgument{promptArg("session_id", `Session as "project/session"`, true)},
		text: func(a map[string]string) string {
			return fmt.Sprintf(`Session %s is not doing what I expect. Diagnose it.
1. get_session: status, current task, pending permission or question, saved state.
2. get_session_logs with a small limit, and search_session_log for "error" if the tail does not show the cause.
3. get_history for the previous runs if the log is empty.
4. Explain the cause in two sentences, then offer exactly one next step among: answer a pending item, restart_session, clear_session_state, send_message, or fix the project config (update_session). Do not run a state-changing tool until I agree.`, a["session_id"])
		},
	},
	{
		name: "triage_waiting", title: "Handle everything waiting for a human",
		description: "List all pending permissions and questions across sessions and walk through them.",
		text: func(map[string]string) string {
			return `Go through everything that waits for me.
1. get_pending: all permission requests and questions across all sessions.
2. For each, say which session asked, what it wants, and your recommendation (approve, deny or the answer) with the reason.
3. Wait for my decision on each one, then call approve_permission, deny_permission or answer_question. Offer add_permission_rule only when the same request repeats.
4. Finish with get_pending again to confirm the queue is empty.`
		},
	},
	{
		name: "cost_report", title: "Report spending for a period",
		description: "Cost and token report per project for the last N days.",
		args: []*mcp.PromptArgument{
			promptArg("days", "Period in days (default 7)", false),
			promptArg("project", "Limit to one project (default: all)", false),
		},
		text: func(a map[string]string) string {
			days := orDefault(a["days"], "7")
			scope := "every project from get_projects"
			if a["project"] != "" {
				scope = "project " + a["project"]
			}
			return fmt.Sprintf(`Report Claude Manager spending for the last %[1]s days.
1. get_cost with scope "project" and period %[1]s for %[2]s.
2. get_cost with scope "day" for today.
3. get_rate_limit for the rate-limit status.
Give a small table (project, cost_usd, total tokens), the most expensive project, and one observation on where the cost comes from (get_top_actions or get_token_attribution may help). Do not change any setting.`, days, scope)
		},
	},
}

// addPrompts registers the ready-made scenarios. They only produce a message;
// the client's model then calls the tools, under the access level in force.
func (s *mcpServer) addPrompts(srv *mcp.Server) {
	for _, d := range promptDefs {
		srv.AddPrompt(&mcp.Prompt{Name: d.name, Title: d.title, Description: d.description, Arguments: d.args},
			func(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
				got := map[string]string{}
				for _, a := range d.args {
					v := strings.TrimSpace(req.Params.Arguments[a.Name])
					if a.Required && v == "" {
						return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf("prompt %s: argument %q is required", d.name, a.Name)}
					}
					got[a.Name] = v
				}
				return &mcp.GetPromptResult{
					Description: d.description,
					Messages:    []*mcp.PromptMessage{{Role: "user", Content: &mcp.TextContent{Text: d.text(got)}}},
				}, nil
			})
	}
}
