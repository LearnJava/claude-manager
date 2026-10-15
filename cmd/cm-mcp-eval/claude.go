package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Metrics is what one agent run cost and how it went, read from the
// stream-json output of `claude -p`.
type Metrics struct {
	Calls      int     `json:"calls"`       // tool calls the agent made
	ErrorCalls int     `json:"error_calls"` // of them, results flagged is_error
	Turns      int     `json:"turns"`
	Tokens     int     `json:"tokens"` // input + output + cache, all of it
	CostUSD    float64 `json:"cost_usd"`
	Answer     string  `json:"-"` // the agent's final text
	Failed     bool    `json:"-"` // the run itself failed (not the task)
}

// AgentConfig describes how the agent is launched.
type AgentConfig struct {
	ClaudePath string
	Model      string
	CMMCPPath  string
	BudgetUSD  float64
	Timeout    time.Duration
}

// writeMCPConfig writes the --mcp-config file that exposes only cm-mcp,
// pointed at the instance's control-plane.
func writeMCPConfig(dir string, in *Instance, cmMCP, level string) (string, error) {
	cfg := map[string]any{"mcpServers": map[string]any{"cm": map[string]any{
		"command": cmMCP,
		"args":    []string{"-level", level},
		"env":     map[string]string{"CM_CONTROL_ADDR": in.Addr, "CM_CONTROL_TOKEN": in.Token},
	}}}
	raw, _ := json.Marshal(cfg)
	p := filepath.Join(dir, "mcp.json")
	return p, os.WriteFile(p, raw, 0o600)
}

// agentPrompt frames a task for the agent: it can reach the manager through
// the cm tools and nothing else.
func agentPrompt(task string) string {
	return "You operate a Claude Manager instance through the MCP server \"cm\" and have no other tools. " +
		"Do the task below, then reply briefly. If the task asks for a value, end your reply with one line " +
		"`ANSWER: <value>`.\n\nTask: " + task
}

// RunAgent runs `claude -p` against the instance with only cm-mcp available
// and returns the metrics. The agent starts in an empty folder, so it has no
// repository to read.
func RunAgent(ctx context.Context, cfg AgentConfig, in *Instance, level, task string) (Metrics, error) {
	work, err := os.MkdirTemp("", "cm-mcp-eval-agent")
	if err != nil {
		return Metrics{}, err
	}
	defer os.RemoveAll(work)
	mcpFile, err := writeMCPConfig(work, in, cfg.CMMCPPath, level)
	if err != nil {
		return Metrics{}, err
	}
	args := []string{
		"-p", agentPrompt(task),
		"--output-format", "stream-json", "--verbose",
		"--mcp-config", mcpFile, "--strict-mcp-config",
		"--tools", "",
		"--allowedTools", "mcp__cm",
		"--setting-sources", "project",
		"--disable-slash-commands",
		"--no-session-persistence",
	}
	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}
	if cfg.BudgetUSD > 0 {
		args = append(args, "--max-budget-usd", fmt.Sprintf("%g", cfg.BudgetUSD))
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, cfg.ClaudePath, args...)
	cmd.Dir = work
	var out, errBuf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errBuf
	runErr := cmd.Run()
	m := ParseStream(out.Bytes())
	if runErr != nil && m.Answer == "" {
		m.Failed = true
		return m, fmt.Errorf("claude: %v: %s", runErr, strings.TrimSpace(tail(errBuf.String(), 400)))
	}
	return m, nil
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// ParseStream reads the stream-json of a `claude -p` run: tool_use blocks are
// calls, tool_result blocks with is_error are failed calls, the result event
// carries usage, cost, turns and the final text.
func ParseStream(raw []byte) Metrics {
	var m Metrics
	sc := bufio.NewScanner(bytes.NewReader(raw))
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
			Result       string  `json:"result"`
			IsError      bool    `json:"is_error"`
			NumTurns     int     `json:"num_turns"`
			TotalCostUSD float64 `json:"total_cost_usd"`
			Usage        struct {
				Input       int `json:"input_tokens"`
				Output      int `json:"output_tokens"`
				CacheCreate int `json:"cache_creation_input_tokens"`
				CacheRead   int `json:"cache_read_input_tokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev.Type {
		case "assistant", "user":
			var blocks []struct {
				Type    string `json:"type"`
				IsError bool   `json:"is_error"`
			}
			if json.Unmarshal(ev.Message.Content, &blocks) != nil {
				continue
			}
			for _, b := range blocks {
				switch b.Type {
				case "tool_use":
					m.Calls++
				case "tool_result":
					if b.IsError {
						m.ErrorCalls++
					}
				}
			}
		case "result":
			m.Turns = ev.NumTurns
			m.CostUSD = ev.TotalCostUSD
			m.Tokens = ev.Usage.Input + ev.Usage.Output + ev.Usage.CacheCreate + ev.Usage.CacheRead
			m.Answer = ev.Result
			m.Failed = ev.IsError
		}
	}
	return m
}

// ExtractAnswer returns the value of the last `ANSWER:` line of a reply.
func ExtractAnswer(text string) string {
	ans := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.Trim(strings.TrimSpace(line), "*`"))
		if rest, ok := strings.CutPrefix(line, "ANSWER:"); ok {
			ans = strings.TrimSpace(strings.Trim(strings.TrimSpace(rest), "*`"))
		}
	}
	return ans
}
