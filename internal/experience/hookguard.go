package experience

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"claude-manager/internal/store"
)

// Hook rule kinds (WASTE-TASKS.md WS-03): rules of the shape "this call must
// not be made", enforced by a PreToolUse hook instead of text in the prompt.
const (
	// HookKindRepeatRead stops a Read/Grep/Glob that repeats an earlier call of
	// the session with no edit in between.
	HookKindRepeatRead = "repeat_read"
	// HookKindEmptySearch stops a search right after two empty ones.
	HookKindEmptySearch = "empty_search"
	// HookKindOversizedRead stops a whole-file Read of a file past Param tokens.
	HookKindOversizedRead = "oversized_read"
	// HookKindFailureCmd stops a Bash command containing Param, a command that
	// failed before and has a known right form (Hint).
	HookKindFailureCmd = "failure_cmd"
)

const (
	// HookScriptRel and HookSettingsRel are the only two files the feature
	// writes in a user's repository (slash paths relative to its root).
	HookScriptRel   = ".claude/hooks/cm-waste-guard.py"
	HookSettingsRel = ".claude/settings.json"
)

//go:embed hookguard.py
var hookGuardTemplate string

// hookRulesPlaceholder is replaced by the rules JSON in the script template.
const hookRulesPlaceholder = "__RULES__"

// RenderHookScript is the text of the guard script for rules.
func RenderHookScript(rules []store.HookRule) string {
	type jr struct {
		Kind  string `json:"kind"`
		Param string `json:"param,omitempty"`
		Hint  string `json:"hint,omitempty"`
	}
	out := make([]jr, 0, len(rules))
	for _, r := range rules {
		out = append(out, jr{Kind: r.Kind, Param: r.Param, Hint: r.Hint})
	}
	b, _ := json.Marshal(out)
	// The JSON sits in a Python r'''...''' literal: no quote may end it.
	text := strings.ReplaceAll(string(b), "'", `\u0027`)
	return strings.Replace(hookGuardTemplate, hookRulesPlaceholder, text, 1)
}

// HookFileChange is one file a hook plan writes (or deletes). Old is empty for
// a new file, New for a deleted one; Diff is a readable line diff.
type HookFileChange struct {
	Path   string
	Old    string
	New    string
	Diff   string
	Delete bool
}

// HookPlan is what installing a set of rules would change in a repository.
type HookPlan struct {
	Files []HookFileChange
}

// Empty reports that applying the plan would change nothing.
func (p HookPlan) Empty() bool { return len(p.Files) == 0 }

// hookCommand runs the guard through python the way the project's own
// guard-self-kill hook is registered.
const hookCommand = `python -c "import os,runpy; d=os.environ.get('CLAUDE_PROJECT_DIR') or os.getcwd(); runpy.run_path(os.path.join(d,'.claude','hooks','cm-waste-guard.py'), run_name='__main__')"`

// PlanHook computes the change that makes projectPath's hook hold exactly the
// rules in active (trial and kept ones): the script, and its registration in
// .claude/settings.json. An empty active set plans the removal of both. It
// reads the repository and writes nothing.
func PlanHook(projectPath string, active []store.HookRule) (HookPlan, error) {
	var plan HookPlan
	scriptPath := filepath.Join(projectPath, filepath.FromSlash(HookScriptRel))
	oldScript, scriptExists, err := readIfExists(scriptPath)
	if err != nil {
		return plan, err
	}
	newScript := ""
	if len(active) > 0 {
		newScript = RenderHookScript(active)
	}
	if newScript != oldScript {
		plan.Files = append(plan.Files, newChange(HookScriptRel, oldScript, newScript, newScript == "" && scriptExists))
	}

	settingsPath := filepath.Join(projectPath, filepath.FromSlash(HookSettingsRel))
	oldSettings, settingsExists, err := readIfExists(settingsPath)
	if err != nil {
		return plan, err
	}
	newSettings, err := registerHook(oldSettings, len(active) > 0)
	if err != nil {
		return plan, fmt.Errorf("%s: %w", HookSettingsRel, err)
	}
	if newSettings != oldSettings {
		plan.Files = append(plan.Files, newChange(HookSettingsRel, oldSettings, newSettings, newSettings == "" && settingsExists))
	}
	return plan, nil
}

func newChange(path, old, new string, del bool) HookFileChange {
	return HookFileChange{Path: path, Old: old, New: new, Diff: lineDiff(old, new), Delete: del}
}

func readIfExists(path string) (string, bool, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

// ApplyHookPlan writes plan under projectPath. The only caller that may run
// it is one acting on an approval in the UI (WASTE-TASKS.md invariant 2).
func ApplyHookPlan(projectPath string, plan HookPlan) error {
	for _, f := range plan.Files {
		path := filepath.Join(projectPath, filepath.FromSlash(f.Path))
		if f.Delete {
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				return err
			}
			// Leave no empty folder the feature created; Remove refuses a
			// non-empty one, which is exactly what we want.
			for dir := filepath.Dir(path); dir != filepath.Clean(projectPath) && os.Remove(dir) == nil; dir = filepath.Dir(dir) {
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(f.New), 0o644); err != nil {
			return err
		}
	}
	return nil
}

// isOurHookEntry reports whether a hooks array element is the guard's.
func isOurHookEntry(entry any) bool {
	b, _ := json.Marshal(entry)
	return bytes.Contains(b, []byte("cm-waste-guard.py"))
}

// registerHook returns settings.json text with the guard registered (on) or
// unregistered (off) under hooks.PreToolUse and hooks.PostToolUse; every other
// key keeps its place and its text. Removing the last of a file's content
// returns "" so the caller deletes a file this feature created.
func registerHook(raw string, on bool) (string, error) {
	var top map[string]json.RawMessage
	var order []string
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &top); err != nil {
			return "", err
		}
		var err error
		if order, err = topLevelKeys(raw); err != nil {
			return "", err
		}
	}
	if top == nil {
		top = map[string]json.RawMessage{}
	}
	hooks := map[string][]any{}
	if h, ok := top["hooks"]; ok {
		if err := json.Unmarshal(h, &hooks); err != nil {
			return "", fmt.Errorf("hooks: %w", err)
		}
	}
	before := marshalPlain(hooks)
	for _, ev := range []string{"PreToolUse", "PostToolUse"} {
		var keep []any
		for _, e := range hooks[ev] {
			if !isOurHookEntry(e) {
				keep = append(keep, e)
			}
		}
		if on {
			matcher := "Read|Grep|Glob|Bash|PowerShell|Edit|Write|MultiEdit|NotebookEdit"
			if ev == "PostToolUse" {
				matcher = "Grep|Glob"
			}
			keep = append(keep, map[string]any{
				"matcher": matcher,
				"hooks":   []any{map[string]any{"type": "command", "command": hookCommand, "timeout": 15}},
			})
		}
		if len(keep) == 0 {
			delete(hooks, ev)
		} else {
			hooks[ev] = keep
		}
	}
	if bytes.Equal(before, marshalPlain(hooks)) {
		return raw, nil // already as wanted: keep the user's own formatting
	}
	if len(hooks) == 0 {
		delete(top, "hooks")
	} else {
		b := marshalPlain(hooks)
		if _, existed := top["hooks"]; !existed {
			order = append(order, "hooks")
		}
		top["hooks"] = b
	}
	if len(top) == 0 {
		return "", nil
	}
	var sb strings.Builder
	sb.WriteString("{\n")
	n := 0
	for _, k := range order {
		v, ok := top[k]
		if !ok {
			continue
		}
		var ind bytes.Buffer
		if err := json.Indent(&ind, v, "  ", "  "); err != nil {
			return "", err
		}
		kb, _ := json.Marshal(k)
		if n > 0 {
			sb.WriteString(",\n")
		}
		fmt.Fprintf(&sb, "  %s: %s", kb, ind.String())
		n++
	}
	sb.WriteString("\n}\n")
	return sb.String(), nil
}

// marshalPlain is json.Marshal without HTML escaping, so a user's `&&` in a
// hook command is not rewritten as &.
func marshalPlain(v any) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimSpace(buf.Bytes())
}

// topLevelKeys returns the keys of a JSON object in file order.
func topLevelKeys(raw string) ([]string, error) {
	dec := json.NewDecoder(strings.NewReader(raw))
	if _, err := dec.Token(); err != nil {
		return nil, err
	}
	var keys []string
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return nil, err
		}
		keys = append(keys, t.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

// lineDiff is a unified-style line diff of old against new with two lines of
// context around each change; "" when they are equal.
func lineDiff(old, new string) string {
	if old == new {
		return ""
	}
	a, b := splitLines(old), splitLines(new)
	// LCS table over lines.
	lcs := make([][]int, len(a)+1)
	for i := range lcs {
		lcs[i] = make([]int, len(b)+1)
	}
	for i := len(a) - 1; i >= 0; i-- {
		for j := len(b) - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	var ops []string
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			ops = append(ops, " "+a[i])
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, "-"+a[i])
			i++
		default:
			ops = append(ops, "+"+b[j])
			j++
		}
	}
	for ; i < len(a); i++ {
		ops = append(ops, "-"+a[i])
	}
	for ; j < len(b); j++ {
		ops = append(ops, "+"+b[j])
	}
	const ctx = 2
	show := make([]bool, len(ops))
	for k, op := range ops {
		if op[0] == ' ' {
			continue
		}
		for d := -ctx; d <= ctx; d++ {
			if k+d >= 0 && k+d < len(ops) {
				show[k+d] = true
			}
		}
	}
	var out []string
	skipped := false
	for k, op := range ops {
		if !show[k] {
			skipped = true
			continue
		}
		if skipped && len(out) > 0 {
			out = append(out, "...")
		}
		skipped = false
		out = append(out, op)
	}
	return strings.Join(out, "\n")
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(strings.ReplaceAll(s, "\r\n", "\n"), "\n"), "\n")
}
