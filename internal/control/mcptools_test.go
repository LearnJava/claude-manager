package control

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// toolSamples: representative arguments and the decoded control-plane result
// for every registry tool. A new tool without a sample fails the test.
var toolSamples = map[string]struct {
	args string
	raw  string
}{
	"start_session":             {`{"session_id":"p/s"}`, `null`},
	"stop_session":              {`{"session_id":"p/s","after_task":true}`, `null`},
	"interrupt_session":         {`{"session_id":"p/s"}`, `null`},
	"restart_session":           {`{"session_id":"p/s","resume":true}`, `null`},
	"send_message":              {`{"session_id":"p/s","text":"hi"}`, `null`},
	"set_session_model":         {`{"session_id":"p/s","model":"opus"}`, `null`},
	"set_session_runtime":       {`{"session_id":"p/s","runtime":"hermes"}`, `null`},
	"clear_session_state":       {`{"session_id":"p/s"}`, `null`},
	"start_project":             {`{"project":"p"}`, `null`},
	"stop_project":              {`{"project":"p"}`, `null`},
	"stop_all_sessions":         {`{"confirm":true}`, `null`},
	"start_ad_hoc_chat":         {`{"project":"p"}`, `null`},
	"get_session":               {`{"session_id":"p/s"}`, `{"session":{"id":"p/s","status":"idle"},"saved_state":null,"pending_permissions":[],"pending_question":null}`},
	"list_slash_commands":       {`{"session_id":"p/s"}`, `[{"name":"/init","kind":"command"}]`},
	"get_pending":               {`{}`, `{"permissions":[],"questions":[]}`},
	"get_pending_questions":     {`{"session_id":"p/s"}`, `[{"session_id":"p/s","question":{"id":"q1","question":"Which?","options":["a","b"]}}]`},
	"answer_question":           {`{"session_id":"p/s","question_id":"q1","answer":"a"}`, `null`},
	"add_permission_rule":       {`{"session_id":"p/s","tool":"Bash","pattern":"ls*","decision":"allow"}`, `null`},
	"get_permission_candidates": {`{"project":"p"}`, `{"safe":[],"needs_review":[]}`},
	"get_plan":                  {`{"plan_id":"7"}`, `{"id":7}`},
	"get_projects":              {`{}`, `[{"name":"p","path":"/p","sessions":["s"],"mirror":false,"mixed_programming":false}]`},
	"get_config":                {`{"section":"settings"}`, `{"version":"abc","config":{"settings":{"theme":"dark"},"project":[]}}`},
	"check_environment":         {`{}`, `{"tools":[]}`},
	"get_project_setup":         {`{"project":"p"}`, `{"project":"p","path":"/p","claude_md":true,"session_protocol":false,"mirror":{"enabled":false}}`},
	"add_project":               {`{"name":"p","path":"."}`, `{"dry_run":false,"changed":true,"changes":{"project":{"was":null,"now":{"name":"p"}}},"version":"abc"}`},
	"update_project":            {`{"project":"p","fields":{"journal":true}}`, `{"dry_run":true,"changed":false,"changes":{"journal":{"was":false,"now":true}},"version":"abc"}`},
	"remove_project":            {`{"project":"p"}`, `{"dry_run":false,"changed":true,"changes":{"project":{"was":{"name":"p"},"now":null}},"version":"abc"}`},
	"add_session":               {`{"project":"p","name":"s"}`, `{"dry_run":false,"changed":true,"changes":{"session":{"was":null,"now":{"id":"p/s"}}},"version":"abc"}`},
	"update_session":            {`{"session_id":"p/s","fields":{"model":"opus"}}`, `{"dry_run":false,"changed":true,"changes":{"model":{"was":"","now":"opus"}},"version":"abc"}`},
	"remove_session":            {`{"session_id":"p/s"}`, `{"dry_run":false,"changed":true,"changes":{"session":{"was":{"id":"p/s"},"now":null}},"version":"abc"}`},
	"update_global_settings":    {`{"fields":{"theme":"light"}}`, `{"dry_run":false,"changed":false,"changes":{},"version":"abc"}`},
	"update_config":             {`{"config":{"settings":{}},"version":"abc"}`, `{"version":"def"}`},
	"init_git_repo":             {`{"project":"p"}`, `null`},
	"generate_claude_md":        {`{"project":"p"}`, `null`},
	"install_session_protocol":  {`{"project":"p"}`, `{"project":"p","created":["scripts/worktree-pool.sh"]}`},
	"mirror_project":            {`{"project":"p"}`, `{"enabled":true,"dir":"/m","files":3}`},
	"unmirror_project":          {`{"project":"p"}`, `null`},
	"sync_project_mirrors":      {`{}`, `[{"project":"p","files":3}]`},
	"approve_plan":              {`{"plan":{"id":7}}`, `{"id":7}`},
	"approve_permission":        {`{"session_id":"p/s","request_id":"r1","scope":"once"}`, `null`},
	"deny_permission":           {`{"session_id":"p/s","request_id":"r1"}`, `null`},
	"run_preflight":             {`{"project":"p","task":"t"}`, `{"id":7,"steps":[]}`},
	"execute_plan":              {`{"plan_id":"7"}`, `null`},
	"register_mixed_brief":      {`{"id":"b","task":"t"}`, `null`},
	"dispatch_mixed_task":       {`{"project":"p","brief_id":"b","worker":"w"}`, `{"id":"p/b/w","status":"done"}`},
	"get_mixed_rounds":          {`{"project":"p"}`, `[{"id":"p/b/w"}]`},
	"wait_for_worker_status":    {`{"task_id":"p/b/w"}`, `{"event":"worker:done","data":{"status":"done"},"ts":"2026-10-15T10:00:00Z"}`},
	"get_sessions":              {`{}`, `[{"id":"p/s","status":"idle"}]`},
	"get_session_logs":          {`{"session_id":"p/s","limit":5}`, `[{"text":"x"}]`},
	"get_pending_permissions":   {`{}`, `null`},
	"get_rate_limit":            {`{}`, `{"remaining":1}`},
	"get_history":               {`{"project":"p"}`, `[{"Project":"p","Session":"s"}]`},
	"search_session_log":        {`{"session_id":"p/s","level":"error"}`, `[{"Level":"error","Message":"x"}]`},
	"get_session_metrics":       {`{"session_id":"p/s"}`, `{"session_id":"p/s","total_cost_usd":1.5}`},
	"get_cost":                  {`{"scope":"project","project":"p"}`, `{"date":"","input_tokens":10,"output_tokens":5,"cache_read":0,"cache_creation":0,"total":15,"cost_usd":1.25}`},
	"export_log":                {`{"session_id":"p/s","format":"md"}`, `{"path":"/x/p_s.md","format":"md","entries":3,"bytes":120}`},
	"import_project_logs":       {`{"project":"p"}`, `{"files":2,"runs":2,"actions":9,"skipped":0,"errors":0}`},
	"get_project_log_files":     {`{"project":"p"}`, `[{"name":"a.log","path":"/p/a.log","size":10,"mod_time":"2026-10-15T10:00:00Z"}]`},
	"clear_project_logs":        {`{"project":"p","confirm":true}`, `null`},
	"clean_old_logs":            {`{"retention_days":30,"confirm":true}`, `null`},
	"get_top_actions":           {`{"project":"p"}`, `[{"Sig":"Bash:ls","Count":3}]`},
	"get_action_samples":        {`{"project":"p","signature":"Bash:ls"}`, `[{"Tool":"Bash","Arg":"ls"}]`},
	"get_duration_profile":      {`{"project":"p"}`, `[{"Sig":"Bash:go test","Count":4}]`},
	"get_token_attribution":     {`{"project":"p","top_n":5}`, `{"TotalEstTokens":10,"BySignature":[]}`},
	"get_model_advice":          {`{"session_id":"p/s"}`, `{"auto_routing":true,"recommendation":{"model":"sonnet"}}`},
	"session_preflight":         {`{"session_id":"p/s"}`, `{"id":3}`},
	"generate_roadmap":          {`{"project":"p","idea":"an app"}`, `{"id":9,"kind":"roadmap"}`},
	"get_latest_draft_roadmap":  {`{"project":"p"}`, `null`},
	"approve_roadmap":           {`{"plan_id":"9"}`, `{"id":9}`},
	"get_session_roadmap":       {`{"session_id":"p/s"}`, `{"title":"R","done":1,"total":2,"nodes":[{"kind":"phase","children":[{"kind":"task"}]}]}`},
	"get_roadmap_task_detail":   {`{"project":"p","path":"tasks/1.md"}`, `"body"`},
	"get_roadmap_row_detail":    {`{"project":"p","roadmap_file":"ROADMAP.md","line":3}`, `"row"`},
	"get_skill_candidates":      {`{"project":"p"}`, `[{"Sig":["Bash:ls"],"DistinctRuns":3,"Score":2.5,"RelatedFailures":[]}]`},
	"distill_skill":             {`{"project":"p","sig":["Bash:ls"]}`, `{"ID":7,"Name":"ls","Status":"draft","MD":"# ls"}`},
	"get_failure_rules":         {`{"project":"p"}`, `{"Rules":[{"ID":4,"ErrorKey":"boom","RuleText":"r","Status":"draft"}],"Pending":1}`},
	"draft_failure_rules":       {`{"project":"p"}`, `[{"ID":4,"ErrorKey":"boom","RuleText":"r","Status":"draft"}]`},
	"accept_failure_rule":       {`{"rule_id":"4","text":"r"}`, `null`},
	"dismiss_failure_rule":      {`{"rule_id":"4"}`, `null`},
	"get_skills":                {`{"project":"p"}`, `[{"ID":7,"Name":"ls","Status":"draft","MD":"# ls"}]`},
	"approve_skill":             {`{"project":"p","skill_id":"7"}`, `{"path":"/p/.claude/skills/ls/SKILL.md"}`},
	"archive_skill":             {`{"skill_id":"7"}`, `null`},
	"restore_skill":             {`{"skill_id":"7"}`, `{"path":"/p/.claude/skills/ls/SKILL.md"}`},
	"get_skill_usage":           {`{"project":"p"}`, `{"7":{"skill_id":7,"name":"ls","loads":2}}`},
	"get_skill_quality":         {`{"project":"p"}`, `[{"skill_id":7,"skill_name":"ls"}]`},
	"get_skill_autopilot":       {`{"project":"p"}`, `{"enabled":true,"tracking":true,"daily_budget_usd":2,"spent_today_usd":0.5,"spent_total_usd":1,"trial_runs":5}`},
	"set_skill_autopilot":       {`{"project":"p","enabled":true,"daily_budget_usd":2}`, `null`},
	"run_skill_autopilot":       {`{"project":"p"}`, `{"applied":["ls"],"idle":""}`},
	"get_mixed_quality":         {`{"project":"p"}`, `[{"worker":"w","tasks_total":1}]`},
	"cancel_mixed_task":         {`{"task_id":"p/b/w"}`, `null`},
	"wait_for_status":           {`{"session_id":"p/s","status":"idle"}`, `{"event":"session:status","data":{"id":"p/s"},"ts":"2026-10-15T10:00:00Z"}`},
	"wait_for_event":            {`{"event":"session:log"}`, `{"event":"session:log","data":null,"ts":"2026-10-15T10:00:00Z"}`},
	"wait_for_any":              {`{"session_ids":["p/s"]}`, `{"event":"session:question","data":{"id":"p/s"},"ts":"2026-10-15T10:00:00Z"}`},
}

func TestMCPTools_StructuredContentMatchesOutputSchema(t *testing.T) {
	seen := map[string]bool{}
	for _, tool := range MCPTools {
		seen[tool.Name] = true
		sample, ok := toolSamples[tool.Name]
		if !ok {
			t.Errorf("%s: no entry in toolSamples", tool.Name)
			continue
		}
		var raw any
		if err := json.Unmarshal([]byte(sample.raw), &raw); err != nil {
			t.Fatalf("%s: bad sample: %v", tool.Name, err)
		}
		out, summary, err := ShapeResult(tool.Name, json.RawMessage(sample.args), raw)
		if err != nil {
			t.Errorf("%s: ShapeResult: %v", tool.Name, err)
			continue
		}
		if summary == "" || strings.HasPrefix(strings.TrimSpace(summary), "{") {
			t.Errorf("%s: summary must be a short sentence, got %q", tool.Name, summary)
		}
		validateAgainst(t, tool.Name, tool.OutputSchema, out)

		// Arguments must satisfy the declared input schema too.
		var args any
		_ = json.Unmarshal([]byte(sample.args), &args)
		validateAgainst(t, tool.Name+" args", tool.InputSchema, args)
	}
	for name := range toolSamples {
		if !seen[name] {
			t.Errorf("toolSamples has %q, not in the registry", name)
		}
	}
}

func validateAgainst(t *testing.T, what string, schema json.RawMessage, v any) {
	t.Helper()
	var s jsonschema.Schema
	if err := json.Unmarshal(schema, &s); err != nil {
		t.Errorf("%s: schema: %v", what, err)
		return
	}
	res, err := s.Resolve(nil)
	if err != nil {
		t.Errorf("%s: resolve: %v", what, err)
		return
	}
	b, _ := json.Marshal(v) // normalise structs to generic JSON
	var inst any
	_ = json.Unmarshal(b, &inst)
	if err := res.Validate(inst); err != nil {
		t.Errorf("%s: %v\ninstance: %s", what, err, b)
	}
}

func TestMCPTools_RegistryInvariants(t *testing.T) {
	names := map[string]bool{}
	for _, tool := range MCPTools {
		if names[tool.Name] {
			t.Errorf("duplicate tool %s", tool.Name)
		}
		names[tool.Name] = true
		if tool.Title == "" || tool.Description == "" || tool.Level == "" {
			t.Errorf("%s: title, description and level are required", tool.Name)
		}
		if !json.Valid(tool.InputSchema) || !json.Valid(tool.OutputSchema) {
			t.Errorf("%s: invalid schema", tool.Name)
		}
	}
}

func TestSessionRef_Required(t *testing.T) {
	if _, _, err := TranslateTool("interrupt_session", json.RawMessage(`{"session_id":"p/s"}`)); err != nil {
		t.Fatalf("session_id: %v", err)
	}
	if _, _, err := TranslateTool("interrupt_session", json.RawMessage(`{}`)); err == nil {
		t.Error("missing session reference must be an error")
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
