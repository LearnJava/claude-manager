package cmdguard

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Commands below are taken from saved run logs where they failed (or
// succeeded, for the must-not-change cases).
func TestFixWindowsPaths(t *testing.T) {
	cases := []struct {
		name, in, want string
		rules          []string
	}{
		{"trailing backslash before closing quote",
			`ls "C:\Users\k\.claude\projects\D--Rust\memory\" | grep -i 456`,
			`ls "C:/Users/k/.claude/projects/D--Rust/memory/" | grep -i 456`,
			[]string{RuleQuotedTrailingBackslash}},
		{"trailing backslash at end of command",
			`rmdir "D:\RustProjects\lumen\.claude\worktrees\merge-tmp" 2>&1; ls "D:\RustProjects\lumen\.claude\worktrees\"`,
			`rmdir "D:\RustProjects\lumen\.claude\worktrees\merge-tmp" 2>&1; ls "D:/RustProjects/lumen/.claude/worktrees/"`,
			[]string{RuleQuotedTrailingBackslash}},
		{"two broken strings",
			`grep -rln x "D:\a\docs\" "D:\a\scripts\" 2>/dev/null`,
			`grep -rln x "D:/a/docs/" "D:/a/scripts/" 2>/dev/null`,
			[]string{RuleQuotedTrailingBackslash}},
		{"escaped backslash closes normally",
			`ls "D:\\ffmpeg-dev\\" 2>&1 | head -5`, "", nil},
		{"quoted path without trailing backslash",
			`cat "D:\RustProjects\lumen\Cargo.toml"`, "", nil},
		{"unquoted path loses its backslashes",
			`cd D:\RustProjects\lumen-browser && ls`,
			`cd D:/RustProjects/lumen-browser && ls`,
			[]string{RuleUnquotedWindowsPath}},
		{"unquoted path inside a double-quoted cmd string is left alone",
			`cmd //c "rmdir /s /q D:\RustProjects\lumen\.claude\worktrees\merge-tmp" 2>&1`, "", nil},
		{"single-quoted content is left alone",
			`cmd //c '"C:\Program Files\VC\vcvars64.bat" && set > D:\ffmpeg\vcenv.txt'`, "", nil},
		{"escaped space is not a plain path",
			`ls D:\Program\ Files`, "", nil},
		{"heredoc body is left alone",
			"python - <<'PY'\nprint(\"D:\\a\\\" + 'x')\nls D:\\x\nPY\nls \"D:\\b\\\"",
			"python - <<'PY'\nprint(\"D:\\a\\\" + 'x')\nls D:\\x\nPY\nls \"D:/b/\"",
			[]string{RuleQuotedTrailingBackslash}},
		{"comment with an apostrophe does not open a quote",
			"# don't\nls \"D:\\b\\\"", "# don't\nls \"D:/b/\"", []string{RuleQuotedTrailingBackslash}},
		{"plain command", `git status --short | head`, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, rules := FixCommand(c.in, "", "")
			want := c.want
			if want == "" {
				want = c.in
			}
			if got != want {
				t.Errorf("got  %q\nwant %q", got, want)
			}
			if !reflect.DeepEqual(rules, c.rules) {
				t.Errorf("rules = %v, want %v", rules, c.rules)
			}
		})
	}
}

func TestFixLeadingCd(t *testing.T) {
	root := t.TempDir()
	wt := filepath.Join(root, ".claude", "worktrees", "p1-work")
	if err := os.MkdirAll(filepath.Join(wt, "crates"), 0o755); err != nil {
		t.Fatal(err)
	}
	abs := filepath.ToSlash(wt)
	cases := []struct {
		name, in, cwd, want string
	}{
		{"re-entering the worktree it is already in",
			"cd .claude/worktrees/p1-work && grep -n x crates/a.rs", wt,
			`cd "` + abs + `" && grep -n x crates/a.rs`},
		{"quoted dir, semicolon", `cd ".claude/worktrees/p1-work"; ls`, wt, `cd "` + abs + `" ; ls`},
		{"dir exists under cwd", "cd crates && ls", wt, ""},
		{"dir exists nowhere", "cd nope && ls", wt, ""},
		{"from the root itself", "cd .claude/worktrees/p1-work && ls", root, ""},
		{"absolute dir", "cd /d/x && ls", wt, ""},
		{"not the first segment", "mkdir -p x && cd .claude/worktrees/p1-work", wt, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := FixCommand(c.in, c.cwd, root)
			want := c.want
			if want == "" {
				want = c.in
			}
			if got != want {
				t.Errorf("got  %q\nwant %q", got, want)
			}
		})
	}
}

func runHook(t *testing.T, payload map[string]any) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir()) // keep logFix out of the real home
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	data, _ := json.Marshal(payload)
	var out bytes.Buffer
	if code := RunHook([]string{HookArg}, bytes.NewReader(data), &out); code != 0 {
		t.Fatalf("exit %d", code)
	}
	return out.String()
}

func TestRunHookRewritesInBypassMode(t *testing.T) {
	out := runHook(t, map[string]any{
		"tool_name":       "Bash",
		"permission_mode": "bypassPermissions",
		"tool_input":      map[string]any{"command": `ls "D:\a\"`, "description": "List"},
	})
	var dec struct {
		HookSpecificOutput struct {
			PermissionDecision string         `json:"permissionDecision"`
			UpdatedInput       map[string]any `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &dec); err != nil {
		t.Fatalf("output %q: %v", out, err)
	}
	h := dec.HookSpecificOutput
	if h.PermissionDecision != "allow" || h.UpdatedInput["command"] != `ls "D:/a/"` || h.UpdatedInput["description"] != "List" {
		t.Errorf("decision = %+v", h)
	}
}

func TestRunHookSilentOtherwise(t *testing.T) {
	cases := map[string]map[string]any{
		"default mode keeps the permission prompt": {
			"tool_name": "Bash", "permission_mode": "default",
			"tool_input": map[string]any{"command": `ls "D:\a\"`}},
		"nothing to fix": {
			"tool_name": "Bash", "permission_mode": "bypassPermissions",
			"tool_input": map[string]any{"command": "ls"}},
		"other tool": {
			"tool_name": "Read", "permission_mode": "bypassPermissions",
			"tool_input": map[string]any{"file_path": `D:\a\`}},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			if out := runHook(t, p); out != "" {
				t.Errorf("want no output, got %q", out)
			}
		})
	}
	var out bytes.Buffer
	if RunHook([]string{HookArg}, strings.NewReader("not json"), &out); out.Len() != 0 {
		t.Errorf("bad payload produced output %q", out.String())
	}
}

func TestSettingsJSON(t *testing.T) {
	var s struct {
		Hooks struct {
			PreToolUse []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					If      string `json:"if"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"PreToolUse"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(SettingsJSON(`C:\apps\claude-manager.exe`)), &s); err != nil {
		t.Fatal(err)
	}
	p := s.Hooks.PreToolUse
	if len(p) != 1 || p[0].Matcher != "Bash" || p[0].Hooks[0].Command != `"C:/apps/claude-manager.exe" hook cmdguard` || p[0].Hooks[0].If != "Bash(cd .*)" {
		t.Errorf("settings = %+v", s)
	}
}
