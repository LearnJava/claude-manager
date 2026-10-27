package experience

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/store"
)

func pythonExe(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python", "python3"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("python not available; skipping hook script test")
	return ""
}

func rules(kinds ...string) []store.HookRule {
	var out []store.HookRule
	for i, k := range kinds {
		out = append(out, store.HookRule{ID: int64(i + 1), Kind: k})
	}
	return out
}

// guard runs a generated script on one hook payload, returning the exit code
// and stderr. The state and marker files live under a per-test temp dir.
type guard struct {
	t      *testing.T
	py     string
	script string
	tmp    string
}

func newGuard(t *testing.T, rs []store.HookRule) *guard {
	t.Helper()
	dir := t.TempDir()
	script := filepath.Join(dir, "guard.py")
	if err := os.WriteFile(script, []byte(RenderHookScript(rs)), 0o644); err != nil {
		t.Fatal(err)
	}
	return &guard{t: t, py: pythonExe(t), script: script, tmp: t.TempDir()}
}

func (g *guard) call(event, tool string, input map[string]any, resp string) (int, string) {
	g.t.Helper()
	payload, _ := json.Marshal(map[string]any{
		"hook_event_name": event, "session_id": "s1", "tool_name": tool,
		"tool_input": input, "tool_response": resp,
	})
	cmd := exec.Command(g.py, g.script)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Env = append(os.Environ(), "TMP="+g.tmp, "TEMP="+g.tmp, "TMPDIR="+g.tmp)
	cmd.Dir = g.tmp
	err := cmd.Run()
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), stderr.String()
	}
	if err != nil {
		g.t.Fatalf("run guard: %v", err)
	}
	return 0, stderr.String()
}

func (g *guard) pre(tool string, input map[string]any) (int, string) {
	return g.call("PreToolUse", tool, input, "")
}

func (g *guard) marker() string { return filepath.Join(g.tmp, "cm-guard-s1.allow") }

func TestGuardBlocksRepeatedReadAndExplains(t *testing.T) {
	g := newGuard(t, rules(HookKindRepeatRead))
	in := map[string]any{"file_path": "/p/a.go"}
	if code, _ := g.pre("Read", in); code != 0 {
		t.Fatalf("first read blocked: %d", code)
	}
	code, msg := g.pre("Read", in)
	if code != 2 {
		t.Fatalf("repeat read = %d, want 2", code)
	}
	for _, want := range []string{"already read", "Instead:", ".allow"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal lacks %q: %s", want, msg)
		}
	}
	if code, _ := g.pre("Read", map[string]any{"file_path": "/p/a.go", "offset": 10, "limit": 20}); code != 0 {
		t.Errorf("a slice of the file is a different call, got %d", code)
	}
}

func TestGuardReadAllowedAfterEditOfThatFile(t *testing.T) {
	g := newGuard(t, rules(HookKindRepeatRead))
	in := map[string]any{"file_path": "/p/a.go"}
	g.pre("Read", in)
	g.pre("Edit", map[string]any{"file_path": "/P/A.go"}) // case-insensitive, like the detector
	if code, _ := g.pre("Read", in); code != 0 {
		t.Errorf("read after an edit of the same file blocked: %d", code)
	}
}

func TestGuardMarkerPassesOnceThenBlocksAgain(t *testing.T) {
	g := newGuard(t, rules(HookKindRepeatRead))
	in := map[string]any{"file_path": "/p/a.go"}
	g.pre("Read", in)
	if code, _ := g.pre("Read", in); code != 2 {
		t.Fatal("setup: repeat not blocked")
	}
	if err := os.WriteFile(g.marker(), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, _ := g.pre("Read", in); code != 0 {
		t.Errorf("marker did not let the call through: %d", code)
	}
	if _, err := os.Stat(g.marker()); err == nil {
		t.Error("marker was not consumed")
	}
	if code, _ := g.pre("Read", in); code != 2 {
		t.Errorf("block did not return after the marker was spent: %d", code)
	}
}

func TestGuardBlocksSearchAfterTwoEmptyOnes(t *testing.T) {
	g := newGuard(t, rules(HookKindEmptySearch))
	for _, pat := range []string{"foo", "bar"} {
		if code, _ := g.pre("Grep", map[string]any{"pattern": pat}); code != 0 {
			t.Fatalf("search %s blocked early", pat)
		}
		g.call("PostToolUse", "Grep", map[string]any{"pattern": pat}, "No matches found")
	}
	code, msg := g.pre("Grep", map[string]any{"pattern": "baz"})
	if code != 2 || !strings.Contains(msg, "ls") {
		t.Fatalf("third guess = %d %q, want a block that says to list the directory", code, msg)
	}
	if code, _ := g.pre("Grep", map[string]any{"pattern": "baz"}); code != 0 {
		t.Errorf("after the explanation the search must go through, got %d", code)
	}
}

func TestGuardNonEmptySearchResetsStreak(t *testing.T) {
	g := newGuard(t, rules(HookKindEmptySearch))
	g.call("PostToolUse", "Grep", nil, "No matches found")
	g.call("PostToolUse", "Grep", nil, strings.Repeat("a/b.go:12: hit\n", 20))
	g.call("PostToolUse", "Grep", nil, "No matches found")
	if code, _ := g.pre("Grep", map[string]any{"pattern": "x"}); code != 0 {
		t.Errorf("one empty search after a hit blocked: %d", code)
	}
}

func TestGuardBlocksOversizedWholeRead(t *testing.T) {
	g := newGuard(t, []store.HookRule{{ID: 1, Kind: HookKindOversizedRead, Param: "100"}})
	big := filepath.Join(g.tmp, "big.txt")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	code, msg := g.pre("Read", map[string]any{"file_path": big})
	if code != 2 || !strings.Contains(msg, "offset and limit") {
		t.Fatalf("whole read of a big file = %d %q", code, msg)
	}
	if code, _ := g.pre("Read", map[string]any{"file_path": big, "limit": 50}); code != 0 {
		t.Errorf("a sliced read blocked: %d", code)
	}
	small := filepath.Join(g.tmp, "small.txt")
	os.WriteFile(small, []byte("x"), 0o644)
	if code, _ := g.pre("Read", map[string]any{"file_path": small}); code != 0 {
		t.Errorf("a small file blocked: %d", code)
	}
}

func TestGuardBlocksKnownFailingCommandWithRightForm(t *testing.T) {
	g := newGuard(t, []store.HookRule{{ID: 1, Kind: HookKindFailureCmd, Param: "go test ./...", Hint: "use: go test ./internal/..."}})
	code, msg := g.pre("Bash", map[string]any{"command": "cd x &&   go test   ./... -count=1"})
	if code != 2 || !strings.Contains(msg, "go test ./internal/...") {
		t.Fatalf("known-bad command = %d %q, want a block naming the right form", code, msg)
	}
	if code, _ := g.pre("Bash", map[string]any{"command": "go test ./internal/..."}); code != 0 {
		t.Errorf("the right form blocked: %d", code)
	}
}

func TestGuardFailsOpenOnGarbage(t *testing.T) {
	g := newGuard(t, rules(HookKindRepeatRead))
	cmd := exec.Command(g.py, g.script)
	cmd.Stdin = strings.NewReader("not json")
	if err := cmd.Run(); err != nil {
		t.Errorf("garbage input must exit 0, got %v", err)
	}
}

func TestRenderHookScriptEscapesQuotes(t *testing.T) {
	g := newGuard(t, []store.HookRule{{ID: 1, Kind: HookKindFailureCmd, Param: `echo '''x\"y`, Hint: "h"}})
	if code, _ := g.pre("Bash", map[string]any{"command": `echo '''x\"y`}); code != 2 {
		t.Errorf("rule with triple quotes did not load/match: %d", code)
	}
}

func TestPlanHookCreatesBothFilesAndNothingWhenOff(t *testing.T) {
	dir := t.TempDir()
	plan, err := PlanHook(dir, nil)
	if err != nil || !plan.Empty() {
		t.Fatalf("no rules on a clean repo must plan nothing: %+v %v", plan, err)
	}
	if err := ApplyHookPlan(dir, plan); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("empty plan left files behind: %v", entries)
	}

	plan, err = PlanHook(dir, rules(HookKindRepeatRead))
	if err != nil || len(plan.Files) != 2 {
		t.Fatalf("plan = %+v %v, want script + settings", plan, err)
	}
	for _, f := range plan.Files {
		if !strings.Contains(f.Diff, "+") {
			t.Errorf("%s has no diff", f.Path)
		}
	}
	if err := ApplyHookPlan(dir, plan); err != nil {
		t.Fatal(err)
	}
	again, _ := PlanHook(dir, rules(HookKindRepeatRead))
	if !again.Empty() {
		t.Errorf("planning the installed state again must be empty, got %+v", again)
	}
}

func TestRevertRemovesHookAndRestoresSettings(t *testing.T) {
	dir := t.TempDir()
	settings := filepath.Join(dir, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settings), 0o755)
	orig := "{\n  \"model\": \"opus\",\n  \"hooks\": {\n    \"PreToolUse\": [\n      {\"matcher\": \"Bash\", \"hooks\": [{\"type\": \"command\", \"command\": \"a && b\"}]}\n    ]\n  },\n  \"env\": {\"A\": \"1\"}\n}\n"
	os.WriteFile(settings, []byte(orig), 0o644)

	on, err := PlanHook(dir, rules(HookKindRepeatRead, HookKindEmptySearch))
	if err != nil {
		t.Fatal(err)
	}
	ApplyHookPlan(dir, on)
	got, _ := os.ReadFile(settings)
	for _, want := range []string{"cm-waste-guard.py", "a && b", `"model"`, `"env"`, "PostToolUse"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("installed settings lack %q:\n%s", want, got)
		}
	}
	if strings.Index(string(got), `"model"`) > strings.Index(string(got), `"hooks"`) || strings.Index(string(got), `"hooks"`) > strings.Index(string(got), `"env"`) {
		t.Errorf("top-level key order changed:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(HookScriptRel))); err != nil {
		t.Fatalf("script not written: %v", err)
	}

	off, err := PlanHook(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ApplyHookPlan(dir, off)
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(HookScriptRel))); err == nil {
		t.Error("script survived the revert")
	}
	back, _ := os.ReadFile(settings)
	if strings.Contains(string(back), "cm-waste-guard") || strings.Contains(string(back), "PostToolUse") {
		t.Errorf("registration survived the revert:\n%s", back)
	}
	if !strings.Contains(string(back), "a && b") || !strings.Contains(string(back), `"env"`) {
		t.Errorf("revert damaged the user's own settings:\n%s", back)
	}
}

func TestRevertDeletesSettingsFileWeCreated(t *testing.T) {
	dir := t.TempDir()
	on, _ := PlanHook(dir, rules(HookKindRepeatRead))
	ApplyHookPlan(dir, on)
	off, _ := PlanHook(dir, nil)
	ApplyHookPlan(dir, off)
	if entries, _ := os.ReadDir(filepath.Join(dir, ".claude")); len(entries) != 0 {
		for _, e := range entries {
			t.Errorf("left behind: %s", e.Name())
		}
	}
}

func TestRegisterHookLeavesUnrelatedFileUntouched(t *testing.T) {
	raw := "{\"a\":1,\n \"hooks\":{}}"
	if got, err := registerHook(raw, false); err != nil || got != raw {
		t.Errorf("removing from a file without our entries must not rewrite it: %q %v", got, err)
	}
	if _, err := registerHook("{broken", true); err == nil {
		t.Error("a malformed settings.json must be an error, not overwritten")
	}
}

func TestLineDiffShowsOnlyChangesWithContext(t *testing.T) {
	old := "a\nb\nc\nd\ne\nf\ng\nh\n"
	new := "a\nb\nc\nd\nX\nf\ng\nh\n"
	d := lineDiff(old, new)
	if !strings.Contains(d, "-e") || !strings.Contains(d, "+X") || strings.Contains(d, " a") {
		t.Errorf("diff = %q", d)
	}
	if lineDiff(old, old) != "" {
		t.Error("equal texts must diff to nothing")
	}
}
