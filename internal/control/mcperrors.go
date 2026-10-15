package control

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	reRPCPrefix     = regexp.MustCompile(`^rpc error -?\d+: `)
	reSessionAbsent = regexp.MustCompile(`session "([^"]*)" not found`)
	reProjectAbsent = regexp.MustCompile(`project "([^"]*)" not found`)
	reNoPermission  = regexp.MustCompile(`no pending permission with id "([^"]*)"`)
	reAlreadyRun    = regexp.MustCompile(`session "([^"]*)" already running \(status=([a-z_]+)\)`)
	reTaskNotRun    = regexp.MustCompile(`mixed task "([^"]*)" is not running`)
)

// ExplainError turns a control-plane failure into an actionable tool error:
// what is wrong and what to do next (docs/mcp.md, "Ошибки"). lookup runs a
// read-only tool (get_sessions, get_pending_permissions) and returns its
// items; it is only called when the message needs context and may return nil.
// Messages that need no help come back unchanged.
func ExplainError(tool string, err error, lookup func(tool string) []any) string {
	msg := reRPCPrefix.ReplaceAllString(err.Error(), "")
	if lookup == nil {
		lookup = func(string) []any { return nil }
	}
	switch {
	case reSessionAbsent.MatchString(msg):
		id := reSessionAbsent.FindStringSubmatch(msg)[1]
		return sessionAbsent(id, lookup("get_sessions"))
	case reProjectAbsent.MatchString(msg):
		name := reProjectAbsent.FindStringSubmatch(msg)[1]
		if known := projectNames(lookup("get_sessions")); len(known) > 0 {
			return fmt.Sprintf("Project %q not found. Known projects: %s. Sessions are listed by get_sessions.", name, joinCap(known))
		}
		return fmt.Sprintf("Project %q not found. Project names appear in the session_id of get_sessions (project/session).", name)
	case reNoPermission.MatchString(msg):
		id := reNoPermission.FindStringSubmatch(msg)[1]
		if ids := fieldValues(lookup("get_pending_permissions"), "id"); len(ids) > 0 {
			return fmt.Sprintf("Permission request %q is not pending (it may already be answered). Pending request ids: %s. Details: get_pending_permissions.", id, joinCap(ids))
		}
		return fmt.Sprintf("Permission request %q is not pending (it may already be answered); no requests are waiting now. Check get_pending_permissions.", id)
	case reAlreadyRun.MatchString(msg):
		m := reAlreadyRun.FindStringSubmatch(msg)
		return fmt.Sprintf("Session %q is already running (status %s). To talk to it use send_message; to cancel its current turn use interrupt_session; to restart it use stop_session, then start_session.", m[1], m[2])
	case reTaskNotRun.MatchString(msg):
		return fmt.Sprintf("Mixed task %q is not running, so it cannot be cancelled. Its final state is in get_mixed_rounds.", reTaskNotRun.FindStringSubmatch(msg)[1])
	case msg == "wait timeout":
		return fmt.Sprintf("%s timed out: the awaited condition did not happen in time. Check the current state with get_sessions or get_session_logs, then call again, optionally with a larger timeout_ms.", tool)
	case strings.HasPrefix(msg, "method not found"):
		return msg + ". The running Claude Manager is older than cm-mcp and lacks this operation; update and restart the app."
	case strings.HasPrefix(msg, "bad arguments"):
		return msg + ". Check the arguments against the tool's inputSchema."
	}
	return msg
}

func sessionAbsent(id string, sessions []any) string {
	project, _, _ := strings.Cut(id, "/")
	if inProject := namesWithPrefix(fieldValues(sessions, "id"), project+"/"); len(inProject) > 0 {
		return fmt.Sprintf("Session %q not found. Sessions in project %q: %s. All sessions: get_sessions.", id, project, joinCap(inProject))
	}
	if known := projectNames(sessions); len(known) > 0 {
		return fmt.Sprintf("Session %q not found, and there is no project %q. Known projects: %s. All sessions: get_sessions.", id, project, joinCap(known))
	}
	return fmt.Sprintf("Session %q not found. List sessions with get_sessions; ids look like project/session, e.g. lumen/P1.", id)
}

// fieldValues collects the string field key of every map item, in order.
func fieldValues(items []any, key string) []string {
	var out []string
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if v, ok := m[key].(string); ok && v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

func namesWithPrefix(ids []string, prefix string) []string {
	var out []string
	for _, id := range ids {
		if strings.HasPrefix(id, prefix) {
			out = append(out, id)
		}
	}
	return out
}

// projectNames returns the distinct project parts of the session ids, sorted.
func projectNames(sessions []any) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range fieldValues(sessions, "id") {
		if p, _, ok := strings.Cut(id, "/"); ok && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// joinCap lists at most 20 names so an error stays short.
func joinCap(names []string) string {
	const max = 20
	if len(names) <= max {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, and %d more", strings.Join(names[:max], ", "), len(names)-max)
}
