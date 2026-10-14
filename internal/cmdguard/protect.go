package cmdguard

import (
	"regexp"
	"strings"
)

// The manager's configuration (`<project>/.claude-manager/config*.toml`,
// `~/.claude-manager/config.toml`) is owned by its Settings window: the
// manager does not reread hand edits, and the next save overwrites them. A
// session that edits it anyway (a Chat wiring a queue for another session)
// leaves the UI and the file disagreeing. Edit/Write are denied by
// permission rules (ProtectDenyRules); Bash commands that write such a file
// are denied by the hook (ProtectedConfigWrite).

// ProtectHookCondition starts the hook for Bash calls that mention the
// manager's folder at all; ProtectedConfigWrite decides the rest.
const ProtectHookCondition = "Bash(*.claude-manager*)"

// ProtectDenyRules are permission deny rules for the file tools — an Edit rule
// covers Edit, MultiEdit and Write. Deny rules hold in every permission mode,
// bypassPermissions included (checked against the real CLI).
var ProtectDenyRules = []string{
	"Edit(**/.claude-manager/config.toml)",
	"Edit(**/.claude-manager/config.local.toml)",
}

var (
	reConfigFile = regexp.MustCompile(`(?i)\bconfig(\.local)?\.toml\b`)
	// Commands and interpreter idioms that write a file. Reading the config
	// (cat, grep, sed -n, head) stays allowed.
	reWrites = regexp.MustCompile(`(?i)(\bsed\s+(-\w+\s+)*-\w*i|\bperl\b[^|;&]*\s-\w*i|\btee\b|\b(cp|mv|rm|install|truncate|dd)\s|` +
		`>>?\s*["']?[^\s&|;<>]*config(\.local)?\.toml|\bopen\([^)]*["'][wa+]|\.write(_text)?\(|Set-Content|Out-File|Add-Content|writeFile)`)
)

// ProtectedConfigWrite reports whether a Bash command writes the manager's
// configuration: it names `.claude-manager`, a `config.toml` /
// `config.local.toml`, and contains a writing command or idiom.
func ProtectedConfigWrite(cmd string) bool {
	if !strings.Contains(strings.ToLower(cmd), ".claude-manager") {
		return false
	}
	return reConfigFile.MatchString(cmd) && reWrites.MatchString(cmd)
}

// ProtectReason is shown to the agent when a write is denied.
const ProtectReason = "claude-manager: the manager's configuration (.claude-manager/config*.toml, " +
	"~/.claude-manager/config.toml) is changed only in the manager's Settings window — the manager does not " +
	"reread hand edits and overwrites them on the next save. Tell the user which setting to change instead."
