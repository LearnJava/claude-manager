// Command fakehermes is a scripted double of `hermes chat --query-file -
// --format stream-json` (HERMES-TASKS.md HR-03): it lets the manager's Hermes
// runtime be exercised end to end without a real Hermes install or any API
// tokens.
//
// Behaviour, per invocation (= one turn):
//   - reads the whole query from stdin;
//   - reuses the --resume id when given, else mints "fake_<n>";
//   - emits init → a tool_use/tool_result pair → the reply as two text
//     deltas → result, in the exact shape hermes_cli/stream_json.py writes;
//   - the reply is "echo: <first line of the query>", or, when the query
//     contains "FAIL_429", a failed result with a rate-limit error.
//
// FAKEHERMES_LOG, when set, is a file each invocation appends one JSON line
// to ({"args":[…],"query":"…"}), so a test can assert on exactly what the
// manager launched.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout)) }

func run(args []string, in io.Reader, out io.Writer) int {
	query, _ := io.ReadAll(in)
	q := string(query)

	if p := os.Getenv("FAKEHERMES_LOG"); p != "" {
		if f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			line, _ := json.Marshal(map[string]any{"args": args, "query": q})
			_, _ = f.Write(append(line, '\n'))
			_ = f.Close()
		}
	}

	sid, model := "", "fake-model"
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--resume", "-r":
			sid = args[i+1]
		case "-m", "--model":
			model = args[i+1]
		}
	}
	if sid == "" {
		sid = fmt.Sprintf("fake_%d", time.Now().UnixNano())
	}

	emit := func(v map[string]any) {
		v["timestamp"] = time.Now().UnixMilli()
		b, _ := json.Marshal(v)
		fmt.Fprintln(out, string(b))
	}
	emit(map[string]any{"type": "system", "subtype": "init", "model": model, "session_id": sid})

	if strings.Contains(q, "FAIL_429") {
		emit(map[string]any{"type": "result", "session_id": sid, "exit_code": 1, "text": "",
			"error": "HTTP 429: rate limit exceeded", "tokens": map[string]int{}, "duration_ms": 5})
		return 1
	}

	emit(map[string]any{"type": "tool_use", "name": "terminal", "input": map[string]any{"command": "echo hi"}})
	emit(map[string]any{"type": "tool_result", "name": "terminal",
		"output": `{"output": "hi", "exit_code": 0}`, "duration_ms": 3, "is_error": false})

	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(q), "\n", 2)[0])
	reply := "echo: " + first
	half := len(reply) / 2
	emit(map[string]any{"type": "text", "text": reply[:half]})
	emit(map[string]any{"type": "text", "text": reply[half:]})
	emit(map[string]any{"type": "result", "session_id": sid, "exit_code": 0, "text": reply,
		"tokens":      map[string]int{"input": 10, "output": 5, "total": 115, "cache_read": 100, "cache_write": 0},
		"duration_ms": 42})
	fmt.Fprintf(os.Stderr, "\nsession_id: %s\n", sid)
	return 0
}
