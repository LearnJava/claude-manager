// Command fakeopencode is a scripted double of `opencode run --format json`
// (opencode v2): it lets the manager's OpenCode runtime be exercised end to
// end without a real opencode install or any API tokens.
//
// Behaviour, per invocation (= one turn):
//   - reads the whole query from stdin (run reads it when no message
//     argument is given);
//   - reuses the --session id when given, else mints "sesfake_<n>";
//   - emits step_start → a completed tool part → the reply text →
//     step_finish, in the exact shape opencode v2.2.7 writes;
//   - the reply is "echo: <first line of the query>";
//   - "FAIL_429": one {"type":"error"} line and exit 1;
//   - "FAIL_403": the same plus a 403 line on stderr, the shape
//     ClassifyStderrLine recognizes as KindAuth;
//   - "ASK_USER_MARKER": the reply ends in a ```ask-user question block
//     (parser.go's marker), then the process exits — the manager must keep
//     the question pending and feed the answer back as the next invocation.
//
// FAKEOPENCODE_LOG, when set, is a file each invocation appends one JSON
// line to ({"args":[…],"query":"…"}), so a test can assert on exactly what
// the manager launched.
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

	if p := os.Getenv("FAKEOPENCODE_LOG"); p != "" {
		f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fakeopencode: log open %q: %v\n", p, err)
		} else {
			line, _ := json.Marshal(map[string]any{"args": args, "query": q})
			_, _ = f.Write(append(line, '\n'))
			_ = f.Close()
		}
	}

	sid, _ := "", ""
	for i := 0; i < len(args)-1; i++ {
		switch args[i] {
		case "--session", "-s":
			sid = args[i+1]
		}
	}
	if sid == "" {
		sid = fmt.Sprintf("sesfake_%d", time.Now().UnixNano())
	}

	emit := func(v map[string]any) {
		v["timestamp"] = time.Now().UnixMilli()
		v["sessionID"] = sid
		b, _ := json.Marshal(v)
		fmt.Fprintln(out, string(b))
	}
	emitStep := func(t string, part map[string]any) {
		emit(map[string]any{"type": t, "part": part})
	}

	emitStep("step_start", map[string]any{"type": "step-start"})

	if strings.Contains(q, "FAIL_429") || strings.Contains(q, "FAIL_403") {
		msg := "HTTP 429: rate limit exceeded"
		if strings.Contains(q, "FAIL_403") {
			msg = "403 Forbidden — not authenticated"
			fmt.Fprintln(os.Stderr, "API Error: 403 Forbidden — not authenticated")
		}
		emit(map[string]any{"type": "error", "error": map[string]string{"type": "unknown", "message": msg}})
		return 1
	}

	emitStep("tool_use", map[string]any{"type": "tool", "id": "call_fake1", "tool": "shell",
		"state": map[string]any{
			"status": "completed",
			"input":  map[string]any{"command": "echo hi"},
			"output": "hi\n",
		}})

	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(q), "\n", 2)[0])
	reply := "echo: " + first
	// "ASK_USER_MARKER": the turn ends with a ```ask-user question — the
	// process exits right after, which is exactly the state that must leave
	// the question pending manager-side (the answer arrives as the next
	// invocation's query).
	if strings.Contains(q, "ASK_USER_MARKER") {
		reply = "deciding.\n\n```ask-user\n" +
			`{"question": "What next?", "options": ["Option A", "Option B"]}` + "\n```"
	}
	emitStep("text", map[string]any{"type": "text", "text": reply})
	emitStep("step_finish", map[string]any{"type": "step-finish", "reason": "stop", "cost": 0,
		"tokens": map[string]any{"input": 10, "output": 5, "reasoning": 3,
			"cache": map[string]int{"read": 100, "write": 0}}})
	return 0
}
