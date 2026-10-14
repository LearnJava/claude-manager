package cmdguard

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// HookArg is the argument after `hook` that selects this hook:
// `claude-manager hook cmdguard`.
const HookArg = "cmdguard"

// hookInput is the subset of the PreToolUse payload the guard reads.
type hookInput struct {
	ToolName       string         `json:"tool_name"`
	ToolInput      map[string]any `json:"tool_input"`
	Cwd            string         `json:"cwd"`
	PermissionMode string         `json:"permission_mode"`
}

// RunHook implements the PreToolUse hook: read the payload from stdin and,
// when a rule repairs the Bash command, print a decision that substitutes the
// repaired command. It always exits 0 and prints nothing on any problem —
// the guard is an optimisation, and a hook failure must never block a call.
//
// The substitution needs permissionDecision "allow", which also skips the
// permission prompt, so it is only used in bypassPermissions mode where every
// call is allowed anyway. In any other mode the command runs unchanged.
func RunHook(args []string, stdin io.Reader, stdout io.Writer) int {
	if len(args) == 0 || args[0] != HookArg {
		return 0
	}
	data, err := io.ReadAll(io.LimitReader(stdin, 8<<20))
	if err != nil {
		return 0
	}
	var in hookInput
	if json.Unmarshal(data, &in) != nil || in.ToolName != "Bash" {
		return 0
	}
	cmd, _ := in.ToolInput["command"].(string)
	if cmd == "" {
		return 0
	}
	fixed, rules := FixCommand(cmd, in.Cwd, os.Getenv("CLAUDE_PROJECT_DIR"))
	if len(rules) == 0 {
		logCall("-", cmd, "")
		return 0
	}
	if in.PermissionMode != "bypassPermissions" {
		logCall("skipped:"+in.PermissionMode, cmd, fixed)
		return 0
	}
	updated := make(map[string]any, len(in.ToolInput))
	for k, v := range in.ToolInput {
		updated[k] = v
	}
	updated["command"] = fixed
	out, err := json.Marshal(map[string]any{
		"hookSpecificOutput": map[string]any{
			"hookEventName":            "PreToolUse",
			"permissionDecision":       "allow",
			"permissionDecisionReason": "claude-manager cmdguard: " + strings.Join(rules, ", "),
			"updatedInput":             updated,
		},
	})
	if err != nil {
		return 0
	}
	if _, err := stdout.Write(out); err != nil {
		return 0
	}
	logCall(strings.Join(rules, ","), cmd, fixed)
	return 0
}

// logCall appends one line per hook run to ~/.claude-manager/logs/cmdguard.log:
// the outcome ("-" = nothing to fix, "skipped:<mode>" = fix not applied
// outside bypassPermissions, else the rules applied), the command and its
// repair. It is the record that shows how often the hook runs and whether the
// rules pay for that. Best effort.
func logCall(outcome, before, after string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(home, ".claude-manager", "logs", "cmdguard.log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\t%q\t%q\n", time.Now().Format(time.RFC3339), outcome, before, after)
}

// HookCondition limits the hook to Bash calls with a `cd .…` subcommand,
// evaluated by the CLI itself so no process starts for any other call.
// Starting a process costs ~50-100 ms on Windows; run before every Bash call
// that would outweigh the fixes (replay of 95k logged calls: the Windows-path
// rules alone repair 1 call in ~1000). `cd .*` covers 75 of the 97 logged
// failed leading cds and matches 0.6% of calls; the path rules still apply
// to the calls it lets through. The permission-rule syntax of `if` can match
// neither a backslash nor a drive letter (`:` is reserved), so a path-based
// condition is not possible.
const HookCondition = "Bash(cd .*)"

// HookCommand returns the hook command line for exe (the manager binary).
// Forward slashes keep the path intact whether the CLI runs hooks through
// Git Bash or cmd on Windows.
func HookCommand(exe string) string {
	return `"` + filepath.ToSlash(exe) + `" hook ` + HookArg
}

// SettingsJSON returns the --settings value that installs the guard as a
// PreToolUse hook on the Bash tool.
func SettingsJSON(exe string) string {
	b, _ := json.Marshal(map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []any{map[string]any{
				"matcher": "Bash",
				"hooks": []any{map[string]any{
					"type":    "command",
					"if":      HookCondition,
					"command": HookCommand(exe),
					"timeout": 10,
				}},
			}},
		},
	})
	return string(b)
}
