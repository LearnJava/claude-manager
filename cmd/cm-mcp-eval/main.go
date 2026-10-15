// cm-mcp-eval measures how well a real agent manages Claude Manager through
// cm-mcp alone. Opt-in (CM_MCP_EVAL=1): it spends API tokens.
//
// Each task gets a fresh throw-away manager on fakeclaude. `claude -p` starts
// in an empty folder with only the cm-mcp server (--strict-mcp-config, no
// built-in tools), is given the task text, and the outcome is judged from the
// manager's state afterwards. Per task it reports success, tool calls, failed
// calls, turns, tokens and cost.
//
//	CM_MCP_EVAL=1 go run ./cmd/cm-mcp-eval -model sonnet -out docs/mcp-eval-last.md
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// Result is one task's outcome.
type Result struct {
	Task    Task
	Metrics Metrics
	OK      bool
	Reason  string // why the task failed
}

func main() {
	if os.Getenv("CM_MCP_EVAL") != "1" {
		fmt.Fprintln(os.Stderr, "cm-mcp-eval spends API tokens: set CM_MCP_EVAL=1 to run it.")
		os.Exit(2)
	}
	var (
		model   = flag.String("model", "", "model for the agent (claude -p --model); empty = the CLI default")
		only    = flag.String("only", "", "comma-separated task ids to run (default: all)")
		out     = flag.String("out", "", "also write the markdown report to this file")
		budget  = flag.Float64("budget", 1.0, "per-task budget cap in USD")
		timeout = flag.Duration("timeout", 6*time.Minute, "per-task time limit")
		claude  = flag.String("claude", "claude", "the claude CLI to drive the agent")
		list    = flag.Bool("list", false, "list the tasks and exit")
	)
	flag.Parse()

	all := tasks()
	if *list {
		for _, t := range all {
			fmt.Printf("%-30s %-18s %s\n", t.ID, t.Area, t.Level)
		}
		return
	}
	pick := map[string]bool{}
	for _, id := range strings.Split(*only, ",") {
		if id = strings.TrimSpace(id); id != "" {
			pick[id] = true
		}
	}

	root, err := repoRoot()
	if err != nil {
		fatal(err)
	}
	bin, err := os.MkdirTemp("", "cm-mcp-eval-bin")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(bin)
	fakeclaude, err := buildCmd(root, bin, "fakeclaude")
	if err != nil {
		fatal(err)
	}
	cmMCP, err := buildCmd(root, bin, "cm-mcp")
	if err != nil {
		fatal(err)
	}
	scenarios := filepath.Join(root, "testdata", "scenarios")
	agent := AgentConfig{ClaudePath: *claude, Model: *model, CMMCPPath: cmMCP, BudgetUSD: *budget, Timeout: *timeout}

	var results []Result
	for _, t := range all {
		if len(pick) > 0 && !pick[t.ID] {
			continue
		}
		fmt.Fprintf(os.Stderr, "== %s ...\n", t.ID)
		r := runTask(context.Background(), agent, fakeclaude, scenarios, t)
		status := "ok"
		if !r.OK {
			status = "FAIL: " + r.Reason
		}
		fmt.Fprintf(os.Stderr, "   %s (calls %d, errors %d, $%.3f)\n", status, r.Metrics.Calls, r.Metrics.ErrorCalls, r.Metrics.CostUSD)
		results = append(results, r)
	}
	report := Report(results, *model)
	fmt.Print(report)
	if *out != "" {
		if err := os.WriteFile(*out, []byte(report), 0o644); err != nil {
			fatal(err)
		}
	}
}

func runTask(ctx context.Context, agent AgentConfig, fakeclaude, scenarios string, t Task) Result {
	r := Result{Task: t}
	in, err := NewInstance(fakeclaude, scenarios)
	if err != nil {
		r.Reason = "instance: " + err.Error()
		return r
	}
	defer in.Close()
	text, err := t.Setup(in)
	if err != nil {
		r.Reason = "setup: " + err.Error()
		return r
	}
	m, err := RunAgent(ctx, agent, in, t.Level, text)
	r.Metrics = m
	if err != nil {
		r.Reason = err.Error()
		return r
	}
	time.Sleep(500 * time.Millisecond) // let a just-issued action reach the state
	if err := t.Check(in, ExtractAnswer(m.Answer)); err != nil {
		r.Reason = err.Error()
		return r
	}
	r.OK = true
	return r
}

// Report renders the results as a markdown table with totals.
func Report(results []Result, model string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Model: %s. Date: %s.\n\n", orDefault(model, "CLI default"), time.Now().Format("2006-01-02"))
	b.WriteString("| Task | Area | OK | Calls | Failed calls | Turns | Tokens | Cost, USD |\n|---|---|---|---|---|---|---|---|\n")
	var ok, calls, errs, tokens int
	var cost float64
	for _, r := range results {
		mark := "yes"
		if !r.OK {
			mark = "NO"
		} else {
			ok++
		}
		m := r.Metrics
		fmt.Fprintf(&b, "| %s | %s | %s | %d | %d | %d | %d | %.3f |\n", r.Task.ID, r.Task.Area, mark, m.Calls, m.ErrorCalls, m.Turns, m.Tokens, m.CostUSD)
		calls += m.Calls
		errs += m.ErrorCalls
		tokens += m.Tokens
		cost += m.CostUSD
	}
	n := len(results)
	if n == 0 {
		return b.String()
	}
	fmt.Fprintf(&b, "\nSuccess: %d/%d (%.0f%%). Failed calls per task: %.2f. Calls per task: %.1f. Tokens per task: %d. Cost: $%.2f.\n",
		ok, n, 100*float64(ok)/float64(n), float64(errs)/float64(n), float64(calls)/float64(n), tokens/n, cost)
	for _, r := range results {
		if !r.OK {
			fmt.Fprintf(&b, "\n- %s failed: %s\n", r.Task.ID, r.Reason)
		}
	}
	return b.String()
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "cm-mcp-eval:", err)
	os.Exit(1)
}

// repoRoot finds the module root by walking up from the working directory.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found above the working directory")
		}
		dir = parent
	}
}

func buildCmd(root, dir, name string) (string, error) {
	exe := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		exe += ".exe"
	}
	cmd := exec.Command("go", "build", "-o", exe, "./cmd/"+name+"/")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build %s: %v\n%s", name, err, out)
	}
	return exe, nil
}
