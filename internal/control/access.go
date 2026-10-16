package control

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"claude-manager/internal/logger"
)

// Access control (MCP-05, MCP-19, docs/mcp.md "Уровни доступа"). cm-mcp
// enforces the level by hiding tools; the control-plane enforces it again per
// RPC method, and a request that names no level is treated as operate, so a
// plain call to /rpc cannot reach admin methods by omission.
//
// The level is a guard against mistakes of an agent that works through the MCP
// tools. It is NOT a security boundary against an agent with a shell under the
// same Windows user: that agent can read ~/.claude-manager/control.json and send
// X-CM-Level: admin itself. What the per-session token (CM_CONTROL_TOKEN) does
// close is the caller identity: the self-stop guard keys off the session the
// token was issued to, not off a header the client writes.

// Headers cm-mcp sends with every control-plane request.
const (
	HeaderLevel     = "X-CM-Level"          // access level the cm-mcp process runs at
	HeaderClient    = "X-CM-Client"         // MCP clientInfo name/version, for the audit log
	HeaderCaller    = "X-CM-Caller-Session" // "{project}/{session}" hosting the cm-mcp process
	HeaderAllowSelf = "X-CM-Allow-Self"     // "1": the human allowed self-stop/restart/clear
)

// DefaultLevel is what cm-mcp runs at when no level is configured: ordinary
// operator work, no approval on a human's behalf, no configuration changes.
const DefaultLevel = LevelOperate

// LevelRank orders the levels; unknown levels rank -1.
func LevelRank(l AccessLevel) int {
	switch l {
	case LevelRead:
		return 0
	case LevelOperate:
		return 1
	case LevelAdmin:
		return 2
	}
	return -1
}

// ParseLevel validates a level name. The empty string is DefaultLevel.
func ParseLevel(s string) (AccessLevel, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return DefaultLevel, nil
	}
	l := AccessLevel(s)
	if LevelRank(l) < 0 {
		return "", fmt.Errorf("access level %q is not valid: use read, operate or admin", s)
	}
	return l, nil
}

// Allows reports whether a process running at l may use something that needs need.
func (l AccessLevel) Allows(need AccessLevel) bool {
	return LevelRank(l) >= LevelRank(need) && LevelRank(l) >= 0
}

// LevelHint is the actionable tail of every "level too low" error.
func LevelHint(need AccessLevel) string {
	return fmt.Sprintf("requires access level %s: the human can enable it by starting cm-mcp with CM_MCP_LEVEL=%s "+
		"(or --level %s) in the MCP client config; an agent cannot raise its own level", need, need, need)
}

// rpcLevels is the level each control-plane RPC method needs. Methods missing
// from the table need admin (deny by default); TestRPCLevelsCoverRegistry keeps
// it complete. RespondPermission is special-cased by RPCLevel.
var rpcLevels = map[string]AccessLevel{
	// read
	"GetPendingPermissions": LevelRead, "GetAllSessions": LevelRead, "GetSession": LevelRead,
	"GetSessionLog": LevelRead, "GetHistory": LevelRead, "GetSessionMetrics": LevelRead,
	"GetDailyCost": LevelRead, "GetProjectCost": LevelRead, "GetRateLimitStatus": LevelRead,
	"GetSessionState": LevelRead, "GetPlan": LevelRead, "GetMixedRounds": LevelRead,
	"GetMixedQuality": LevelRead, "ListSlashCommands": LevelRead, "GetSessionOverview": LevelRead,
	"GetProjects": LevelRead, "GetConfigView": LevelRead, "CheckEnvironment": LevelRead,
	"HasClaudeMd": LevelRead, "HasSessionProtocol": LevelRead, "GetProjectMirror": LevelRead, "GetProjectSetup": LevelRead,
	"GetPendingQuestions": LevelRead, "GetPending": LevelRead, "GetPermissionCandidates": LevelRead,
	"GetDailyTokens": LevelRead, "GetProjectTokens": LevelRead, "SearchSessionLog": LevelRead,
	"GetProjectLogFiles": LevelRead, "GetTopActions": LevelRead, "GetActionSamples": LevelRead,
	"GetDurationProfile": LevelRead, "GetTokenAttribution": LevelRead, "GetAutoModelRouting": LevelRead,
	"GetModelRecommendation": LevelRead, "GetModelAdvice": LevelRead,
	"ExportLog": LevelOperate, "ImportProjectLogs": LevelOperate,
	"ClearProjectLogs": LevelAdmin, "CleanOldLogs": LevelAdmin,
	"GetLatestDraftRoadmap": LevelRead, "GetSessionRoadmap": LevelRead, "GetRoadmapTaskDetail": LevelRead,
	"GetRoadmapRowDetail": LevelRead,
	"GenerateRoadmap": LevelOperate, "ApproveRoadmap": LevelAdmin,
	"GetSkills": LevelRead, "GetSkillCandidates": LevelRead, "GetSkillQuality": LevelRead, "GetSkillUsage": LevelRead,
	"GetSkillAutopilot": LevelRead, "DistillSkill": LevelOperate, "ArchiveSkill": LevelOperate,
	"RunSkillAutopilot": LevelOperate, "ApproveSkill": LevelAdmin, "RestoreSkill": LevelAdmin, "SetSkillAutopilot": LevelAdmin,
	// operate
	"StartSession": LevelOperate, "StartSessionWithOverride": LevelOperate, "InterruptSession": LevelOperate,
	"StopSession": LevelOperate, "RestartSession": LevelOperate, "ResumeSession": LevelOperate,
	"StartProject": LevelOperate, "SendMessage": LevelOperate, "RunPreflight": LevelOperate,
	"SessionPreflight": LevelOperate, "ExecutePlan": LevelOperate, "RegisterMixedBrief": LevelOperate,
	"DispatchMixedTask": LevelOperate, "CancelMixedTask": LevelOperate,
	"StartSessionWithModel": LevelOperate, "StartAdHocChatSession": LevelOperate,
	"SyncProjectMirrors": LevelOperate,
	"AnswerQuestion":     LevelOperate, "SendMessageWithImages": LevelOperate, "SetSessionRuntime": LevelOperate, "SetSessionModel": LevelOperate,
	// admin
	"StopAll": LevelAdmin, "StopProject": LevelAdmin, "ClearSessionState": LevelAdmin,
	"ApprovePlan": LevelAdmin, "AddPermissionRule": LevelAdmin, "UpdateConfig": LevelAdmin,
	"AddProject": LevelAdmin, "RemoveProject": LevelAdmin, "UpdateProject": LevelAdmin, "AddSession": LevelAdmin,
	"RemoveSession": LevelAdmin, "UpdateSession": LevelAdmin, "UpdateGlobalSettings": LevelAdmin, "ReplaceConfig": LevelAdmin,
	"InitGitRepo": LevelAdmin, "GenerateClaudeMdSession": LevelAdmin, "InstallSessionProtocol": LevelAdmin,
	"MirrorProject": LevelAdmin, "UnmirrorProject": LevelAdmin, "GetConfig": LevelAdmin,
}

// RPCLevel returns the level needed to call method with decision (only
// RespondPermission looks at it: a refusal is operate, anything else approves
// on the human's behalf and is admin).
func RPCLevel(method, decision string) AccessLevel {
	if method == "RespondPermission" {
		if decision == "deny" {
			return LevelOperate
		}
		return LevelAdmin
	}
	if l, ok := rpcLevels[method]; ok {
		return l
	}
	return LevelAdmin
}

// requestLevel reads the caller's level from the headers. A request without
// the header is DefaultLevel (operate): the test harness and the e2e runner
// send X-CM-Level: admin explicitly. cm-mcp always sends the header.
func requestLevel(r *http.Request) (AccessLevel, error) {
	h := r.Header.Get(HeaderLevel)
	if h == "" {
		return DefaultLevel, nil
	}
	l := AccessLevel(strings.ToLower(strings.TrimSpace(h)))
	if LevelRank(l) < 0 {
		return "", fmt.Errorf("access level %q is not valid: use read, operate or admin", h)
	}
	return l, nil
}

type sessionCallerKey struct{}

// requestCaller is the session a request comes from. A request authenticated
// with a per-session token is that session whatever X-CM-Caller-Session says;
// otherwise (the shared token) the header is the only information available.
func requestCaller(r *http.Request) string {
	if id, ok := r.Context().Value(sessionCallerKey{}).(string); ok {
		return id
	}
	return strings.TrimSpace(r.Header.Get(HeaderCaller))
}

// selfTarget reports whether an RPC call acts on the calling session itself (or,
// for the mass-stop methods, on a set that necessarily contains it).
// params are the decoded fields the methods use.
func selfTarget(method, caller string, p map[string]any) bool {
	if caller == "" {
		return false
	}
	str := func(k string) string { s, _ := p[k].(string); return s }
	switch method {
	case "StopSession", "RestartSession", "InterruptSession", "ResumeSession", "SetSessionRuntime":
		return str("id") == caller
	case "RemoveSession", "UpdateSession":
		return str("project")+"/"+str("session") == caller
	case "ClearSessionState":
		return str("project")+"/"+str("session") == caller
	case "StopAll":
		return true
	case "StopProject", "ClearProjectLogs":
		proj, _, _ := strings.Cut(caller, "/")
		return str("project") == proj
	}
	return false
}

var secretKey = regexp.MustCompile(`(?i)token|secret|key|password|passwd|authorization|credential`)

// redact returns v with secret-looking fields masked and long strings cut, for
// the audit log.
func redact(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			if secretKey.MatchString(k) {
				out[k] = "***"
			} else {
				out[k] = redact(val)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = redact(val)
		}
		return out
	case string:
		if r := []rune(x); len(r) > 200 {
			return string(r[:200]) + "…"
		}
	}
	return v
}

// cleanClient keeps the audit field to printable ASCII, 80 chars.
func cleanClient(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		}
		if b.Len() >= 80 {
			break
		}
	}
	return b.String()
}

// auditEntry is one line of the manager log for a state-changing RPC call
// (operate/admin). Reads are not logged: they change nothing and are frequent.
type auditEntry struct {
	client string
	level  AccessLevel
	method string
	params any
}

func (a auditEntry) logs() bool { return RPCLevel(a.method, "") != LevelRead }

func (a auditEntry) log(result string, extra ...any) {
	if !a.logs() {
		return
	}
	logger.L.Info("control.audit", append([]any{
		"client", a.client, "level", string(a.level), "method", a.method,
		"params", fmt.Sprint(a.params), "result", result,
	}, extra...)...)
}

func (a auditEntry) deny(reason string, need AccessLevel) {
	a.log("denied:"+reason, "need", string(need))
}

func (a auditEntry) done(err error) {
	if err != nil {
		a.log("error", "error", err.Error())
		return
	}
	a.log("ok")
}
