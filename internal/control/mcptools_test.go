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
	"stop_session":              {`{"project":"p","session":"s","after_task":true}`, `null`},
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
	"set_global_settings":       {`{"config":{"a":1}}`, `null`},
	"run_preflight":             {`{"project":"p","task":"t"}`, `{"id":7,"steps":[]}`},
	"execute_plan":              {`{"plan_id":"7"}`, `null`},
	"register_mixed_brief":      {`{"id":"b","task":"t"}`, `null`},
	"dispatch_mixed_task":       {`{"project":"p","brief_id":"b","worker":"w"}`, `{"id":"p/b/w","status":"done"}`},
	"get_mixed_rounds":          {`{"project":"p"}`, `[{"id":"p/b/w"}]`},
	"wait_for_worker_status":    {`{"task_id":"p/b/w"}`, `{"event":"worker:done","data":{"status":"done"},"ts":"2026-10-15T10:00:00Z"}`},
	"get_sessions":              {`{}`, `[{"id":"p/s","status":"idle"}]`},
	"get_session_logs":          {`{"session_id":"p/s","tail":5}`, `[{"text":"x"}]`},
	"get_pending_permissions":   {`{}`, `null`},
	"get_metrics":               {`{"project":"p","period":3}`, `1.25`},
	"wait_for_status":           {`{"session_id":"p/s","status":"idle"}`, `{"event":"session:status","data":{"id":"p/s"},"ts":"2026-10-15T10:00:00Z"}`},
	"wait_for_event":            {`{"event":"session:log"}`, `{"event":"session:log","data":null,"ts":"2026-10-15T10:00:00Z"}`},
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

func TestSessionRef_AliasesAndConflict(t *testing.T) {
	_, body, err := TranslateTool("interrupt_session", json.RawMessage(`{"project":"p","session":"s"}`))
	if err != nil || !strings.Contains(mustJSON(body), `"id":"p/s"`) {
		t.Fatalf("legacy pair: %v %v", body, err)
	}
	if _, _, err := TranslateTool("interrupt_session", json.RawMessage(`{"session_id":"p/s","project":"p"}`)); err == nil {
		t.Error("session_id together with project must be an error")
	}
	if _, _, err := TranslateTool("interrupt_session", json.RawMessage(`{}`)); err == nil {
		t.Error("missing session reference must be an error")
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
