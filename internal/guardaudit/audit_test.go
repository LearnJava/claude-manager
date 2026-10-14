package guardaudit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"claude-manager/internal/experience"
)

// call is one tool_use + tool_result pair for writeTranscript.
type call struct {
	id, tool, input, result string
	isError                 bool
}

// writeTranscript writes a minimal CLI JSONL transcript with the given calls,
// all made from cwd.
func writeTranscript(t *testing.T, path, session, cwd string, calls []call) {
	t.Helper()
	var b strings.Builder
	enc := json.NewEncoder(&b)
	for _, c := range calls {
		input := map[string]any{"command": c.input}
		if c.tool != "Bash" {
			input = map[string]any{"file_path": c.input}
		}
		_ = enc.Encode(map[string]any{
			"type": "assistant", "sessionId": session, "cwd": cwd, "timestamp": "2026-10-14T10:00:00Z",
			"message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": c.id, "name": c.tool, "input": input},
			}},
		})
		_ = enc.Encode(map[string]any{
			"type": "user", "sessionId": session, "cwd": cwd, "timestamp": "2026-10-14T10:00:01Z",
			"message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": c.id, "content": c.result, "is_error": c.isError},
			}},
		})
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestScan_RulesAndUncoveredClusters(t *testing.T) {
	root := t.TempDir()
	cwd := t.TempDir()
	writeTranscript(t, filepath.Join(root, "proj-a", "s1.jsonl"), "s1", cwd, []call{
		{"t1", "Bash", `ls "D:\a\"`, "Exit code 2\n/usr/bin/bash: unexpected EOF while looking for matching `\"'", true},
		{"t2", "Bash", "git status", "clean", false},
		{"t3", "Bash", "rev x", "Exit code 127\n/usr/bin/bash: line 1: rev: command not found", true},
		{"t4", "Read", `C:\big.txt`, "<tool_use_error>File content (31337 tokens) exceeds maximum allowed tokens (25000).</tool_use_error>", true},
	})
	writeTranscript(t, filepath.Join(root, "proj-b", "s2.jsonl"), "s2", cwd, []call{
		{"t1", "Bash", "rev y", "Exit code 127\n/usr/bin/bash: line 3: rev: command not found", true},
		{"t2", "Read", `D:\other.txt`, "File content (40000 tokens) exceeds maximum allowed tokens (25000).", true},
		{"t3", "Read", `D:\third.txt`, "File content (50000 tokens) exceeds maximum allowed tokens (25000).", true},
	})

	rep, err := Scan([]string{root}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Files != 2 || rep.ToolCalls != 7 || rep.BashCalls != 4 || rep.Failed != 6 {
		t.Fatalf("counts: %+v", rep)
	}
	if len(rep.Rules) != 1 || rep.Rules[0].Fixed != 1 || rep.Rules[0].OnFailed != 1 || rep.Rules[0].OnWorking != 0 {
		t.Fatalf("rules: %+v", rep.Rules)
	}
	if len(rep.Broken) != 0 {
		t.Fatalf("broken: %+v", rep.Broken)
	}
	// The fixed call is not an uncovered failure; the two others cluster
	// across sessions despite different line numbers / token counts.
	if len(rep.Uncovered) != 2 {
		t.Fatalf("uncovered: %+v", rep.Uncovered)
	}
	for _, c := range rep.Uncovered {
		if c.Sessions != 2 {
			t.Errorf("cluster %s %q: sessions %d, want 2", c.Tool, c.Key, c.Sessions)
		}
		if strings.HasPrefix(c.Key, "Exit code") || strings.Contains(c.Key, "tool_use_error") {
			t.Errorf("cluster key not cleaned: %q", c.Key)
		}
	}
	// Ties on sessions break by count: Read (3 calls) before Bash (2).
	if rep.Uncovered[0].Tool != "Read" || rep.Uncovered[0].Count != 3 {
		t.Errorf("order: %+v", rep.Uncovered)
	}

	rep, _ = Scan([]string{root}, Options{Project: "PROJ-B"})
	if rep.Files != 1 {
		t.Errorf("project filter: files %d, want 1", rep.Files)
	}
}

// A rule that rewrites a command which succeeded is reported as broken.
func TestScan_RewriteOfWorkingCommandIsBroken(t *testing.T) {
	root := t.TempDir()
	writeTranscript(t, filepath.Join(root, "p", "s.jsonl"), "s", t.TempDir(), []call{
		{"t1", "Bash", `ls "D:\a\"`, "ok", false},
	})
	rep, err := Scan([]string{root}, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Broken) != 1 || rep.Rules[0].OnWorking != 1 {
		t.Fatalf("want one broken rewrite, got %+v / %+v", rep.Broken, rep.Rules)
	}
}

func TestFirstErrorLine(t *testing.T) {
	cases := map[string]string{
		"Exit code 1\n\nfatal: not a git repository":                  "fatal: not a git repository",
		"<tool_use_error>File has not been read yet</tool_use_error>": "File has not been read yet",
		"Exit code 1": "",
		"Exit code 1  fatal: not a git repository": "fatal: not a git repository",
		"": "",
	}
	for in, want := range cases {
		if got := firstErrorLine(in); got != want {
			t.Errorf("firstErrorLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// A failed cd followed by `;` exits 0: the rule's own failure text in the
// output still marks the call failed, not a rewrite of a working command.
// A command the live hook already rewrote (cmdguard.log) counts as live.
func TestScan_EvidenceAndLiveRewrites(t *testing.T) {
	root := t.TempDir()
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	elsewhere := t.TempDir() // exists, but has no "sub"
	// The first call records the project root; two more are made from
	// elsewhere.
	path := filepath.Join(root, "p", "s.jsonl")
	writeTranscript(t, path, "s", project, []call{
		{"t0", "Bash", "pwd", project, false},
	})
	var more strings.Builder
	enc := json.NewEncoder(&more)
	for _, c := range []call{
		{"t1", "Bash", "cd sub; echo done", "/usr/bin/bash: line 1: cd: sub: No such file or directory\ndone", false},
		{"t2", "Bash", "cd sub && ls", "a.txt", false},
	} {
		_ = enc.Encode(map[string]any{"type": "attachment", "cwd": elsewhere})
		_ = enc.Encode(map[string]any{"type": "assistant", "sessionId": "s", "cwd": elsewhere,
			"message": map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "tool_use", "id": c.id, "name": c.tool, "input": map[string]any{"command": c.input}}}}})
		_ = enc.Encode(map[string]any{"type": "user", "sessionId": "s", "cwd": elsewhere,
			"message": map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": c.id, "content": c.result, "is_error": c.isError}}}})
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString(more.String())
	f.Close()

	logPath := filepath.Join(t.TempDir(), "cmdguard.log")
	logText := "2026-10-14T10:00:00+03:00\t-\t\"pwd\"\t\"\"\n" +
		"2026-10-14T10:00:01+03:00\tcd-relative-to-project-root\t\"cd sub && ls\"\t\"cd \\\"x/sub\\\" && ls\"\n"
	if err := os.WriteFile(logPath, []byte(logText), 0o644); err != nil {
		t.Fatal(err)
	}
	live, err := LoadGuardLog(logPath)
	if err != nil || len(live) != 1 || !live["cd sub && ls"] {
		t.Fatalf("LoadGuardLog = %v, %v", live, err)
	}

	rep, err := Scan([]string{root}, Options{LiveFixed: live})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Rules) != 1 {
		t.Fatalf("rules: %+v", rep.Rules)
	}
	r := rep.Rules[0]
	if r.Fixed != 2 || r.OnFailed != 1 || r.Live != 1 || r.OnWorking != 0 || len(rep.Broken) != 0 {
		t.Errorf("rule stat %+v, broken %d", r, len(rep.Broken))
	}

	if rep, _ := Scan([]string{root}, Options{Exclude: []string{"P"}}); rep.Files != 0 {
		t.Errorf("exclude: files %d, want 0", rep.Files)
	}
	if m, err := LoadGuardLog(filepath.Join(t.TempDir(), "missing.log")); err != nil || m != nil {
		t.Errorf("missing log: %v, %v", m, err)
	}
}

// The markdown backend pairs results with calls first-in-first-out, so a
// failure may sit on a neighbouring step; a heredoc on one line is not
// replayed at all.
func TestAdd_MarkdownPairingAndHeredoc(t *testing.T) {
	bash := func(cmd, result string, isErr bool) experience.Step {
		return experience.Step{Kind: experience.StepToolUse, ToolName: "Bash", InputText: cmd, ResultText: result, ResultIsError: isErr}
	}
	traj := experience.Trajectory{Steps: []experience.Step{
		bash(`ls "D:\a\" | grep x`, "", false),
		bash("git status", "/usr/bin/bash: eval: line 1: unexpected EOF while looking for matching `\"'", true),
		bash(`cat > x.bat << 'EOF'  set P=D:\ffmpeg\bin  EOF`, "", false),
	}}
	a := NewAuditor(Options{})
	a.Add("x.md", traj)
	rep := a.Report()
	if len(rep.Broken) != 0 || len(rep.Rules) != 1 || rep.Rules[0].OnFailed != 1 {
		t.Fatalf("rules %+v, broken %+v", rep.Rules, rep.Broken)
	}
}
