package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"claude-manager/internal/testkit"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Getenv("FAKECLAUDE_SCENARIO")))
}

// run is the testable core. It parses CLI flags, loads the scenario, and runs it.
// Returns the scenario's exit code (or 1 on error).
func run(args []string, in io.Reader, out io.Writer, scenarioEnv string) int {
	vars := testkit.Vars{}

	// Parse flags we care about: --session-id, --name, --model, --add-dir
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--session-id" && i+1 < len(args):
			vars.SessionID = args[i+1]
			i++
		case strings.HasPrefix(arg, "--session-id="):
			vars.SessionID = strings.TrimPrefix(arg, "--session-id=")
		case arg == "--name" && i+1 < len(args):
			vars.Name = args[i+1]
			i++
		case strings.HasPrefix(arg, "--name="):
			vars.Name = strings.TrimPrefix(arg, "--name=")
		case arg == "--model" && i+1 < len(args):
			vars.Model = args[i+1]
			i++
		case strings.HasPrefix(arg, "--model="):
			vars.Model = strings.TrimPrefix(arg, "--model=")
		case arg == "--add-dir" && i+1 < len(args):
			if vars.Cwd == "" {
				vars.Cwd = args[i+1]
			}
			i++
		case strings.HasPrefix(arg, "--add-dir="):
			if vars.Cwd == "" {
				vars.Cwd = strings.TrimPrefix(arg, "--add-dir=")
			}
		}
	}

	if scenarioEnv == "" {
		fmt.Fprintln(os.Stderr, "fakeclaude: FAKECLAUDE_SCENARIO not set")
		return 1
	}

	var scenario *testkit.Scenario

	info, err := os.Stat(scenarioEnv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeclaude: cannot stat FAKECLAUDE_SCENARIO %q: %v\n", scenarioEnv, err)
		return 1
	}

	if info.IsDir() {
		// Directory mode: read first stdin line to match scenario.
		firstLine, restReader, err := readFirstLine(in)
		if err != nil && err != io.EOF {
			fmt.Fprintf(os.Stderr, "fakeclaude: reading first line: %v\n", err)
			return 1
		}
		prompt := extractPrompt(firstLine)

		scenarios, err := testkit.LoadScenariosDir(scenarioEnv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fakeclaude: loading scenarios dir: %v\n", err)
			return 1
		}
		scenario = testkit.MatchScenario(scenarios, prompt)
		if scenario == nil {
			fmt.Fprintf(os.Stderr, "fakeclaude: no scenario matched prompt: %q\n", prompt)
			return 1
		}
		// Reconstruct reader so the runner's first await_stdin sees the original line.
		in = io.MultiReader(strings.NewReader(firstLine+"\n"), restReader)
	} else {
		// File mode: load directly.
		scenario, err = testkit.LoadScenario(scenarioEnv)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fakeclaude: loading scenario: %v\n", err)
			return 1
		}
	}

	// Apply scenario defaults for unset vars.
	if vars.Model == "" {
		vars.Model = scenario.Model
	}
	if vars.Cwd == "" {
		if cwd, err := os.Getwd(); err == nil {
			vars.Cwd = cwd
		}
	}
	if vars.SessionID == "" {
		vars.SessionID = "fake-session-id"
	}

	runner := testkit.NewRunner(scenario, vars, in, out)
	code, err := runner.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fakeclaude: runner error: %v\n", err)
		return 1
	}
	return code
}

// readFirstLine reads the first line from r and returns it along with a reader
// for the remaining data.
func readFirstLine(r io.Reader) (string, io.Reader, error) {
	var buf []byte
	oneByte := make([]byte, 1)
	for {
		n, err := r.Read(oneByte)
		if n > 0 {
			if oneByte[0] == '\n' {
				return string(buf), r, nil
			}
			buf = append(buf, oneByte[0])
		}
		if err != nil {
			return string(buf), r, err
		}
	}
}

// extractPrompt parses a stream-json user_message line and returns the message field.
func extractPrompt(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	var m struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return line
	}
	return m.Message
}
