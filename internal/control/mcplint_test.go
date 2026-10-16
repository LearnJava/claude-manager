package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

// The registry linter (MCP-04): the standard every tool is held to, as code.
// lintTool returns one message per violation; TestRegistryLint requires none
// for the real registry and the negative tests prove each rule bites.

var reDescription = regexp.MustCompile(`^\S.*\. Use when: .+ Not for: .+ Returns: .+ Side effects: .+ Access: (read|operate|admin)\. Example: (\{.*\})$`)

// Words from the implementation that mean nothing to an agent reading tools/list.
var descriptionJargon = []string{"inputCh", "permissionCh", "atomic", "ring buffer", "control-plane", "goroutine", "mutex"}

const maxDescriptionLen = 1100

func lintDescription(tool Tool) []string {
	var bad []string
	m := reDescription.FindStringSubmatch(tool.Description)
	if m == nil {
		return []string{tool.Name + ": description does not follow the template: What. Use when: … Not for: … Returns: … Side effects: … Access: <level>. Example: {…}"}
	}
	if len(tool.Description) > maxDescriptionLen {
		bad = append(bad, fmt.Sprintf("%s: description is %d chars, limit %d", tool.Name, len(tool.Description), maxDescriptionLen))
	}
	if m[1] != string(tool.Level) {
		bad = append(bad, fmt.Sprintf("%s: description says access %s, tool level is %s", tool.Name, m[1], tool.Level))
	}
	for _, w := range descriptionJargon {
		if strings.Contains(strings.ToLower(tool.Description), strings.ToLower(w)) {
			bad = append(bad, fmt.Sprintf("%s: description uses internal jargon %q", tool.Name, w))
		}
	}
	var args any
	if err := json.Unmarshal([]byte(m[2]), &args); err != nil {
		return append(bad, tool.Name+": example arguments are not valid JSON: "+err.Error())
	}
	if err := validateSchema(tool.InputSchema, args); err != nil {
		bad = append(bad, fmt.Sprintf("%s: example arguments violate the input schema: %v", tool.Name, err))
	}
	return bad
}

var reDefaultMention = regexp.MustCompile(`\(default `)

func lintProperties(name, kind string, raw json.RawMessage, input bool) []string {
	var s jsonschema.Schema
	if err := json.Unmarshal(raw, &s); err != nil {
		return []string{fmt.Sprintf("%s: %s schema: %v", name, kind, err)}
	}
	var bad []string
	for prop, ps := range s.Properties {
		if strings.TrimSpace(ps.Description) == "" {
			bad = append(bad, fmt.Sprintf("%s: %s %q has no description", name, kind, prop))
		}
		if !input {
			continue
		}
		if reDefaultMention.MatchString(ps.Description) && len(ps.Default) == 0 {
			bad = append(bad, fmt.Sprintf("%s: %q documents a default but the schema has none", name, prop))
		}
		switch prop {
		case "status", "scope", "decision":
			if len(ps.Enum) == 0 {
				bad = append(bad, fmt.Sprintf("%s: %q takes a closed set of values but has no enum", name, prop))
			}
		}
	}
	return bad
}

func lintResponse(name string, out any) []string {
	b, _ := json.Marshal(out)
	if len(b) > MaxResponseBytes {
		return []string{fmt.Sprintf("%s: response is %d bytes, limit %d", name, len(b), MaxResponseBytes)}
	}
	return nil
}

func lintTool(tool Tool) []string {
	bad := lintDescription(tool)
	bad = append(bad, lintProperties(tool.Name, "input", tool.InputSchema, true)...)
	bad = append(bad, lintProperties(tool.Name, "output", tool.OutputSchema, false)...)
	return bad
}

func validateSchema(schema json.RawMessage, v any) error {
	var s jsonschema.Schema
	if err := json.Unmarshal(schema, &s); err != nil {
		return err
	}
	res, err := s.Resolve(nil)
	if err != nil {
		return err
	}
	return res.Validate(v)
}

func TestRegistryLint(t *testing.T) {
	for _, tool := range MCPTools {
		for _, msg := range lintTool(tool) {
			t.Error(msg)
		}
	}
}

func TestLint_RejectsTemplateViolations(t *testing.T) {
	good := MCPTools[0]
	cases := map[string]func(*Tool){
		"old free-form description": func(x *Tool) { x.Description = "Start a session by session_id." },
		"missing Not for":           func(x *Tool) { x.Description = strings.Replace(x.Description, " Not for:", " Never:", 1) },
		"wrong access level":        func(x *Tool) { x.Description = strings.Replace(x.Description, "Access: operate", "Access: read", 1) },
		"internal jargon":           func(x *Tool) { x.Description = strings.Replace(x.Description, "Returns:", "Returns: inputCh state,", 1) },
		"example breaks schema":     func(x *Tool) { x.Description = x.Description[:strings.Index(x.Description, "Example:")] + `Example: {"session_id":5}` },
		"example not JSON":          func(x *Tool) { x.Description = x.Description[:strings.Index(x.Description, "Example:")] + `Example: {oops}` },
		"too long":                  func(x *Tool) { x.Description = strings.Replace(x.Description, "Returns:", "Returns: "+strings.Repeat("x", 1200), 1) },
	}
	if got := lintDescription(good); len(got) != 0 {
		t.Fatalf("baseline tool must be clean: %v", got)
	}
	for name, mutate := range cases {
		x := good
		mutate(&x)
		if len(lintDescription(x)) == 0 {
			t.Errorf("%s: linter accepted it", name)
		}
	}
}

func TestLint_RejectsParameterViolations(t *testing.T) {
	type undocumented struct {
		Plain  string `json:"plain"`
		Status string `json:"status" jsonschema:"A status"`
		Count  int    `json:"count,omitempty" jsonschema:"How many (default 5)"`
	}
	raw := mustSchema[undocumented](nil)
	got := strings.Join(lintProperties("fixture", "input", raw, true), "\n")
	for _, want := range []string{`"plain" has no description`, `"status" takes a closed set`, `"count" documents a default`} {
		if !strings.Contains(got, want) {
			t.Errorf("linter missed %q in:\n%s", want, got)
		}
	}
	fixed := mustSchema[undocumented](func(p map[string]*jsonschema.Schema) {
		p["plain"].Description = "ok"
		p["status"].Enum = []any{"a"}
		p["count"].Default = json.RawMessage(`5`)
	})
	if got := lintProperties("fixture", "input", fixed, true); len(got) != 0 {
		t.Errorf("fixed fixture still flagged: %v", got)
	}
}

func TestLint_RejectsOversizedResponse(t *testing.T) {
	big := []any{map[string]any{"text": strings.Repeat("x", MaxResponseBytes+1)}}
	if len(lintResponse("fixture", sessionsOut{Sessions: big})) == 0 {
		t.Error("response over the limit must be flagged")
	}
	if len(lintResponse("fixture", sessionsOut{Sessions: []any{"small"}})) != 0 {
		t.Error("small response must pass")
	}
}

// fatEntries builds n log-like entries of about 1 KiB each.
func fatEntries(n int) []any {
	items := make([]any, n)
	for i := range items {
		items[i] = map[string]any{"n": float64(i), "text": strings.Repeat("y", 1000)}
	}
	return items
}

func TestShapeResult_ListsStayWithinLimitAndPage(t *testing.T) {
	// 300 entries of ~1 KiB would be ~300 KiB: the byte limit must cut the page.
	raw := fatEntries(300)
	for _, tool := range []string{"get_sessions", "get_pending_permissions"} {
		out, summary, err := ShapeResult(tool, json.RawMessage(`{"limit":300}`), raw)
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range lintResponse(tool, out) {
			t.Error(msg)
		}
		b, _ := json.Marshal(out)
		var m map[string]any
		_ = json.Unmarshal(b, &m)
		if m["truncated"] != true || m["next_cursor"] == nil {
			t.Fatalf("%s: expected truncated page with next_cursor, got %s", tool, b[:200])
		}
		if !strings.Contains(summary, "cursor="+fmt.Sprintf("%q", m["next_cursor"])) {
			t.Errorf("%s: summary lacks the continuation hint: %q", tool, summary)
		}
		// The next page continues exactly where this one stopped.
		next := m["next_cursor"].(string)
		out2, _, _ := ShapeResult(tool, json.RawMessage(`{"limit":300,"cursor":"`+next+`"}`), raw)
		b2, _ := json.Marshal(out2)
		var m2 map[string]any
		_ = json.Unmarshal(b2, &m2)
		first := firstN(m2)
		if fmt.Sprint(int(first)) != next {
			t.Errorf("%s: second page starts at %v, want %s", tool, first, next)
		}
	}
}

func firstN(m map[string]any) float64 {
	for _, v := range m {
		if l, ok := v.([]any); ok && len(l) > 0 {
			return l[0].(map[string]any)["n"].(float64)
		}
	}
	return -1
}

func TestShapeResult_SmallListIsNotTruncated(t *testing.T) {
	out, summary, _ := ShapeResult("get_sessions", json.RawMessage(`{}`), []any{map[string]any{"id": "p/s"}})
	b, _ := json.Marshal(out)
	if strings.Contains(string(b), "next_cursor") || !strings.Contains(string(b), `"truncated":false`) {
		t.Errorf("unexpected paging fields: %s", b)
	}
	if summary != "1 session." {
		t.Errorf("summary: %q", summary)
	}
}

func TestSessionLogs_Paging(t *testing.T) {
	_, body, err := TranslateTool("get_session_logs", json.RawMessage(`{"session_id":"p/s","limit":2,"cursor":"4"}`))
	if err != nil {
		t.Fatal(err)
	}
	params := body.(map[string]any)["params"].(map[string]any)
	if params["offset"] != 4 || params["limit"] != 3 {
		t.Fatalf("want offset 4, limit 3 (page + 1 probe), got %v", params)
	}
	// Three rows back for a page of two: one more page exists, from cursor 6.
	raw := []any{map[string]any{"n": 4.0}, map[string]any{"n": 5.0}, map[string]any{"n": 6.0}}
	out, summary, _ := ShapeResult("get_session_logs", json.RawMessage(`{"session_id":"p/s","limit":2,"cursor":"4"}`), raw)
	lo := out.(logsOut)
	if len(lo.Entries) != 2 || lo.NextCursor != "6" || !lo.Truncated || !strings.Contains(summary, `cursor="6"`) {
		t.Errorf("page: %+v summary %q", lo, summary)
	}
}

func TestPaging_RejectsBadArguments(t *testing.T) {
	for _, args := range []string{`{"cursor":"abc"}`, `{"cursor":"-1"}`, `{"limit":-3}`, `{"limit":501}`} {
		for _, tool := range []string{"get_sessions", "get_pending_permissions"} {
			if _, _, err := TranslateTool(tool, json.RawMessage(args)); err == nil {
				t.Errorf("%s %s: expected an error", tool, args)
			}
		}
	}
	_, _, err := TranslateTool("get_sessions", json.RawMessage(`{"cursor":"abc"}`))
	if err == nil || !strings.Contains(err.Error(), "next_cursor") {
		t.Errorf("the error must say what to pass instead: %v", err)
	}
}

// A tool that stops or interrupts a session must say so in its annotations.
func TestLint_StoppingToolsAreDestructive(t *testing.T) {
	for _, tool := range MCPTools {
		stops := strings.HasPrefix(tool.Name, "stop_") || strings.HasPrefix(tool.Name, "interrupt_")
		if stops && !tool.Annotations.Destructive {
			t.Errorf("%s stops or interrupts work but is not marked destructive", tool.Name)
		}
	}
}

func TestDenyPermission_ScopeMapsToDecision(t *testing.T) {
	for scope, want := range map[string]string{"": "deny", "once": "deny", "always": "deny_always"} {
		args := `{"session_id":"p/s","request_id":"r"` + map[bool]string{true: `,"scope":"` + scope + `"`, false: ""}[scope != ""] + `}`
		_, body, err := TranslateTool("deny_permission", json.RawMessage(args))
		if err != nil {
			t.Fatalf("%q: %v", scope, err)
		}
		if got := body.(map[string]any)["params"].(map[string]string)["decision"]; got != want {
			t.Errorf("scope %q: decision %q, want %q", scope, got, want)
		}
	}
	if _, _, err := TranslateTool("deny_permission", json.RawMessage(`{"session_id":"p/s","request_id":"r","scope":"session"}`)); err == nil {
		t.Error("deny scope session must be refused")
	}
	if RPCLevel("RespondPermission", "deny_always") != RPCLevel("RespondPermission", "allow_always") || RPCLevel("RespondPermission", "deny_always") != LevelAdmin {
		t.Error("deny_always must need the same level as allow_always (admin)")
	}
	if RPCLevel("RespondPermission", "allow_similar") != RPCLevel("RespondPermission", "allow_session") {
		t.Error("allow_similar must need the same level as allow_session")
	}
}

func TestApprovePermission_ScopeMapsToDecision(t *testing.T) {
	for scope, want := range map[string]string{"": "allow", "once": "allow", "session": "allow_session", "similar": "allow_similar", "always": "allow_always"} {
		args := `{"session_id":"p/s","request_id":"r"` + map[bool]string{true: `,"scope":"` + scope + `"`, false: ""}[scope != ""] + `}`
		_, body, err := TranslateTool("approve_permission", json.RawMessage(args))
		if err != nil {
			t.Fatalf("%q: %v", scope, err)
		}
		if got := body.(map[string]any)["params"].(map[string]string)["decision"]; got != want {
			t.Errorf("scope %q: decision %q, want %q", scope, got, want)
		}
	}
	if _, _, err := TranslateTool("approve_permission", json.RawMessage(`{"session_id":"p/s","request_id":"r","scope":"forever"}`)); err == nil ||
		!strings.Contains(err.Error(), "once, session, similar or always") {
		t.Errorf("bad scope: %v", err)
	}
}

func TestExplainError(t *testing.T) {
	sessions := []any{map[string]any{"id": "lumen/P1"}, map[string]any{"id": "lumen/P2"}, map[string]any{"id": "cm/Chat"}}
	perms := []any{map[string]any{"id": "r1"}, map[string]any{"id": "r2"}}
	lookup := func(tool string) []any {
		switch tool {
		case "get_sessions":
			return sessions
		case "get_pending_permissions":
			return perms
		}
		return nil
	}
	cases := []struct {
		name string
		err  string
		look func(string) []any
		want []string
	}{
		{"session in known project", `rpc error -32000: session "lumen/P9" not found`, lookup, []string{`"lumen/P9" not found`, "lumen/P1, lumen/P2", "get_sessions"}},
		{"unknown project", `session "zzz/P1" not found`, lookup, []string{`no project "zzz"`, "cm, lumen"}},
		{"no context available", `session "a/b" not found`, nil, []string{"get_sessions", "lumen/P1"}},
		{"project", `project "zzz" not found`, lookup, []string{"Known projects: cm, lumen"}},
		{"permission", `session p/s: no pending permission with id "r9"`, lookup, []string{`"r9" is not pending`, "r1, r2"}},
		{"permission none", `no pending permission with id "r9"`, nil, []string{"no requests are waiting", "get_pending_permissions"}},
		{"already running", `session "lumen/P1" already running (status=working)`, lookup, []string{"status working", "send_message", "interrupt_session", "stop_session"}},
		{"timeout", `wait timeout`, nil, []string{"wait_for_status timed out", "timeout_ms", "get_sessions"}},
		{"old app", `rpc error -32601: method not found: Foo`, nil, []string{"update and restart"}},
		{"bad arguments", `bad arguments: json: cannot unmarshal`, nil, []string{"inputSchema"}},
		{"unknown stays", `rpc error -32000: disk full`, nil, []string{"disk full"}},
	}
	for _, c := range cases {
		got := ExplainError("wait_for_status", errors.New(c.err), c.look)
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s: %q lacks %q", c.name, got, w)
			}
		}
		if strings.HasPrefix(got, "rpc error") {
			t.Errorf("%s: transport prefix leaked: %q", c.name, got)
		}
	}
}

func TestExplainError_CapsLongLists(t *testing.T) {
	var many []any
	for i := 0; i < 50; i++ {
		many = append(many, map[string]any{"id": fmt.Sprintf("p/S%d", i)})
	}
	got := ExplainError("start_session", errors.New(`session "p/zzz" not found`), func(string) []any { return many })
	if !strings.Contains(got, "and 30 more") {
		t.Errorf("list not capped: %q", got)
	}
}
