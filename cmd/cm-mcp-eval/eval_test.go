package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func testRoot(t *testing.T) string {
	t.Helper()
	wd, _ := os.Getwd()
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func build(t *testing.T, dir, name string) string {
	t.Helper()
	exe := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, "./cmd/"+name+"/")
	cmd.Dir = testRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build %s: %v\n%s", name, err, out)
	}
	return exe
}

// TestTasksSolvableAndChecked runs every task with its scripted agent: the
// check must fail before the agent acts and pass after, so a task that cannot
// be done through the tools, or whose check is vacuous, shows up without a
// model run.
func TestTasksSolvableAndChecked(t *testing.T) {
	bin := t.TempDir()
	claude := build(t, bin, "fakeclaude")
	cmMCP := build(t, bin, "cm-mcp")
	scen := filepath.Join(testRoot(t), "testdata", "scenarios")
	for _, task := range tasks() {
		t.Run(task.ID, func(t *testing.T) {
			sol := solutions[task.ID]
			if sol == nil {
				t.Fatal("no scripted solution")
			}
			in, err := NewInstance(claude, scen)
			if err != nil {
				t.Fatal(err)
			}
			defer in.Close()
			prompt, err := task.Setup(in)
			if err != nil {
				t.Fatalf("setup: %v", err)
			}
			if prompt == "" {
				t.Fatal("empty task text")
			}
			if err := task.Check(in, ""); err == nil {
				t.Fatal("check passes before the agent did anything")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			s, err := ConnectSolver(ctx, cmMCP, in, task.Level)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			st := s.Steps()
			answer := sol(st, in)
			if st.Err() != nil {
				t.Fatalf("solution: %v", st.Err())
			}
			time.Sleep(500 * time.Millisecond)
			if err := task.Check(in, answer); err != nil {
				t.Fatalf("check after solution: %v (answer %q)", err, answer)
			}
		})
	}
}

func TestExtractAnswer(t *testing.T) {
	if got := ExtractAnswer("done\n**ANSWER: 0.03**\n"); got != "0.03" {
		t.Errorf("got %q", got)
	}
	if got := ExtractAnswer("ANSWER: a\nmore\nANSWER: b"); got != "b" {
		t.Errorf("last wins, got %q", got)
	}
	if ExtractAnswer("no answer here") != "" {
		t.Error("want empty")
	}
}

func TestParseStream(t *testing.T) {
	raw := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"mcp__cm__get_sessions"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":true}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","name":"x"},{"type":"text","text":"hi"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","is_error":false}]}}
{"type":"result","subtype":"success","result":"ok\nANSWER: 1","num_turns":3,"total_cost_usd":0.12,"usage":{"input_tokens":10,"output_tokens":5,"cache_creation_input_tokens":100,"cache_read_input_tokens":1000}}
`
	m := ParseStream([]byte(raw))
	if m.Calls != 2 || m.ErrorCalls != 1 || m.Turns != 3 || m.Tokens != 1115 || m.CostUSD != 0.12 || ExtractAnswer(m.Answer) != "1" {
		t.Errorf("%+v", m)
	}
}

func TestNumberNear(t *testing.T) {
	if numberNear("$0.03 USD", 0.03) != nil || numberNear("0.5", 0.03) == nil || numberNear("abc", 1) == nil {
		t.Error("numberNear misjudges")
	}
}

func TestReport(t *testing.T) {
	rs := []Result{
		{Task: Task{ID: "a", Area: "x"}, OK: true, Metrics: Metrics{Calls: 4, ErrorCalls: 1, Tokens: 1000, CostUSD: 0.1}},
		{Task: Task{ID: "b", Area: "y"}, Reason: "boom", Metrics: Metrics{Calls: 2}},
	}
	out := Report(rs, "sonnet")
	for _, want := range []string{"Success: 1/2 (50%)", "Failed calls per task: 0.50", "b failed: boom", "| a | x | yes | 4 | 1 |"} {
		if !strings.Contains(out, want) {
			t.Errorf("report lacks %q:\n%s", want, out)
		}
	}
}
