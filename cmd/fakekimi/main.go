// fakekimi is a scripted Kimi Code CLI double for tests: it parses the same
// argv shape as the real `kimi -p <prompt> --output-format stream-json
// [--session id] [-m model]` and emits deterministic stream-json, selected by
// substrings in the prompt (the fakehermes convention). No API tokens, no
// real Kimi binary. Scenarios:
//
//   - default: init, an "echo: <prompt>" assistant text, a result with usage.
//   - prompt contains "tool": a tool_use/tool_result pair sharing one id.
//   - "ask-user-marker": a result carrying an ```ask-user fenced question;
//     the next turn's reply contains "proceeding with the chosen budget".
//   - "resume-demo turn 2": reply contains "step 2".
//   - FAIL_429 / FAIL_403 / FAIL_SESSION_NOT_FOUND / FAIL_DESKTOP: the
//     matching stderr line and exit code 1.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func emit(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

func main() {
	var prompt, convID, model string
	args := os.Args[1:]
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-p":
			if i+1 < len(args) {
				i++
				prompt = args[i]
			}
		case "--session":
			if i+1 < len(args) {
				i++
				convID = args[i]
			}
		case "-m":
			if i+1 < len(args) {
				i++
				model = args[i]
			}
		}
	}
	id := convID
	if id == "" {
		id = "kimi-fake-session-1"
	}

	fail := func(line string) {
		fmt.Fprintln(os.Stderr, line)
		os.Exit(1)
	}
	switch {
	case strings.Contains(prompt, "FAIL_429"):
		fail("Error: rate limit exceeded — usage quota is spent, resets soon")
	case strings.Contains(prompt, "FAIL_403"):
		fail("Error: 403 forbidden: authentication required")
	case strings.Contains(prompt, "FAIL_SESSION_NOT_FOUND"):
		fail("Session not found: " + id)
	case strings.Contains(prompt, "FAIL_DESKTOP"):
		fail("This session is in use by another Kimi Code Desktop instance.")
	}

	emit(map[string]any{"type": "system", "subtype": "init", "session_id": id, "model": model})

	result := "echo: " + prompt
	if strings.Contains(prompt, "tool") {
		call := map[string]any{"id": "call-1", "type": "function", "function": map[string]any{"name": "Bash", "arguments": `{"command":"go test ./..."}`}}
		emit(map[string]any{"role": "assistant", "tool_calls": []any{call}})
		emit(map[string]any{"role": "tool", "name": "Bash", "tool_call_id": "call-1", "content": "ok"})
		result = "ran the tool"
	}
	if strings.Contains(prompt, "ask-user-marker") {
		result = "Before I proceed:\n```ask-user\n{\"question\":\"Which timeout should I keep?\",\"options\":[\"Keep 2ms\",\"Keep 5ms\"]}\n```"
	}
	if strings.Contains(prompt, "proceeding") || strings.Contains(prompt, "Keep 2ms") {
		result = "proceeding with the chosen budget"
	}
	if strings.Contains(prompt, "resume-demo turn 2") {
		result = "step 2: continuing after step 1"
	}

	emit(map[string]any{"role": "assistant", "content": result})
	emit(map[string]any{
		"type": "result", "stop_reason": "success", "result": result,
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 5},
	})
}
